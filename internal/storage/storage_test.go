package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMockStorage(t *testing.T) (*PostgresStorage, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &PostgresStorage{db: db}, mock
}

func TestPing_Success(t *testing.T) {
	s, mock := newMockStorage(t)
	mock.ExpectPing()

	if err := s.Ping(context.Background()); err != nil {
		t.Errorf("Ping() returned unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestPing_Error(t *testing.T) {
	s, mock := newMockStorage(t)
	wantErr := errors.New("connection refused")
	mock.ExpectPing().WillReturnError(wantErr)

	err := s.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping() returned nil, want error")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Ping() = %v, want wrapped %v", err, wantErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestPing_ContextCanceled(t *testing.T) {
	s, mock := newMockStorage(t)
	mock.ExpectPing()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Ping(ctx); err == nil {
		t.Error("Ping() with canceled context returned nil, want error")
	}
}
