package main

import (
	"GophProfile/internal/broker"
	"GophProfile/internal/config"
	"GophProfile/internal/filestorage"
	"GophProfile/internal/services"
	"GophProfile/internal/storage"
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

const serviceName = "GophProfile"

func InitLoggerProvider(ctx context.Context) (*slog.Logger, func()) {
	exporter, err := otlploggrpc.New(ctx)
	if err != nil {
		log.Fatalf("failed to create OTLP log exporter: %v", err)
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		log.Fatalf("failed to create resource: %v", err)
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)

	otelHandler := otelslog.NewHandler(serviceName, otelslog.WithLoggerProvider(loggerProvider))
	stdoutHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(fanoutHandler{handlers: []slog.Handler{stdoutHandler, otelHandler}})
	slog.SetDefault(logger)

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := loggerProvider.Shutdown(ctx); err != nil {
			otel.Handle(err)
		}
	}

	return logger, shutdown
}

// send to both stdout (for `docker logs`) and the OTel pipeline (for Loki).
type fanoutHandler struct {
	handlers []slog.Handler
}

func (f fanoutHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range f.handlers {
		if err := h.Handle(ctx, r.Clone()); err != nil {
			return err
		}
	}
	return nil
}

func (f fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		out[i] = h.WithAttrs(attrs)
	}
	return fanoutHandler{handlers: out}
}

func (f fanoutHandler) WithGroup(name string) slog.Handler {
	out := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		out[i] = h.WithGroup(name)
	}
	return fanoutHandler{handlers: out}
}

func Run(ctx context.Context) error {
	config.ParseFlags()

	logger, otelShutdown := InitLoggerProvider(ctx)
	defer otelShutdown()

	repo, storageErr := storage.NewPostgresStorage(config.Options.DatabaseDSN)
	if storageErr != nil {
		return storageErr
	}

	fileStore, err := filestorage.NewMinioStorage(
		config.Options.MinioEndpoint,
		config.Options.MinioAccessKey,
		config.Options.MinioSecretKey,
		config.Options.MinioBucket,
		config.Options.MinioUseSSL,
	)
	if err != nil {
		return fmt.Errorf("failed to init minio: %w", err)
	}

	pub, err := broker.NewRabbitPublisher(config.Options.RabbitURL, logger)
	if err != nil {
		return fmt.Errorf("failed to init rabbitmq publisher: %w", err)
	}
	defer pub.Close()

	server := services.NewServer(&services.ServerConfig{
		AppPort:  config.Options.AppPort,
		CertFile: config.Options.CertFile,
		KeyFile:  config.Options.KeyFile,
	}, logger)

	return server.Start(ctx, repo, fileStore, pub)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := Run(ctx); err != nil {
		slog.ErrorContext(ctx, "server exited with error", "err", err)
		os.Exit(1)
	}
}
