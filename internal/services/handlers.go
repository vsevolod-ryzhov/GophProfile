package services

import (
	"GophProfile/internal/errs"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"time"

	"GophProfile/internal/broker"
	"GophProfile/internal/filestorage"
	"GophProfile/internal/storage"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

const (
	userIDHeader    = "X-User-ID"
	userIDMaxLength = 255

	imageFormField = "image"
	maxUploadBytes = 10 << 20
	sniffLen       = 512
)

var allowedMIMETypes = map[string]struct{}{
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
}

type uploadedFile struct {
	Data     []byte
	FileName string
	MIMEType string
	Size     int64
}

var userIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._\-@:]+$`)

func extractUserID(r *http.Request) (string, error) {
	raw := r.Header.Get(userIDHeader)
	if raw == "" {
		return "", errs.UserIDHeaderNotFound
	}
	if len(raw) > userIDMaxLength {
		return "", errs.UserIDHeaderExceedsMaximumLength
	}
	if !userIDPattern.MatchString(raw) {
		return "", errs.UserIDHeaderContainsInvalidChar
	}
	return raw, nil
}

func writeJSONError(w http.ResponseWriter, status int, err error, details string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   err.Error(),
		"details": details,
	})
}

type Handler struct {
	storage     storage.Storage
	fileStorage filestorage.FileStorage
	publisher   broker.Publisher
	logger      *zap.Logger
}

func NewHandler(s storage.Storage, fs filestorage.FileStorage, pub broker.Publisher, logger *zap.Logger) *Handler {
	return &Handler{storage: s, fileStorage: fs, publisher: pub, logger: logger}
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

func (h *Handler) AvatarInfo(w http.ResponseWriter, r *http.Request) {
	userID, usrErr := extractUserID(r)
	if usrErr != nil {
		writeJSONError(w, http.StatusBadRequest, usrErr, "")
		return
	}

	avatarID := chi.URLParam(r, "avatar_id")
	if avatarID == "" {
		writeJSONError(w, http.StatusBadRequest, errs.AvatarNotFound, "")
		return
	}

	avatar, err := h.storage.GetAvatarByID(r.Context(), avatarID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errs.AvatarNotFound, "")
			return
		}
		h.logger.Error("failed to get avatar",
			zap.String("avatar_id", avatarID),
			zap.Error(err),
		)
		writeJSONError(w, http.StatusInternalServerError, errs.InternalError, "")
		return
	}

	if avatar.UserID != userID {
		writeJSONError(w, http.StatusForbidden, errs.Forbidden, "")
		return
	}

	thumbnails := make([]map[string]string, 0, len(avatar.ThumbnailS3Keys))
	for _, key := range avatar.ThumbnailS3Keys {
		thumbnails = append(thumbnails, map[string]string{"s3_key": key})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id":                avatar.ID,
		"user_id":           avatar.UserID,
		"file_name":         avatar.FileName,
		"mime_type":         avatar.MimeType,
		"size_bytes":        avatar.SizeBytes,
		"s3_key":            avatar.S3Key,
		"thumbnails":        thumbnails,
		"processing_status": avatar.ProcessingStatus,
		"created_at":        avatar.CreatedAt,
		"updated_at":        avatar.UpdatedAt,
	})
}

func (h *Handler) AvatarUpload(w http.ResponseWriter, r *http.Request) {
	userID, usrErr := extractUserID(r)
	if usrErr != nil {
		writeJSONError(w, http.StatusBadRequest, usrErr, "")
		return
	}

	upload, uploadErr := readUploadedFile(r)
	if uploadErr != nil {
		h.logger.Warn("avatar upload rejected",
			zap.String("user_id", userID),
			zap.Error(uploadErr),
		)

		status := http.StatusBadRequest
		if errors.Is(uploadErr, errs.FileTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSONError(w, status, uploadErr, "")
		return
	}

	avatar, dbErr := h.storage.CreateNewAvatarRecord(r.Context(), userID, upload.FileName, upload.MIMEType, upload.Size)
	if dbErr != nil {
		h.logger.Warn("avatar upload rejected",
			zap.String("user_id", userID),
			zap.Error(dbErr),
		)
		status := http.StatusBadRequest
		writeJSONError(w, status, dbErr, "")
		return
	}

	objectKey := fmt.Sprintf("%s/%s", userID, avatar.ID)
	if err := h.fileStorage.Upload(r.Context(), objectKey, upload.Data); err != nil {
		h.logger.Error("failed to upload file to storage",
			zap.String("user_id", userID),
			zap.String("avatar_id", avatar.ID.String()),
			zap.Error(err),
		)
		writeJSONError(w, http.StatusInternalServerError, errs.InternalError, "")
		return
	}

	if err := h.storage.UpdateAvatarS3Key(r.Context(), avatar.ID.String(), objectKey); err != nil {
		h.logger.Error("failed to update avatar s3 key",
			zap.String("user_id", userID),
			zap.String("avatar_id", avatar.ID.String()),
			zap.Error(err),
		)
		writeJSONError(w, http.StatusInternalServerError, errs.InternalError, "")
		return
	}

	if err := h.publisher.PublishUpload(r.Context(), broker.AvatarUploadEvent{
		AvatarID: avatar.ID.String(),
		UserID:   userID,
		S3Key:    objectKey,
	}); err != nil {
		h.logger.Error("failed to publish upload event",
			zap.String("user_id", userID),
			zap.String("avatar_id", avatar.ID.String()),
			zap.Error(err),
		)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id":         avatar.ID,
		"user_id":    avatar.UserID,
		"url":        fmt.Sprintf("/api/v1/avatars/%s", avatar.ID),
		"status":     "processing",
		"created_at": avatar.CreatedAt,
	})
}

func (h *Handler) AvatarDelete(w http.ResponseWriter, r *http.Request) {
	userID, usrErr := extractUserID(r)
	if usrErr != nil {
		writeJSONError(w, http.StatusBadRequest, usrErr, "")
		return
	}

	avatarID := chi.URLParam(r, "avatar_id")
	if avatarID == "" {
		writeJSONError(w, http.StatusBadRequest, errs.AvatarNotFound, "")
		return
	}

	avatar, err := h.storage.GetAvatarByID(r.Context(), avatarID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errs.AvatarNotFound, "")
			return
		}
		h.logger.Error("failed to get avatar",
			zap.String("avatar_id", avatarID),
			zap.Error(err),
		)
		writeJSONError(w, http.StatusInternalServerError, errs.InternalError, "")
		return
	}

	if avatar.UserID != userID {
		writeJSONError(w, http.StatusForbidden, errs.Forbidden, "")
		return
	}

	if err := h.storage.SoftDeleteAvatar(r.Context(), avatarID); err != nil {
		h.logger.Error("failed to soft-delete avatar",
			zap.String("avatar_id", avatarID),
			zap.Error(err),
		)
		writeJSONError(w, http.StatusInternalServerError, errs.InternalError, "")
		return
	}

	s3Keys := make([]string, 0, 1+len(avatar.ThumbnailS3Keys))
	if avatar.S3Key != "" {
		s3Keys = append(s3Keys, avatar.S3Key)
	}
	s3Keys = append(s3Keys, avatar.ThumbnailS3Keys...)
	if err := h.publisher.PublishDelete(r.Context(), broker.AvatarDeleteEvent{
		AvatarID: avatarID,
		S3Keys:   s3Keys,
	}); err != nil {
		h.logger.Error("failed to publish delete event",
			zap.String("avatar_id", avatarID),
			zap.Error(err),
		)
	}

	w.WriteHeader(http.StatusNoContent)
}

func readUploadedFile(r *http.Request) (*uploadedFile, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxUploadBytes)

	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		if errors.Is(err, http.ErrNotMultipart) || errors.Is(err, http.ErrMissingBoundary) {
			return nil, errs.ExpectedMultipartFormData
		}

		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, errs.FileTooLarge
		}
		return nil, errs.InvalidMultipartBody
	}

	file, header, err := r.FormFile(imageFormField)
	if err != nil {
		return nil, errs.MissingFileField
	}
	defer file.Close()

	if header.Size <= 0 {
		return nil, errs.EmptyFile
	}
	if header.Size > maxUploadBytes {
		return nil, errs.FileTooLarge
	}

	head := make([]byte, sniffLen)
	h, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, errs.FileReadError
	}
	head = head[:h]

	mime := http.DetectContentType(head)
	if _, ok := allowedMIMETypes[mime]; !ok {
		return nil, errs.UnsupportedMediaType
	}

	rest, err := io.ReadAll(file)
	if err != nil {
		return nil, errs.FileReadError
	}
	data := append(head, rest...)

	if int64(len(data)) != header.Size {
		return nil, errs.FileSizeMismatch
	}

	return &uploadedFile{
		Data:     data,
		FileName: filepath.Base(header.Filename),
		MIMEType: mime,
		Size:     header.Size,
	}, nil
}
