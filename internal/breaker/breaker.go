// Package breaker wraps external dependency calls (Postgres, MinIO) in a circuit breaker so a downstream outage trips fast-failing locally instead of stalling every in-flight request behind connection timeouts
package breaker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/sony/gobreaker/v2"
)

// Settings tunes a single breaker. Zero values fall back to conservative defaults
type Settings struct {
	Name             string
	MaxFailures      uint32        // consecutive failures before tripping
	OpenTimeout      time.Duration // how long the breaker stays open before half-opening
	HalfOpenRequests uint32        // probe requests allowed while half-open
	// IsFailure decides whether an error counts against the breaker
	// Returning false keeps "expected" errors (NotFound, cancelled context) out of the failure count
	IsFailure func(error) bool
	Logger    *slog.Logger
}

func (s Settings) withDefaults() Settings {
	if s.MaxFailures == 0 {
		s.MaxFailures = 5
	}
	if s.OpenTimeout == 0 {
		s.OpenTimeout = 30 * time.Second
	}
	if s.HalfOpenRequests == 0 {
		s.HalfOpenRequests = 1
	}
	if s.IsFailure == nil {
		s.IsFailure = DefaultIsFailure
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	return s
}

// ErrOpen is returned by gobreaker when a call is short-circuited because the breaker is open. Re-exported so callers can `errors.Is` without depending on gobreaker directly
var ErrOpen = gobreaker.ErrOpenState

// Breaker wraps a gobreaker so callers can invoke `Do` / `DoTyped` without caring about the underlying generics
type Breaker struct {
	cb        *gobreaker.CircuitBreaker[any]
	isFailure func(error) bool
}

// New constructs a Breaker from Settings
func New(s Settings) *Breaker {
	s = s.withDefaults()
	cb := gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name:        s.Name,
		Timeout:     s.OpenTimeout,
		MaxRequests: s.HalfOpenRequests,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.ConsecutiveFailures >= s.MaxFailures
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			s.Logger.Warn("circuit breaker state change",
				"breaker", name, "from", from.String(), "to", to.String())
		},
		IsSuccessful: func(err error) bool {
			return err == nil || !s.IsFailure(err)
		},
	})
	return &Breaker{cb: cb, isFailure: s.IsFailure}
}

// DefaultIsFailure treats cancelled / deadline-exceeded contexts as non-failures (the client gave up; the dependency is not necessarily unhealthy)
func DefaultIsFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// Do runs a side-effecting function under the breaker
func (b *Breaker) Do(fn func() error) error {
	_, err := b.cb.Execute(func() (any, error) { return nil, fn() })
	return err
}

// DoTyped runs a function that returns a value under the breaker
func DoTyped[T any](b *Breaker, fn func() (T, error)) (T, error) {
	res, err := b.cb.Execute(func() (any, error) { return fn() })
	if err != nil {
		var zero T
		return zero, err
	}
	if res == nil {
		var zero T
		return zero, nil
	}
	return res.(T), nil
}
