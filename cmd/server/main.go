package main

import (
	"GophProfile/internal/broker"
	"GophProfile/internal/config"
	"GophProfile/internal/filestorage"
	"GophProfile/internal/observability"
	"GophProfile/internal/services"
	"GophProfile/internal/storage"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

const serviceName = "GophProfile"

func Run(ctx context.Context) error {
	config.ParseFlags()

	logger, shutdown, err := observability.Init(ctx, serviceName)
	if err != nil {
		return fmt.Errorf("init observability: %w", err)
	}
	defer shutdown()

	metrics, err := observability.NewAvatarMetrics()
	if err != nil {
		return fmt.Errorf("init metrics: %w", err)
	}

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
