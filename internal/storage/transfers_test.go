package storage

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMockStore(t *testing.T) (*TransferStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &TransferStore{DB: db}, mock
}

func TestExecuteSettlesTransfer(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectBegin()
	mock.ExpectQuery(`FROM transfers WHERE id = \$1 AND status = 'pending'`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"from_account_id", "to_account_id", "amount"}).
			AddRow(1, 2, 50))
	mock.ExpectQuery(`SELECT balance FROM accounts WHERE id = \$1 FOR UPDATE`).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(100))
	mock.ExpectQuery(`SELECT balance FROM accounts WHERE id = \$1 FOR UPDATE`).
		WithArgs(int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(0))
	mock.ExpectExec(`UPDATE accounts SET balance = balance - \$1`).
		WithArgs(int64(50), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE accounts SET balance = balance \+ \$1`).
		WithArgs(int64(50), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO entries`).
		WithArgs(int64(1), int64(-50), int64(2), int64(50)).
		WillReturnResult(sqlmock.NewResult(1, 2))
	mock.ExpectExec(`UPDATE transfers SET status = 'completed'`).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.Execute(context.Background(), 7); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestExecuteInsufficientFundsMarksFailed(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectBegin()
	mock.ExpectQuery(`FROM transfers WHERE id = \$1 AND status = 'pending'`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"from_account_id", "to_account_id", "amount"}).
			AddRow(1, 2, 500))
	mock.ExpectQuery(`SELECT balance FROM accounts WHERE id = \$1 FOR UPDATE`).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(100))
	mock.ExpectQuery(`SELECT balance FROM accounts WHERE id = \$1 FOR UPDATE`).
		WithArgs(int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(0))
	// No balance is moved; the transfer is marked failed and that is committed.
	mock.ExpectExec(`UPDATE transfers SET status = 'failed', error = 'insufficient funds'`).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.Execute(context.Background(), 7); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestExecuteAlreadyProcessedIsNoop(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectBegin()
	mock.ExpectQuery(`FROM transfers WHERE id = \$1 AND status = 'pending'`).
		WithArgs(int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	// A transfer requeued twice (e.g. after restart) must not error or
	// double-settle: the second Execute finds no pending row and returns nil.
	if err := store.Execute(context.Background(), 7); err != nil {
		t.Fatalf("Execute on already-processed transfer: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestExecuteLocksAccountsInIDOrder(t *testing.T) {
	store, mock := newMockStore(t)

	// from=5, to=3: account 3 must be locked before account 5 so two
	// concurrent transfers between the same pair cannot deadlock.
	mock.ExpectBegin()
	mock.ExpectQuery(`FROM transfers WHERE id = \$1 AND status = 'pending'`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"from_account_id", "to_account_id", "amount"}).
			AddRow(5, 3, 10))
	mock.ExpectQuery(`SELECT balance FROM accounts WHERE id = \$1 FOR UPDATE`).
		WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(0))
	mock.ExpectQuery(`SELECT balance FROM accounts WHERE id = \$1 FOR UPDATE`).
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(100))
	mock.ExpectExec(`UPDATE accounts SET balance = balance - \$1`).
		WithArgs(int64(10), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE accounts SET balance = balance \+ \$1`).
		WithArgs(int64(10), int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO entries`).
		WithArgs(int64(5), int64(-10), int64(3), int64(10)).
		WillReturnResult(sqlmock.NewResult(1, 2))
	mock.ExpectExec(`UPDATE transfers SET status = 'completed'`).
		WithArgs(int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.Execute(context.Background(), 9); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
