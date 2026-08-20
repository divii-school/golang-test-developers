ALTER TABLE transfers
    ADD COLUMN status        TEXT        NOT NULL DEFAULT 'pending',
    ADD COLUMN error         TEXT,
    ADD COLUMN processed_at  TIMESTAMPTZ;

CREATE INDEX idx_transfers_status ON transfers (status);
