package observability

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "GophProfile"

type Avatars struct {
	UploadsTotal    metric.Int64Counter
	UploadDuration  metric.Float64Histogram
	StorageBytes    metric.Int64UpDownCounter
	ThumbnailsTotal metric.Int64Counter
}

// NewAvatarMetrics creates the avatar instrument set against the global meter.
func NewAvatarMetrics() (*Avatars, error) {
	m := otel.Meter(meterName)

	uploads, err := m.Int64Counter("avatars_uploads_total",
		metric.WithDescription("Total number of avatar uploads"))
	if err != nil {
		return nil, err
	}
	dur, err := m.Float64Histogram("avatars_upload_duration_seconds",
		metric.WithDescription("Avatar upload duration"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60))
	if err != nil {
		return nil, err
	}
	storage, err := m.Int64UpDownCounter("avatars_storage_bytes",
		metric.WithDescription("Total bytes stored across avatar objects"),
		metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	thumbs, err := m.Int64Counter("avatars_thumbnails_total",
		metric.WithDescription("Total number of thumbnails generated"))
	if err != nil {
		return nil, err
	}

	return &Avatars{
		UploadsTotal:    uploads,
		UploadDuration:  dur,
		StorageBytes:    storage,
		ThumbnailsTotal: thumbs,
	}, nil
}
