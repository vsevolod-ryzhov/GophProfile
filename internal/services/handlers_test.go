package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"GophProfile/internal/broker"
	brokermocks "GophProfile/internal/broker/mocks"
	fsmocks "GophProfile/internal/filestorage/mocks"
	"GophProfile/internal/model"
	storagemocks "GophProfile/internal/storage/mocks"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"log/slog"
)

func newTestHandler(t *testing.T) (*Handler, *storagemocks.Storage, *fsmocks.FileStorage, *brokermocks.Publisher) {
	t.Helper()
	store := storagemocks.NewStorage(t)
	fs := fsmocks.NewFileStorage(t)
	pub := brokermocks.NewPublisher(t)
	logger := slog.New(slog.DiscardHandler)
	h := NewHandler(store, fs, pub, logger)
	return h, store, fs, pub
}

// --- Health ---

func TestHealth_OK(t *testing.T) {
	h, store, _, _ := newTestHandler(t)
	store.On("Ping", mock.Anything).Return(nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.Health(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp healthResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, "up", resp.Components["postgres"])
}

func TestHealth_Degraded(t *testing.T) {
	h, store, _, _ := newTestHandler(t)
	store.On("Ping", mock.Anything).Return(errors.New("connection refused"))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.Health(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp healthResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "degraded", resp.Status)
	assert.Contains(t, resp.Components["postgres"], "down")
}

// --- AvatarInfo ---

func withChiParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	return r.WithContext(ctx)
}

