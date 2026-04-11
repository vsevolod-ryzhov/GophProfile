package main

import (
	"GophProfile/internal/config"
	"GophProfile/internal/filestorage"
	"GophProfile/internal/services"
	"GophProfile/internal/storage"
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
)

var logger *zap.Logger

func Run(ctx context.Context) error {
	config.ParseFlags()

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

	server := services.NewServer(&services.ServerConfig{
		AppPort:  config.Options.AppPort,
		CertFile: config.Options.CertFile,
		KeyFile:  config.Options.KeyFile,
	}, logger)

	serverErr := server.Start(ctx, repo, fileStore)
	if serverErr != nil {
		return serverErr
	}

	return nil
}

func main() {
	log, err := zap.NewDevelopment()
	logger = log

	if err != nil {
		panic(err)
	}
	defer log.Sync()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if errRun := Run(ctx); errRun != nil {
		logger.Fatal(errRun.Error())
	}
}
