package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":        slog.LevelInfo,
		"info":    slog.LevelInfo,
		"INFO":    slog.LevelInfo,
		"debug":   slog.LevelDebug,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"bogus":   slog.LevelInfo,
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFanoutHandler_DispatchesToAll(t *testing.T) {
	var a, b bytes.Buffer
	h := fanoutHandler{handlers: []slog.Handler{
		slog.NewJSONHandler(&a, nil),
		slog.NewJSONHandler(&b, nil),
	}}
	logger := slog.New(h)
	logger.Info("hello", "k", "v")

	for name, buf := range map[string]*bytes.Buffer{"a": &a, "b": &b} {
		if !strings.Contains(buf.String(), `"msg":"hello"`) {
			t.Errorf("handler %s missing msg: %q", name, buf.String())
		}
	}
}

func TestTraceContextHandler_AddsIDs(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	spanID, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	var buf bytes.Buffer
	h := traceContextHandler{Handler: slog.NewJSONHandler(&buf, nil)}
	slog.New(h).InfoContext(ctx, "msg")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if record["trace_id"] != traceID.String() {
		t.Errorf("trace_id = %v, want %s", record["trace_id"], traceID)
	}
	if record["span_id"] != spanID.String() {
		t.Errorf("span_id = %v, want %s", record["span_id"], spanID)
	}
}

func TestTraceContextHandler_NoSpanNoIDs(t *testing.T) {
	var buf bytes.Buffer
	h := traceContextHandler{Handler: slog.NewJSONHandler(&buf, nil)}
	slog.New(h).InfoContext(context.Background(), "msg")

	if strings.Contains(buf.String(), "trace_id") {
		t.Errorf("unexpected trace_id without active span: %s", buf.String())
	}
}

func TestNewAvatarMetrics(t *testing.T) {
	m, err := NewAvatarMetrics()
	if err != nil {
		t.Fatalf("NewAvatarMetrics: %v", err)
	}
	if m.UploadsTotal == nil || m.UploadDuration == nil || m.StorageBytes == nil || m.ThumbnailsTotal == nil {
		t.Errorf("instruments should be non-nil: %+v", m)
	}
	// instruments must be safe to call even with the noop global meter.
	m.UploadsTotal.Add(context.Background(), 1)
	m.UploadDuration.Record(context.Background(), 0.1)
	m.StorageBytes.Add(context.Background(), 100)
	m.ThumbnailsTotal.Add(context.Background(), 1)
}
