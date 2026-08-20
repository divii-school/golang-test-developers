package storage

import (
	"bank-api/internal/transfer"
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type TransferStore struct {
	DB *sql.DB
}

func (s *TransferStore) Create(ctx context.Context, from, to, amount int64) (transfer.Transfer, error) {
	var t transfer.Transfer
	var errMsg sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`INSERT INTO transfers (from_account_id, to_account_id, amount)
			VALUES ($1, $2, $3)
			RETURNING id, from_account_id, to_account_id, amount, status, error, created_at, processed_at`,
		from, to, amount,
	).Scan(&t.ID, &t.FromAccountID, &t.ToAccountID, &t.Amount, &t.Status, &errMsg, &t.CreatedAt, &t.ProcessedAt)
	t.Error = errMsg.String
	return t, err
}

func (s *TransferStore) GetByID(ctx context.Context, id int64) (transfer.Transfer, error) {
	var t transfer.Transfer
	var errMsg sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, from_account_id, to_account_id, amount, status, error, created_at, processed_at
			FROM transfers WHERE id = $1`,
		id,
	).Scan(&t.ID, &t.FromAccountID, &t.ToAccountID, &t.Amount, &t.Status, &errMsg, &t.CreatedAt, &t.ProcessedAt)
	t.Error = errMsg.String
	return t, err
}

// ListPendingIDs is used at startup to requeue transfers that were accepted
// but not yet processed when the previous process stopped.
func (s *TransferStore) ListPendingIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id FROM transfers WHERE status = 'pending' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Execute settles a pending transfer in a single database transaction:
// it locks both accounts, moves the balance, writes ledger entries, and
// marks the transfer completed. Insufficient funds marks it failed instead;
// that is a settled outcome, not an error.
func (s *TransferStore) Execute(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var from, to, amount int64
	err = tx.QueryRowContext(ctx,
		`SELECT from_account_id, to_account_id, amount
			FROM transfers WHERE id = $1 AND status = 'pending'
			FOR UPDATE`,
		id,
	).Scan(&from, &to, &amount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // already processed (e.g. requeued twice); nothing to do
	}
	if err != nil {
		return fmt.Errorf("load transfer %d: %w", id, err)
	}

	// Lock both accounts in id order so two concurrent transfers between the
	// same pair can never deadlock.
	first, second := from, to
	if second < first {
		first, second = second, first
	}
	balances := make(map[int64]int64, 2)
	for _, accID := range []int64{first, second} {
		var bal int64
		if err := tx.QueryRowContext(ctx,
			`SELECT balance FROM accounts WHERE id = $1 FOR UPDATE`, accID,
		).Scan(&bal); err != nil {
			return fmt.Errorf("lock account %d: %w", accID, err)
		}
		balances[accID] = bal
	}

	if balances[from] < amount {
		if _, err := tx.ExecContext(ctx,
			`UPDATE transfers SET status = 'failed', error = 'insufficient funds', processed_at = now()
				WHERE id = $1`, id); err != nil {
			return fmt.Errorf("mark transfer %d failed: %w", id, err)
		}
		return tx.Commit()
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, from); err != nil {
		return fmt.Errorf("debit account %d: %w", from, err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, to); err != nil {
		return fmt.Errorf("credit account %d: %w", to, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO entries (account_id, amount) VALUES ($1, $2), ($3, $4)`,
		from, -amount, to, amount); err != nil {
		return fmt.Errorf("write ledger entries: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE transfers SET status = 'completed', processed_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("mark transfer %d completed: %w", id, err)
	}

	return tx.Commit()
}
