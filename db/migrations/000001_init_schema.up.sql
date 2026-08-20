CREATE TABLE accounts (
    id          BIGSERIAL   PRIMARY KEY,
    owner       TEXT        NOT NULL,
    balance     BIGINT      NOT NULL DEFAULT 0,
    currency    TEXT        NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE entries (
    id          BIGSERIAL   PRIMARY KEY,
    account_id  BIGINT      NOT NULL REFERENCES accounts (id),
    amount      BIGINT      NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()  
);

CREATE TABLE transfers (
    id                  BIGSERIAL PRIMARY KEY,
    from_account_id     BIGINT NOT NULL REFERENCES accounts (id),
    to_account_id       BIGINT NOT NULL REFERENCES accounts (id),
    amount              BIGINT NOT NULL CHECK (amount > 0),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_entries_account_id ON entries (account_id);
CREATE INDEX idx_transfers_from ON transfers (from_account_id);
CREATE INDEX idx_transfers_to ON transfers (to_account_id);