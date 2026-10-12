// Package postgres provides atomic tenant-document persistence through pgx v5.
// New retains an application-owned concurrent DB; it does not validate, open,
// migrate or close the pool. Migrate is explicit. Load allocates backend replies
// before durable state-size validation. Statement CAS is tenant-scoped, not a
// cross-tenant transaction or a guarantee about remote failover/durability.
package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	featureflags "github.com/faustbrian/go-feature-flags/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed schema.sql
var schema string

// DB is implemented by pgxpool.Pool and keeps lifecycle ownership with the
// application.
type DB interface {
	// Exec executes parameterized statements using the supplied context.
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	// QueryRow returns a row whose Scan exposes query result/failure.
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Backend stores one compare-and-swap document per tenant.
type Backend struct{ db DB }

// NewBackend retains caller-owned DB without validation, opening or migration.
func NewBackend(db DB) *Backend { return &Backend{db: db} }

// New constructs durable policy over caller-owned DB; migrate explicitly.
func New(db DB, limits featureflags.Limits) *featureflags.DurableProvider {
	return featureflags.NewDurableProvider(NewBackend(db), limits)
}

// Schema returns maintained embedded SQL without executing it.
func Schema() string { return schema }

// Migrate synchronously executes schema using ctx; migration remains application-owned.
func (backend *Backend) Migrate(ctx context.Context) error {
	if _, err := backend.db.Exec(ctx, schema); err != nil {
		return fmt.Errorf("feature flags postgres migrate: %w", err)
	}

	return nil
}

// Load returns copied document bytes/revision; no row is an explicit miss.
func (backend *Backend) Load(ctx context.Context, tenant string) ([]byte, uint64, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, false, err
	}
	var data []byte
	var revision uint64
	err := backend.db.QueryRow(ctx, `SELECT document, revision
FROM feature_flag_tenant_state WHERE tenant = $1`, tenant).Scan(&data, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("feature flags postgres load: %w", err)
	}

	return append([]byte(nil), data...), revision, true, nil
}

// CompareAndSwap inserts at revision zero or replaces a matching revision; mismatch is
// ErrStorageConflict.
func (backend *Backend) CompareAndSwap(
	ctx context.Context,
	tenant string,
	expectedRevision uint64,
	data []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var (
		tag pgconn.CommandTag
		err error
	)
	if expectedRevision == 0 {
		tag, err = backend.db.Exec(ctx, `INSERT INTO feature_flag_tenant_state
(tenant, revision, document) VALUES ($1, 1, $2)
ON CONFLICT (tenant) DO NOTHING`, tenant, data)
	} else {
		tag, err = backend.db.Exec(ctx, `UPDATE feature_flag_tenant_state
SET revision = revision + 1, document = $3, updated_at = CURRENT_TIMESTAMP
WHERE tenant = $1 AND revision = $2`, tenant, expectedRevision, data)
	}
	if err != nil {
		return fmt.Errorf("feature flags postgres compare and swap: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return featureflags.ErrStorageConflict
	}

	return nil
}

// Health checks SELECT 1 and reports a safe readiness code.
func (backend *Backend) Health(ctx context.Context) featureflags.ProviderHealth {
	if err := ctx.Err(); err != nil {
		return featureflags.ProviderHealth{Code: "context_cancelled"}
	}
	var one int
	if err := backend.db.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		return featureflags.ProviderHealth{Code: "postgres_unavailable"}
	}

	return featureflags.ProviderHealth{Healthy: true, Code: "ready"}
}

// Close does not close the application-owned DB pool.
func (*Backend) Close(ctx context.Context) error { return ctx.Err() }
