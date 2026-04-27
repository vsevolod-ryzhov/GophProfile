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
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/disintegration/imaging"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

const serviceName = "GophProfile-worker"

func initLogger(ctx context.Context) (*slog.Logger, func()) {
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

	provider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)

	otelHandler := otelslog.NewHandler(serviceName, otelslog.WithLoggerProvider(provider))
	stdoutHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(fanout{otelHandler, stdoutHandler})
	slog.SetDefault(logger)

	return logger, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			otel.Handle(err)
		}
	}
}

type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}
func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range f {
		if err := h.Handle(ctx, r.Clone()); err != nil {
			return err
		}
	}
	return nil
}
func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}
func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}

var thumbnailSizes = []struct {
	Name   string
	Width  int
	Height int
}{
	{"100x100", 100, 100},
	{"300x300", 300, 300},
}

func main() {
	config.ParseFlags()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger, shutdown := initLogger(ctx)
	defer shutdown()

	if err := run(ctx, logger); err != nil {
		logger.ErrorContext(ctx, "worker exited with error", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
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

	consumer, err := broker.NewRabbitConsumer(config.Options.RabbitURL, logger)
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

func handleDelete(ctx context.Context, logger *slog.Logger, fileStore filestorage.FileStorage, del broker.DeleteDelivery) {
	log := logger.With("avatar_id", del.Event.AvatarID)
	log.Info("received delete event", "keys", len(del.Event.S3Keys))

	for _, key := range del.Event.S3Keys {
		if err := fileStore.Delete(ctx, key); err != nil {
			log.Error("failed to delete S3 object", "key", key, "err", err)
			if err := del.Nack(); err != nil {
				log.Error("failed to nack message", "err", err)
			}
			return
		}
		log.Info("deleted S3 object", "key", key)
	}

	if err := del.Ack(); err != nil {
		log.Error("failed to ack message", "err", err)
	}
}

// failPermanent marks the avatar as failed and Acks the message so Rabbit drops it — use when the error is not worth retrying (bad image, missing object, etc).
func failPermanent(ctx context.Context, log *slog.Logger, repo storage.Storage, upl broker.UploadDelivery) {
	if err := repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "failed"); err != nil {
		log.Error("failed to set failed status", "err", err)
	}
	if err := upl.Ack(); err != nil {
		log.Error("failed to ack message", "err", err)
	}
}

// failTransient marks the avatar as failed and Nacks so the message is requeued — use for errors that may succeed on retry (broker/storage hiccups).
func failTransient(ctx context.Context, log *slog.Logger, repo storage.Storage, upl broker.UploadDelivery) {
	if err := repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "failed"); err != nil {
		log.Error("failed to set failed status", "err", err)
	}
	if err := upl.Nack(); err != nil {
		log.Error("failed to nack message", "err", err)
	}
}

func handleUpload(ctx context.Context, logger *slog.Logger, fileStore filestorage.FileStorage, repo storage.Storage, upl broker.UploadDelivery) {
	log := logger.With(
		"avatar_id", upl.Event.AvatarID,
		"user_id", upl.Event.UserID,
	)
	log.Info("received upload event, generating thumbnails")

	avatar, err := repo.GetAvatarByID(ctx, upl.Event.AvatarID)
	if err != nil {
		log.Error("failed to load avatar", "err", err)
		upl.Nack()
		return
	}
	if avatar.ProcessingStatus == "completed" {
		log.Info("skipping already-processed upload")
		if err := upl.Ack(); err != nil {
			log.Error("failed to ack message", "err", err)
		}
		return
	}

	if err := repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "processing"); err != nil {
		log.Error("failed to set processing status", "err", err)
		upl.Nack()
		return
	}

	data, err := fileStore.Download(ctx, upl.Event.S3Key)
	if err != nil {
		log.Error("failed to download original image", "err", err)
		failPermanent(ctx, log, repo, upl) // object missing, retrying won't help
		return
	}

	src, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		log.Error("failed to decode image", "err", err)
		failPermanent(ctx, log, repo, upl) // bad image data, retrying won't help
		return
	}

	var thumbKeys []string
	for _, size := range thumbnailSizes {
		thumb := imaging.Resize(src, size.Width, size.Height, imaging.Lanczos)

		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 85}); err != nil {
			log.Error("failed to encode thumbnail", "size", size.Name, "err", err)
			failPermanent(ctx, log, repo, upl) // encode won't succeed on retry
			return
		}

		key := fmt.Sprintf("thumbnails/%s/%s.jpg", upl.Event.AvatarID, size.Name)
		if err := fileStore.Upload(ctx, key, buf.Bytes()); err != nil {
			log.Error("failed to upload thumbnail", "key", key, "err", err)
			failTransient(ctx, log, repo, upl)
			return
		}
		thumbKeys = append(thumbKeys, key)
		log.Info("uploaded thumbnail", "key", key)
	}

	if err := repo.UpdateThumbnailKeys(ctx, upl.Event.AvatarID, thumbKeys); err != nil {
		log.Error("failed to save thumbnail keys", "err", err)
		failTransient(ctx, log, repo, upl)
		return
	}

	if err := repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "completed"); err != nil {
		log.Error("failed to set completed status", "err", err)
		upl.Nack()
		return
	}

	if err := upl.Ack(); err != nil {
		log.Error("failed to ack message", "err", err)
	}
	log.Info("thumbnails generated successfully")
}
