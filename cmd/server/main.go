package main

import (
	"GophProfile/internal/broker"
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

func Run(ctx context.Context, logger *zap.Logger) error {
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

	pub, err := broker.NewRabbitPublisher(config.Options.RabbitURL)
	if err != nil {
		return fmt.Errorf("failed to init rabbitmq publisher: %w", err)
	}
	defer pub.Close()

	server := services.NewServer(&services.ServerConfig{
		AppPort:  config.Options.AppPort,
		CertFile: config.Options.CertFile,
		KeyFile:  config.Options.KeyFile,
	}, logger)

	serverErr := server.Start(ctx, repo, fileStore, pub)
	if serverErr != nil {
		return serverErr
	}

	return nil
}

func main() {
	log, err := zap.NewDevelopment()

	if err != nil {
		panic(err)
	}
	defer log.Sync()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if errRun := Run(ctx, log); errRun != nil {
		log.Fatal(errRun.Error())
	}
}
