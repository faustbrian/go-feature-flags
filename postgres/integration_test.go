//go:build integration

package postgres_test

import (
	"os"
	"testing"

	featureflags "github.com/faustbrian/golib/pkg/feature-flags"
	"github.com/faustbrian/golib/pkg/feature-flags/featureflagstest"
	featurepostgres "github.com/faustbrian/golib/pkg/feature-flags/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProviderConformance(t *testing.T) {
	dsn := os.Getenv("FEATURE_FLAGS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("FEATURE_FLAGS_POSTGRES_DSN is not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	t.Cleanup(pool.Close)
	backend := featurepostgres.NewBackend(pool)
	if err := backend.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	featureflagstest.RunProvider(t, func(t *testing.T) featureflags.Provider {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "TRUNCATE feature_flag_tenant_state"); err != nil {
			t.Fatalf("truncate provider state: %v", err)
		}
		return featurepostgres.New(pool, featureflags.DefaultLimits())
	})
}
