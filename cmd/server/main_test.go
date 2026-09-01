package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"bank-api/internal/storage"
	"bank-api/internal/worker"
)

func testMux(t *testing.T) (*http.ServeMux, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	accounts := &storage.AccountStore{DB: db}
	transfers := &storage.TransferStore{DB: db}
	w := worker.New(transfers, logger)
	return newMux(logger, accounts, transfers, w), mock
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHealth(t *testing.T) {
	mux, _ := testMux(t)
	rec := do(t, mux, http.MethodGet, "/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health: got %d, want 200", rec.Code)
	}
}

func TestCreateAccountValidation(t *testing.T) {
	mux, _ := testMux(t)
	cases := []struct {
		name, body string
	}{
		{"invalid JSON", `{not json`},
		{"missing owner", `{"currency":"INR"}`},
		{"missing currency", `{"owner":"saikat"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, mux, http.MethodPost, "/accounts", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("POST /accounts %s: got %d, want 400", tc.name, rec.Code)
			}
		})
	}
}

func TestCreateAccountSuccess(t *testing.T) {
	mux, mock := testMux(t)
	mock.ExpectQuery(`INSERT INTO accounts`).
		WithArgs("saikat", "INR").
		WillReturnRows(sqlmock.NewRows([]string{"id", "owner", "balance", "currency", "created_at"}).
			AddRow(1, "saikat", 0, "INR", time.Now()))

	rec := do(t, mux, http.MethodPost, "/accounts", `{"owner":"saikat","currency":"INR"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /accounts: got %d, want 201; body: %s", rec.Code, rec.Body)
	}
	var got struct {
		ID    int64 `json:"id"`
		Owner string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if got.ID != 1 {
		t.Errorf("created account id = %d, want 1", got.ID)
	}
}

func TestGetAccountBadID(t *testing.T) {
	mux, _ := testMux(t)
	rec := do(t, mux, http.MethodGet, "/accounts/not-a-number", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /accounts/not-a-number: got %d, want 400", rec.Code)
	}
}

func TestGetAccountNotFound(t *testing.T) {
	mux, mock := testMux(t)
	mock.ExpectQuery(`FROM accounts WHERE id = \$1`).
		WithArgs(int64(42)).
		WillReturnError(sql.ErrNoRows)

	rec := do(t, mux, http.MethodGet, "/accounts/42", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /accounts/42: got %d, want 404", rec.Code)
	}
}

func TestCreateTransferValidation(t *testing.T) {
	mux, _ := testMux(t)
	cases := []struct {
		name, body string
	}{
		{"invalid JSON", `{oops`},
		{"zero amount", `{"from_account_id":1,"to_account_id":2,"amount":0}`},
		{"negative amount", `{"from_account_id":1,"to_account_id":2,"amount":-5}`},
		{"same account", `{"from_account_id":1,"to_account_id":1,"amount":10}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, mux, http.MethodPost, "/transfers", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("POST /transfers %s: got %d, want 400", tc.name, rec.Code)
			}
		})
	}
}

func TestCreateTransferAcceptedForBackgroundSettlement(t *testing.T) {
	mux, mock := testMux(t)
	mock.ExpectQuery(`INSERT INTO transfers`).
		WithArgs(int64(1), int64(2), int64(50)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "from_account_id", "to_account_id", "amount", "status", "error", "created_at", "processed_at"}).
			AddRow(7, 1, 2, 50, "pending", nil, time.Now(), nil))

	rec := do(t, mux, http.MethodPost, "/transfers",
		`{"from_account_id":1,"to_account_id":2,"amount":50}`)
	// 202, not 200: the transfer is persisted as pending and settled by the
	// background worker, so the request path never waits on settlement.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /transfers: got %d, want 202; body: %s", rec.Code, rec.Body)
	}
	var got struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("transfer status = %q, want pending", got.Status)
	}
}

func TestGetTransferBadID(t *testing.T) {
	mux, _ := testMux(t)
	rec := do(t, mux, http.MethodGet, "/transfers/abc", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /transfers/abc: got %d, want 400", rec.Code)
	}
}