func TestAvatarInfo_MissingUserID(t *testing.T) {
	h, _, _, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/abc", nil)
	req = withChiParam(req, "avatar_id", "abc")
	w := httptest.NewRecorder()
	h.AvatarInfo(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAvatarInfo_NotFound(t *testing.T) {
	h, store, _, _ := newTestHandler(t)

	aid := uuid.New().String()
	store.On("GetAvatarByID", mock.Anything, aid).Return(nil, sql.ErrNoRows)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+aid, nil)
	req.Header.Set("X-User-ID", "user1")
	req = withChiParam(req, "avatar_id", aid)
	w := httptest.NewRecorder()
	h.AvatarInfo(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAvatarInfo_Forbidden(t *testing.T) {
	h, store, _, _ := newTestHandler(t)

	aid := uuid.New()
	avatar := &model.Avatar{
		ID:     aid,
		UserID: "owner",
	}
	store.On("GetAvatarByID", mock.Anything, aid.String()).Return(avatar, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+aid.String(), nil)
	req.Header.Set("X-User-ID", "other-user")
	req = withChiParam(req, "avatar_id", aid.String())
	w := httptest.NewRecorder()
	h.AvatarInfo(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAvatarInfo_Success(t *testing.T) {
	h, store, _, _ := newTestHandler(t)

	aid := uuid.New()
	now := time.Now().Truncate(time.Second)
	avatar := &model.Avatar{
		ID:               aid,
		UserID:           "user1",
		FileName:         "photo.jpg",
		MimeType:         "image/jpeg",
		SizeBytes:        12345,
		S3Key:            "user1/" + aid.String(),
		ThumbnailS3Keys:  []string{"thumbnails/" + aid.String() + "/100x100.jpg"},
		ProcessingStatus: "completed",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	store.On("GetAvatarByID", mock.Anything, aid.String()).Return(avatar, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+aid.String(), nil)
	req.Header.Set("X-User-ID", "user1")
	req = withChiParam(req, "avatar_id", aid.String())
	w := httptest.NewRecorder()
	h.AvatarInfo(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "user1", body["user_id"])
	assert.Equal(t, "photo.jpg", body["file_name"])
	assert.Equal(t, "completed", body["processing_status"])

	thumbs, ok := body["thumbnails"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, thumbs, 1)
}

func TestAvatarInfo_EmptyThumbnails(t *testing.T) {
	h, store, _, _ := newTestHandler(t)

	aid := uuid.New()
	avatar := &model.Avatar{
		ID:              aid,
		UserID:          "user1",
		ThumbnailS3Keys: nil,
	}
	store.On("GetAvatarByID", mock.Anything, aid.String()).Return(avatar, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+aid.String(), nil)
	req.Header.Set("X-User-ID", "user1")
	req = withChiParam(req, "avatar_id", aid.String())
	w := httptest.NewRecorder()
	h.AvatarInfo(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	thumbs, ok := body["thumbnails"].([]interface{})
	assert.True(t, ok)
	assert.Empty(t, thumbs)
}

// --- AvatarDelete ---

func TestAvatarDelete_MissingUserID(t *testing.T) {
	h, _, _, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/abc", nil)
	req = withChiParam(req, "avatar_id", "abc")
	w := httptest.NewRecorder()
	h.AvatarDelete(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAvatarDelete_NotFound(t *testing.T) {
	h, store, _, _ := newTestHandler(t)

	aid := uuid.New().String()
	store.On("GetAvatarByID", mock.Anything, aid).Return(nil, sql.ErrNoRows)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+aid, nil)
	req.Header.Set("X-User-ID", "user1")
	req = withChiParam(req, "avatar_id", aid)
	w := httptest.NewRecorder()
	h.AvatarDelete(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAvatarDelete_Forbidden(t *testing.T) {
	h, store, _, _ := newTestHandler(t)

	aid := uuid.New()
	avatar := &model.Avatar{ID: aid, UserID: "owner", S3Key: "owner/" + aid.String()}
	store.On("GetAvatarByID", mock.Anything, aid.String()).Return(avatar, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+aid.String(), nil)
	req.Header.Set("X-User-ID", "attacker")
	req = withChiParam(req, "avatar_id", aid.String())
	w := httptest.NewRecorder()
	h.AvatarDelete(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAvatarDelete_Success(t *testing.T) {
	h, store, _, pub := newTestHandler(t)

	aid := uuid.New()
	s3Key := "user1/" + aid.String()
	avatar := &model.Avatar{ID: aid, UserID: "user1", S3Key: s3Key}
	store.On("GetAvatarByID", mock.Anything, aid.String()).Return(avatar, nil)
	store.On("SoftDeleteAvatar", mock.Anything, aid.String()).Return(nil)
	pub.On("PublishDelete", mock.Anything, broker.AvatarDeleteEvent{
		AvatarID: aid.String(),
		S3Keys:   []string{s3Key},
	}).Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+aid.String(), nil)
	req.Header.Set("X-User-ID", "user1")
	req = withChiParam(req, "avatar_id", aid.String())
	w := httptest.NewRecorder()
	h.AvatarDelete(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAvatarDelete_DBError(t *testing.T) {
	h, store, _, _ := newTestHandler(t)

	aid := uuid.New()
	avatar := &model.Avatar{ID: aid, UserID: "user1", S3Key: "user1/" + aid.String()}
	store.On("GetAvatarByID", mock.Anything, aid.String()).Return(avatar, nil)
	store.On("SoftDeleteAvatar", mock.Anything, aid.String()).Return(errors.New("db down"))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+aid.String(), nil)
	req.Header.Set("X-User-ID", "user1")
	req = withChiParam(req, "avatar_id", aid.String())
	w := httptest.NewRecorder()
	h.AvatarDelete(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- extractUserID ---

func TestExtractUserID(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		wantErr bool
	}{
		{"valid", "user-123", false},
		{"valid_email", "user@example.com", false},
		{"empty", "", true},
		{"invalid_chars", "user<script>", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("X-User-ID", tt.header)
			}
			uid, err := extractUserID(req)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.header, uid)
			}
		})
	}
}

// --- routes ---

func TestRoutes(t *testing.T) {
	h, store, _, _ := newTestHandler(t)
	store.On("Ping", mock.Anything).Return(nil)

	srv := NewServer(&ServerConfig{AppPort: ":0"}, slog.New(slog.DiscardHandler))
	r := srv.routes(h)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
