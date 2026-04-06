package services

import (
	"GophProfile/internal/filestorage"
	"GophProfile/internal/storage"
	"context"
	"net/http"

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

func (s *Server) Start(ctx context.Context, storage storage.Storage, fileStorage filestorage.FileStorage) error {
	return nil
}
