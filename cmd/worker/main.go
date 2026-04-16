package main

import (
	"GophProfile/internal/broker"
	"GophProfile/internal/config"
	"GophProfile/internal/filestorage"
	"GophProfile/internal/storage"
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"os/signal"
	"syscall"

	"github.com/disintegration/imaging"
	"go.uber.org/zap"
)

var thumbnailSizes = []struct {
	Name   string
	Width  int
	Height int
}{
	{"100x100", 100, 100},
	{"300x300", 300, 300},
}

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

	repo, err := storage.NewPostgresStorage(config.Options.DatabaseDSN)
	if err != nil {
		return fmt.Errorf("failed to init postgres: %w", err)
	}

	consumer, err := broker.NewRabbitConsumer(config.Options.RabbitURL)
	if err != nil {
		return fmt.Errorf("failed to init rabbitmq consumer: %w", err)
	}
	defer consumer.Close()

	deletes, err := consumer.ConsumeDeletes(ctx)
	if err != nil {
		return fmt.Errorf("failed to start consuming deletes: %w", err)
	}

	uploads, err := consumer.ConsumeUploads(ctx)
	if err != nil {
		return fmt.Errorf("failed to start consuming uploads: %w", err)
	}

	logger.Info("worker started, waiting for events")

	for {
		select {
		case del, ok := <-deletes:
			if !ok {
				return nil
			}
			handleDelete(ctx, logger, fileStore, del)
		case upl, ok := <-uploads:
			if !ok {
				return nil
			}
			handleUpload(ctx, logger, fileStore, repo, upl)
		case <-ctx.Done():
			logger.Info("worker stopped")
			return nil
		}
	}
}

func handleDelete(ctx context.Context, logger *zap.Logger, fileStore *filestorage.MinioStorage, del broker.DeleteDelivery) {
	log := logger.With(zap.String("avatar_id", del.Event.AvatarID))
	log.Info("received delete event", zap.Int("keys", len(del.Event.S3Keys)))

	for _, key := range del.Event.S3Keys {
		if err := fileStore.Delete(ctx, key); err != nil {
			log.Error("failed to delete S3 object", zap.String("key", key), zap.Error(err))
			if err := del.Nack(); err != nil {
				log.Error("failed to nack message", zap.Error(err))
			}
			return
		}
		log.Info("deleted S3 object", zap.String("key", key))
	}

	if err := del.Ack(); err != nil {
		log.Error("failed to ack message", zap.Error(err))
	}
}

func handleUpload(ctx context.Context, logger *zap.Logger, fileStore *filestorage.MinioStorage, repo *storage.PostgresStorage, upl broker.UploadDelivery) {
	log := logger.With(
		zap.String("avatar_id", upl.Event.AvatarID),
		zap.String("user_id", upl.Event.UserID),
	)
	log.Info("received upload event, generating thumbnails")

	if err := repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "processing"); err != nil {
		log.Error("failed to set processing status", zap.Error(err))
		upl.Nack()
		return
	}

	data, err := fileStore.Download(ctx, upl.Event.S3Key)
	if err != nil {
		log.Error("failed to download original image", zap.Error(err))
		repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "failed")
		upl.Nack()
		return
	}

	src, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		log.Error("failed to decode image", zap.Error(err))
		repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "failed")
		upl.Nack()
		return
	}

	var thumbKeys []string
	for _, size := range thumbnailSizes {
		thumb := imaging.Resize(src, size.Width, size.Height, imaging.Lanczos)

		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 85}); err != nil {
			log.Error("failed to encode thumbnail", zap.String("size", size.Name), zap.Error(err))
			repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "failed")
			upl.Nack()
			return
		}

		key := fmt.Sprintf("thumbnails/%s/%s.jpg", upl.Event.AvatarID, size.Name)
		if err := fileStore.Upload(ctx, key, buf.Bytes()); err != nil {
			log.Error("failed to upload thumbnail", zap.String("key", key), zap.Error(err))
			repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "failed")
			upl.Nack()
			return
		}
		thumbKeys = append(thumbKeys, key)
		log.Info("uploaded thumbnail", zap.String("key", key))
	}

	if err := repo.UpdateThumbnailKeys(ctx, upl.Event.AvatarID, thumbKeys); err != nil {
		log.Error("failed to save thumbnail keys", zap.Error(err))
		repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "failed")
		upl.Nack()
		return
	}

	if err := repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "completed"); err != nil {
		log.Error("failed to set completed status", zap.Error(err))
		upl.Nack()
		return
	}

	if err := upl.Ack(); err != nil {
		log.Error("failed to ack message", zap.Error(err))
	}
	log.Info("thumbnails generated successfully")
}
