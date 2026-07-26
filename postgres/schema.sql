CREATE TABLE IF NOT EXISTS feature_flag_tenant_state (
    tenant TEXT PRIMARY KEY,
    revision BIGINT NOT NULL CHECK (revision > 0),
    document BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
