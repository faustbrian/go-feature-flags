// Package valkey provides atomic tenant-document persistence through a Transport.
// New retains caller-owned transport; NativeTransport adapts valkey-go. Prefix
// is visible and tenant keys are hashed, not authenticated or encrypted. Lua
// number revision precision and native-client retry/failover behavior remain
// deployment constraints; no cross-tenant or durable-delivery guarantee is made.
package valkey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	featureflags "github.com/faustbrian/go-feature-flags/v2"
)

// Transport is the small atomic Valkey contract used by the provider.
type Transport interface {
	// Load loads copied document bytes, revision and explicit presence for a storage key.
	Load(context.Context, string) ([]byte, uint64, bool, error)
	// CompareAndSwap atomically compares revision and replaces copied bytes; false is a
	// conflict.
	CompareAndSwap(context.Context, string, uint64, []byte) (bool, error)
	// Ping checks availability using the supplied context.
	Ping(context.Context) error
}

// Config selects storage namespace, not credentials or lifecycle.
type Config struct {
	// Prefix is the visible namespace; empty selects go-feature-flags.
	Prefix string
}

// Backend stores one compare-and-swap hash per tenant.
type Backend struct {
	transport Transport
	prefix    string
}

// NewBackend retains transport without validation; empty prefix selects go-feature-flags.
func NewBackend(transport Transport, config Config) *Backend {
	if config.Prefix == "" {
		config.Prefix = "go-feature-flags"
	}

	return &Backend{transport: transport, prefix: config.Prefix}
}

// New constructs durable policy without opening or closing transport.
func New(transport Transport, config Config, limits featureflags.Limits) *featureflags.DurableProvider {
	return featureflags.NewDurableProvider(NewBackend(transport, config), limits)
}

// Load delegates a hashed tenant key; copied-byte ownership is a transport obligation.
func (backend *Backend) Load(ctx context.Context, tenant string) ([]byte, uint64, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, false, err
	}
	data, revision, exists, err := backend.transport.Load(ctx, backend.key(tenant))
	if err != nil {
		return nil, 0, false, fmt.Errorf("feature flags valkey load: %w", err)
	}

	return data, revision, exists, nil
}

// CompareAndSwap maps a false atomic transport swap to ErrStorageConflict.
func (backend *Backend) CompareAndSwap(
	ctx context.Context,
	tenant string,
	expectedRevision uint64,
	data []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	swapped, err := backend.transport.CompareAndSwap(
		ctx,
		backend.key(tenant),
		expectedRevision,
		data,
	)
	if err != nil {
		return fmt.Errorf("feature flags valkey compare and swap: %w", err)
	}
	if !swapped {
		return featureflags.ErrStorageConflict
	}

	return nil
}

// Health uses Ping and reports safe readiness codes.
func (backend *Backend) Health(ctx context.Context) featureflags.ProviderHealth {
	if err := ctx.Err(); err != nil {
		return featureflags.ProviderHealth{Code: "context_cancelled"}
	}
	if err := backend.transport.Ping(ctx); err != nil {
		return featureflags.ProviderHealth{Code: "valkey_unavailable"}
	}

	return featureflags.ProviderHealth{Healthy: true, Code: "ready"}
}

// Close returns ctx.Err without closing transport or making this backend unusable.
func (*Backend) Close(ctx context.Context) error { return ctx.Err() }

func (backend *Backend) key(tenant string) string {
	sum := sha256.Sum256([]byte(tenant))

	return backend.prefix + ":tenant:" + hex.EncodeToString(sum[:])
}
