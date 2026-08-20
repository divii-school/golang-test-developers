package storage

import (
	"bank-api/internal/account"
	"context"
	"database/sql"
)

type AccountStore struct {
	DB *sql.DB
}

func (s *AccountStore) Create(ctx context.Context, owner, currency string) (account.Account, error) {
	var a account.Account
	err := s.DB.QueryRowContext(ctx,
		`INSERT INTO accounts (owner, currency)
			VALUES ($1, $2)
			RETURNING id, owner, balance, currency, created_at`,
		owner, currency,
	).Scan(&a.ID, &a.Owner, &a.Balance, &a.Currency, &a.CreatedAt)
	return a, err
}

func (s *AccountStore) List(ctx context.Context) ([]account.Account, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, owner, balance, currency, created_at
			FROM accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accounts := []account.Account{}
	for rows.Next() {
		var a account.Account
		if err := rows.Scan(&a.ID, &a.Owner, &a.Balance, &a.Currency, &a.CreatedAt); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (s *AccountStore) GetByID(ctx context.Context, id int64) (account.Account, error) {
	var a account.Account
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, owner, balance, currency, created_at
		FROM accounts WHERE id = $1`,
		id,
	).Scan(&a.ID, &a.Owner, &a.Balance, &a.Currency, &a.CreatedAt)
	return a, err
}
