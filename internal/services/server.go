package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"GophProfile/internal/broker"
	"GophProfile/internal/filestorage"
	"GophProfile/internal/observability"
	"GophProfile/internal/storage"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type ServerConfig struct {
	AppPort  string
	CertFile string
	KeyFile  string
}

type Server struct {
	httpServer *http.Server
	config     *ServerConfig
	logger     *slog.Logger
	metrics    *observability.Avatars
}

func NewServer(config *ServerConfig, logger *slog.Logger, metrics *observability.Avatars) *Server {
	return &Server{
		config:  config,
		logger:  logger,
		metrics: metrics,
	}
}

func (s *Server) Start(ctx context.Context, store storage.Storage, fileStore filestorage.FileStorage, pub broker.Publisher) error {
	handler := NewHandler(store, fileStore, pub, s.logger, s.metrics)

	s.httpServer = &http.Server{
		Addr:         s.config.AppPort,
		Handler:      otelhttp.NewHandler(s.routes(handler), "http.server"),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		useTLS := s.config.CertFile != "" && s.config.KeyFile != ""
		s.logger.InfoContext(ctx, "starting http server",
			"addr", s.config.AppPort,
			"tls", useTLS,
		)

		var err error
		if useTLS {
			err = s.httpServer.ListenAndServeTLS(s.config.CertFile, s.config.KeyFile)
		} else {
			err = s.httpServer.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("HTTP server failed: %w", err)
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		s.logger.InfoContext(ctx, "shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.ErrorContext(ctx, "HTTP server shutdown failed", "err", err)
			return fmt.Errorf("HTTP shutdown error: %w", err)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) routes(h *Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/health", h.Health)
	r.Post("/api/v1/avatars", h.AvatarUpload)
	r.Delete("/api/v1/avatars/{avatar_id}", h.AvatarDelete)
	r.Get("/api/v1/avatars/{avatar_id}", h.AvatarInfo)
	r.Get("/api/v1/users/{user_id}/avatars", h.AvatarsListByUser)

	fileServer := http.FileServer(http.Dir("web/static"))
	r.Handle("/*", fileServer)

	return r
}
