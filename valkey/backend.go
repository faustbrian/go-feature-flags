// Package valkey provides atomic Valkey persistence for feature flags.
package valkey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	featureflags "github.com/faustbrian/golib/pkg/feature-flags"
)

// Transport is the small atomic Valkey contract used by the provider.
type Transport interface {
	Load(context.Context, string) ([]byte, uint64, bool, error)
	CompareAndSwap(context.Context, string, uint64, []byte) (bool, error)
	Ping(context.Context) error
}

type Config struct {
	Prefix string
}

// Backend stores one compare-and-swap hash per tenant.
type Backend struct {
	transport Transport
	prefix    string
}

func NewBackend(transport Transport, config Config) *Backend {
	if config.Prefix == "" {
		config.Prefix = "go-feature-flags"
	}

	return &Backend{transport: transport, prefix: config.Prefix}
}

func New(transport Transport, config Config, limits featureflags.Limits) *featureflags.DurableProvider {
	return featureflags.NewDurableProvider(NewBackend(transport, config), limits)
}

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

func (backend *Backend) Health(ctx context.Context) featureflags.ProviderHealth {
	if err := ctx.Err(); err != nil {
		return featureflags.ProviderHealth{Code: "context_cancelled"}
	}
	if err := backend.transport.Ping(ctx); err != nil {
		return featureflags.ProviderHealth{Code: "valkey_unavailable"}
	}

	return featureflags.ProviderHealth{Healthy: true, Code: "ready"}
}

func (*Backend) Close(ctx context.Context) error { return ctx.Err() }

func (backend *Backend) key(tenant string) string {
	sum := sha256.Sum256([]byte(tenant))

	return backend.prefix + ":tenant:" + hex.EncodeToString(sum[:])
}
