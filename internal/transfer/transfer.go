package transfer

import "time"

const (
	StatusPending   = "pending"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

type Transfer struct {
	ID            int64      `json:"id"`
	FromAccountID int64      `json:"from_account_id"`
	ToAccountID   int64      `json:"to_account_id"`
	Amount        int64      `json:"amount"`
	Status        string     `json:"status"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ProcessedAt   *time.Time `json:"processed_at,omitempty"`
}
