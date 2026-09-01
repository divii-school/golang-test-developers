package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAccountCreateReturnsInsertedRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	store := &AccountStore{DB: db}

	created := time.Now()
	mock.ExpectQuery(`INSERT INTO accounts`).
		WithArgs("saikat", "INR").
		WillReturnRows(sqlmock.NewRows([]string{"id", "owner", "balance", "currency", "created_at"}).
			AddRow(1, "saikat", 0, "INR", created))

	a, err := store.Create(context.Background(), "saikat", "INR")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.ID != 1 || a.Owner != "saikat" || a.Balance != 0 || a.Currency != "INR" {
		t.Errorf("Create returned %+v, want id=1 owner=saikat balance=0 currency=INR", a)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAccountGetByIDNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	store := &AccountStore{DB: db}

	mock.ExpectQuery(`FROM accounts WHERE id = \$1`).
		WithArgs(int64(42)).
		WillReturnError(sql.ErrNoRows)

	// Handlers rely on sql.ErrNoRows surviving the storage layer to map it
	// to a 404, so it must come back unwrapped.
	_, err = store.GetByID(context.Background(), 42)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetByID: got %v, want sql.ErrNoRows", err)
	}
}

func TestAccountListReturnsAllRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	store := &AccountStore{DB: db}

	created := time.Now()
	mock.ExpectQuery(`FROM accounts ORDER BY id`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "owner", "balance", "currency", "created_at"}).
			AddRow(1, "a", 100, "INR", created).
			AddRow(2, "b", 200, "USD", created))

	list, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List returned %d accounts, want 2", len(list))
	}
	if list[0].Owner != "a" || list[1].Owner != "b" {
		t.Errorf("List order wrong: %+v", list)
	}
}
