package breaker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBreakerOpensAfterMaxFailures(t *testing.T) {
	b := New(Settings{Name: "test", MaxFailures: 3, OpenTimeout: time.Minute})
	bang := errors.New("boom")

	for i := range 3 {
		if err := b.Do(func() error { return bang }); !errors.Is(err, bang) {
			t.Fatalf("call %d: want bang, got %v", i, err)
		}
	}
	// 4th call: breaker is open, should fail fast.
	if err := b.Do(func() error { return nil }); !errors.Is(err, ErrOpen) {
		t.Fatalf("want ErrOpen after threshold, got %v", err)
	}
}

func TestBreakerIgnoresExpectedErrors(t *testing.T) {
	notFound := errors.New("not found")
	b := New(Settings{
		Name:        "test",
		MaxFailures: 2,
		IsFailure:   func(err error) bool { return err != nil && !errors.Is(err, notFound) },
	})

	// 10 "not found"s must not trip the breaker.
	for range 10 {
		_ = b.Do(func() error { return notFound })
	}
	if err := b.Do(func() error { return nil }); err != nil {
		t.Fatalf("breaker tripped on expected error: %v", err)
	}
}

func TestBreakerIgnoresContextCancellation(t *testing.T) {
	b := New(Settings{Name: "test", MaxFailures: 2})
	for range 5 {
		_ = b.Do(func() error { return context.Canceled })
	}
	if err := b.Do(func() error { return nil }); err != nil {
		t.Fatalf("breaker tripped on context.Canceled: %v", err)
	}
}

func TestDoTypedReturnsValueOnSuccess(t *testing.T) {
	b := New(Settings{Name: "test"})
	got, err := DoTyped(b, func() (int, error) { return 42, nil })
	if err != nil || got != 42 {
		t.Fatalf("want (42, nil), got (%d, %v)", got, err)
	}
}
