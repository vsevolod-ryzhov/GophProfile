package main

import (
	"GophProfile/internal/broker"
	"GophProfile/internal/config"
	"GophProfile/internal/filestorage"
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
)

func main() {
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic(err)
	}
	defer logger.Sync()

	config.ParseFlags()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, logger); err != nil {
		logger.Fatal(err.Error())
	}
}

func run(ctx context.Context, logger *zap.Logger) error {
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

	consumer, err := broker.NewRabbitConsumer(config.Options.RabbitURL)
	if err != nil {
		return fmt.Errorf("failed to init rabbitmq consumer: %w", err)
	}
	defer consumer.Close()

	events, err := consumer.ConsumeDeletes(ctx)
	if err != nil {
		return fmt.Errorf("failed to start consuming: %w", err)
	}

	logger.Info("worker started, waiting for delete events")

	for del := range events {
		log := logger.With(zap.String("avatar_id", del.Event.AvatarID))
		log.Info("received delete event", zap.Int("keys", len(del.Event.S3Keys)))

		failed := false
		for _, key := range del.Event.S3Keys {
			if err := fileStore.Delete(ctx, key); err != nil {
				log.Error("failed to delete S3 object", zap.String("key", key), zap.Error(err))
				failed = true
				break
			}
			log.Info("deleted S3 object", zap.String("key", key))
		}

		if failed {
			if err := del.Nack(); err != nil {
				log.Error("failed to nack message", zap.Error(err))
			}
		} else {
			if err := del.Ack(); err != nil {
				log.Error("failed to ack message", zap.Error(err))
			}
		}
	}

	logger.Info("worker stopped")
	return nil
}
