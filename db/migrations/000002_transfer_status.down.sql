DROP INDEX idx_transfers_status;

ALTER TABLE transfers
    DROP COLUMN status,
    DROP COLUMN error,
    DROP COLUMN processed_at;
