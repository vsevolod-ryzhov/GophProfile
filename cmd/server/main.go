package main

import (
	"GophProfile/internal/breaker"
	"GophProfile/internal/broker"
	"GophProfile/internal/config"
	"GophProfile/internal/filestorage"
	"GophProfile/internal/observability"
	"GophProfile/internal/services"
	"GophProfile/internal/storage"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const serviceName = "GophProfile"

func Run(ctx context.Context) error {
	config.ParseFlags()

	obs, err := observability.Init(ctx, serviceName)
	if err != nil {
		return fmt.Errorf("init observability: %w", err)
	}
	defer obs.Shutdown()
	logger := obs.Logger

	metricsSrv := startMetricsServer(ctx, logger, obs.MetricsHandler, ":9464")
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = metricsSrv.Shutdown(shutdownCtx)
	}()

	metrics, err := observability.NewAvatarMetrics()
	if err != nil {
		return fmt.Errorf("init metrics: %w", err)
	}

	pgStore, storageErr := storage.NewPostgresStorage(config.Options.DatabaseDSN)
	if storageErr != nil {
		return storageErr
	}
	repo := storage.NewBreakerStorage(pgStore, breaker.New(breaker.Settings{
		Name:      "postgres",
		IsFailure: storage.IsPostgresFailure,
		Logger:    logger,
	}))

	minioStore, err := filestorage.NewMinioStorage(
		config.Options.MinioEndpoint,
		config.Options.MinioAccessKey,
		config.Options.MinioSecretKey,
		config.Options.MinioBucket,
		config.Options.MinioUseSSL,
	)
	if err != nil {
		return fmt.Errorf("failed to init minio: %w", err)
	}
	fileStore := filestorage.NewBreakerFileStorage(minioStore, breaker.New(breaker.Settings{
		Name:      "minio",
		IsFailure: filestorage.IsMinioFailure,
		Logger:    logger,
	}))

	pub, err := broker.NewRabbitPublisher(config.Options.RabbitURL, logger)
	if err != nil {
		return fmt.Errorf("failed to init rabbitmq publisher: %w", err)
	}
	defer pub.Close()

	server := services.NewServer(&services.ServerConfig{
		AppPort:  config.Options.AppPort,
		CertFile: config.Options.CertFile,
		KeyFile:  config.Options.KeyFile,
	}, logger, metrics)

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

func startMetricsServer(ctx context.Context, logger *slog.Logger, h http.Handler, addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", h)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		logger.InfoContext(ctx, "starting metrics server", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "metrics server failed", "err", err)
		}
	}()
	return srv
}
