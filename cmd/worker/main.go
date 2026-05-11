package main

import (
	"GophProfile/internal/breaker"
	"GophProfile/internal/broker"
	"GophProfile/internal/config"
	"GophProfile/internal/filestorage"
	"GophProfile/internal/observability"
	"GophProfile/internal/storage"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/disintegration/imaging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "GophProfile-worker"

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

	obs, err := observability.Init(ctx, serviceName)
	if err != nil {
		slog.ErrorContext(ctx, "init observability", "err", err)
		os.Exit(1)
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
		logger.ErrorContext(ctx, "init metrics", "err", err)
		os.Exit(1)
	}

	if err := run(ctx, logger, metrics); err != nil {
		logger.ErrorContext(ctx, "worker exited with error", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, metrics *observability.Avatars) error {
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

	pgStore, err := storage.NewPostgresStorage(config.Options.DatabaseDSN)
	if err != nil {
		return fmt.Errorf("failed to init postgres: %w", err)
	}
	repo := storage.NewBreakerStorage(pgStore, breaker.New(breaker.Settings{
		Name:      "postgres",
		IsFailure: storage.IsPostgresFailure,
		Logger:    logger,
	}))

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

	logger.InfoContext(ctx, "worker started, waiting for events")

	for {
		select {
		case del, ok := <-deletes:
			if !ok {
				return nil
			}
			handleDelete(logger, fileStore, del)
		case upl, ok := <-uploads:
			if !ok {
				return nil
			}
			handleUpload(logger, fileStore, repo, metrics, upl)
		case <-ctx.Done():
			logger.InfoContext(ctx, "worker stopped")
			return nil
		}
	}
}

func handleDelete(logger *slog.Logger, fileStore filestorage.FileStorage, del broker.DeleteDelivery) {
	ctx, span := otel.Tracer("worker").Start(del.Context, "process_delete", trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()
	span.SetAttributes(attribute.String("avatar.id", del.Event.AvatarID))

	log := logger.With("avatar_id", del.Event.AvatarID)
	log.InfoContext(ctx, "received delete event", "keys", len(del.Event.S3Keys))

	for _, key := range del.Event.S3Keys {
		if err := fileStore.Delete(ctx, key); err != nil {
			log.ErrorContext(ctx, "failed to delete S3 object", "key", key, "err", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			if err := del.Nack(); err != nil {
				log.ErrorContext(ctx, "failed to nack message", "err", err)
			}
			return
		}
		log.InfoContext(ctx, "deleted S3 object", "key", key)
	}

	if err := del.Ack(); err != nil {
		log.ErrorContext(ctx, "failed to ack message", "err", err)
	}
}

// failPermanent marks the avatar as failed and Acks the message so Rabbit drops it.
func failPermanent(ctx context.Context, log *slog.Logger, repo storage.Storage, upl broker.UploadDelivery) {
	if err := repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "failed"); err != nil {
		log.ErrorContext(ctx, "failed to set failed status", "err", err)
	}
	if err := upl.Ack(); err != nil {
		log.ErrorContext(ctx, "failed to ack message", "err", err)
	}
}

// failTransient marks the avatar as failed and Nacks so the message is requeued.
func failTransient(ctx context.Context, log *slog.Logger, repo storage.Storage, upl broker.UploadDelivery) {
	if err := repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "failed"); err != nil {
		log.ErrorContext(ctx, "failed to set failed status", "err", err)
	}
	if err := upl.Nack(); err != nil {
		log.ErrorContext(ctx, "failed to nack message", "err", err)
	}
}

func handleUpload(logger *slog.Logger, fileStore filestorage.FileStorage, repo storage.Storage, metrics *observability.Avatars, upl broker.UploadDelivery) {
	ctx, span := otel.Tracer("worker").Start(upl.Context, "process_upload", trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()
	span.SetAttributes(
		attribute.String("avatar.id", upl.Event.AvatarID),
		attribute.String("user.id", upl.Event.UserID),
	)

	log := logger.With(
		"avatar_id", upl.Event.AvatarID,
		"user_id", upl.Event.UserID,
	)
	log.InfoContext(ctx, "received upload event, generating thumbnails")

	avatar, err := repo.GetAvatarByID(ctx, upl.Event.AvatarID)
	if err != nil {
		log.ErrorContext(ctx, "failed to load avatar", "err", err)
		span.RecordError(err)
		if nackErr := upl.Nack(); nackErr != nil {
			log.ErrorContext(ctx, "failed to nack message", "err", nackErr)
		}
		return
	}
	if avatar.ProcessingStatus == "completed" {
		log.InfoContext(ctx, "skipping already-processed upload")
		if err := upl.Ack(); err != nil {
			log.ErrorContext(ctx, "failed to ack message", "err", err)
		}
		return
	}

	if err := repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "processing"); err != nil {
		log.ErrorContext(ctx, "failed to set processing status", "err", err)
		if nackErr := upl.Nack(); nackErr != nil {
			log.ErrorContext(ctx, "failed to nack message", "err", nackErr)
		}
		return
	}

	data, err := fileStore.Download(ctx, upl.Event.S3Key)
	if err != nil {
		log.ErrorContext(ctx, "failed to download original image", "err", err)
		span.RecordError(err)
		failPermanent(ctx, log, repo, upl)
		return
	}

	src, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		log.ErrorContext(ctx, "failed to decode image", "err", err)
		span.RecordError(err)
		failPermanent(ctx, log, repo, upl)
		return
	}

	var thumbKeys []string
	for _, size := range thumbnailSizes {
		thumb := imaging.Resize(src, size.Width, size.Height, imaging.Lanczos)

		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 85}); err != nil {
			log.ErrorContext(ctx, "failed to encode thumbnail", "size", size.Name, "err", err)
			span.RecordError(err)
			failPermanent(ctx, log, repo, upl)
			return
		}

		key := fmt.Sprintf("thumbnails/%s/%s.jpg", upl.Event.AvatarID, size.Name)
		if err := fileStore.Upload(ctx, key, buf.Bytes()); err != nil {
			log.ErrorContext(ctx, "failed to upload thumbnail", "key", key, "err", err)
			span.RecordError(err)
			failTransient(ctx, log, repo, upl)
			return
		}
		thumbKeys = append(thumbKeys, key)
		metrics.ThumbnailsTotal.Add(ctx, 1, attributeFor("size", size.Name))
		metrics.StorageBytes.Add(ctx, int64(buf.Len()))
		log.InfoContext(ctx, "uploaded thumbnail", "key", key)
	}

	if err := repo.UpdateThumbnailKeys(ctx, upl.Event.AvatarID, thumbKeys); err != nil {
		log.ErrorContext(ctx, "failed to save thumbnail keys", "err", err)
		span.RecordError(err)
		failTransient(ctx, log, repo, upl)
		return
	}

	if err := repo.UpdateProcessingStatus(ctx, upl.Event.AvatarID, "completed"); err != nil {
		log.ErrorContext(ctx, "failed to set completed status", "err", err)
		span.RecordError(err)
		if nackErr := upl.Nack(); nackErr != nil {
			log.ErrorContext(ctx, "failed to nack message", "err", nackErr)
		}
		return
	}

	if err := upl.Ack(); err != nil {
		log.ErrorContext(ctx, "failed to ack message", "err", err)
	}
	log.InfoContext(ctx, "thumbnails generated successfully")
}

func attributeFor(k, v string) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String(k, v))
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
