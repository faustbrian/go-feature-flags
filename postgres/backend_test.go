package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	featureflags "github.com/faustbrian/golib/pkg/feature-flags"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type conflictDB struct{}

func (conflictDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 0"), nil
}

type stubRow struct {
	values []any
	err    error
}

func (row stubRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	for index, value := range row.values {
		switch destination := destinations[index].(type) {
		case *[]byte:
			*destination = append([]byte(nil), value.([]byte)...)
		case *uint64:
			*destination = value.(uint64)
		case *int:
			*destination = value.(int)
		}
	}
	return nil
}

type stubDB struct {
	execTag pgconn.CommandTag
	execErr error
	row     pgx.Row
}

func (db stubDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return db.execTag, db.execErr
}

func (db stubDB) QueryRow(context.Context, string, ...any) pgx.Row { return db.row }

func TestBackendCoversLifecycleAndFailureMapping(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ready := NewBackend(stubDB{
		execTag: pgconn.NewCommandTag("INSERT 0 1"),
		row:     stubRow{values: []any{1}},
	})
	if err := ready.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := ready.CompareAndSwap(ctx, "tenant", 0, []byte(`{}`)); err != nil {
		t.Fatalf("CompareAndSwap(insert) error = %v", err)
	}
	if health := ready.Health(ctx); !health.Healthy {
		t.Fatalf("Health() = %#v", health)
	}
	if err := ready.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if New(stubDB{}, featureflags.DefaultLimits()) == nil {
		t.Fatal("New() returned nil")
	}

	loaded := NewBackend(stubDB{row: stubRow{values: []any{[]byte(`{"ok":true}`), uint64(7)}}})
	data, revision, exists, err := loaded.Load(ctx, "tenant")
	if err != nil || !exists || revision != 7 || string(data) != `{"ok":true}` {
		t.Fatalf("Load() = (%s, %d, %t, %v)", data, revision, exists, err)
	}
	missing := NewBackend(stubDB{row: stubRow{err: pgx.ErrNoRows}})
	if _, _, exists, err := missing.Load(ctx, "tenant"); err != nil || exists {
		t.Fatalf("Load(missing) = (%t, %v)", exists, err)
	}

	boom := errors.New("database unavailable")
	failing := NewBackend(stubDB{execErr: boom, row: stubRow{err: boom}})
	if err := failing.Migrate(ctx); !errors.Is(err, boom) {
		t.Fatalf("Migrate(failure) error = %v", err)
	}
	if _, _, _, err := failing.Load(ctx, "tenant"); !errors.Is(err, boom) {
		t.Fatalf("Load(failure) error = %v", err)
	}
	if err := failing.CompareAndSwap(ctx, "tenant", 2, nil); !errors.Is(err, boom) {
		t.Fatalf("CompareAndSwap(failure) error = %v", err)
	}
	if health := failing.Health(ctx); health.Healthy || health.Code != "postgres_unavailable" {
		t.Fatalf("Health(failure) = %#v", health)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, err := ready.Load(cancelled, "tenant"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(cancelled) error = %v", err)
	}
	if err := ready.CompareAndSwap(cancelled, "tenant", 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompareAndSwap(cancelled) error = %v", err)
	}
	if health := ready.Health(cancelled); health.Code != "context_cancelled" {
		t.Fatalf("Health(cancelled) = %#v", health)
	}
	if err := ready.Close(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close(cancelled) error = %v", err)
	}
}

func (conflictDB) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func TestBackendMapsFailedCompareAndSwapToStorageConflict(t *testing.T) {
	t.Parallel()

	err := NewBackend(conflictDB{}).CompareAndSwap(context.Background(), "tenant-a", 4, []byte(`{}`))
	if !errors.Is(err, featureflags.ErrStorageConflict) {
		t.Fatalf("CompareAndSwap() error = %v, want ErrStorageConflict", err)
	}
	if !strings.Contains(Schema(), "feature_flag_tenant_state") {
		t.Fatal("Schema() does not declare tenant state table")
	}
}
