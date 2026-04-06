package storage

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPing_Success(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	s := &PostgresStorage{db: db}

	mock.ExpectPing()

	if pingErr := s.Ping(context.Background()); pingErr != nil {
		t.Errorf("Ping() returned unexpected error: %s", pingErr)
	}

	if anotherErr := mock.ExpectationsWereMet(); anotherErr != nil {
		t.Errorf("there were unfulfilled expectations: %s", anotherErr)
	}
}
