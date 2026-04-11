package services

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"GophProfile/internal/filestorage"
	"GophProfile/internal/storage"

	"go.uber.org/zap"
)

type Handler struct {
	storage     storage.Storage
	fileStorage filestorage.FileStorage
	logger      *zap.Logger
}

func NewHandler(s storage.Storage, fs filestorage.FileStorage, logger *zap.Logger) *Handler {
	return &Handler{storage: s, fileStorage: fs, logger: logger}
}

type healthResponse struct {
	Status     string            `json:"status"`
	Components map[string]string `json:"components"`
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	resp := healthResponse{
		Status:     "ok",
		Components: map[string]string{},
	}

	if err := h.storage.Ping(ctx); err != nil {
		resp.Status = "degraded"
		resp.Components["postgres"] = "down: " + err.Error()
	} else {
		resp.Components["postgres"] = "up"
	}

	w.Header().Set("Content-Type", "application/json")
	if resp.Status != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(resp)
}
