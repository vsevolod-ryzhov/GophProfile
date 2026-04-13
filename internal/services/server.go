package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"GophProfile/internal/broker"
	"GophProfile/internal/filestorage"
	"GophProfile/internal/storage"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

type ServerConfig struct {
	AppPort  string
	CertFile string
	KeyFile  string
}

type Server struct {
	httpServer *http.Server
	config     *ServerConfig
	logger     *zap.Logger
}

func NewServer(config *ServerConfig, logger *zap.Logger) *Server {
	return &Server{
		config: config,
		logger: logger,
	}
}

func (s *Server) Start(ctx context.Context, store storage.Storage, fileStore filestorage.FileStorage, pub broker.Publisher) error {
	handler := NewHandler(store, fileStore, pub, s.logger)

	s.httpServer = &http.Server{
		Addr:         s.config.AppPort,
		Handler:      s.routes(handler),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		useTLS := s.config.CertFile != "" && s.config.KeyFile != ""
		s.logger.Info("Starting HTTP server",
			zap.String("addr", s.config.AppPort),
			zap.Bool("tls", useTLS),
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
		s.logger.Info("Shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("HTTP server shutdown failed", zap.Error(err))
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
	return r
}
