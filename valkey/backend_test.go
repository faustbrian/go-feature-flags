package valkey

import (
	"context"
	"errors"
	"testing"

	featureflags "github.com/faustbrian/golib/pkg/feature-flags"
	valkeygo "github.com/valkey-io/valkey-go"
)

type conflictTransport struct{}

func (conflictTransport) Load(context.Context, string) ([]byte, uint64, bool, error) {
	return nil, 0, false, nil
}

type stubNativeExecutor struct {
	array    []valkeygo.ValkeyMessage
	arrayErr error
	integer  int64
	intErr   error
	pingErr  error
}

func (executor stubNativeExecutor) evalArray(
	context.Context, string, string, ...string,
) ([]valkeygo.ValkeyMessage, error) {
	return executor.array, executor.arrayErr
}

func (executor stubNativeExecutor) evalInt(
	context.Context, string, string, ...string,
) (int64, error) {
	return executor.integer, executor.intErr
}

func (executor stubNativeExecutor) ping(context.Context) error { return executor.pingErr }

func TestNativeTransportRejectsMalformedArityAndExecutorErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("transport failure")
	for name, executor := range map[string]stubNativeExecutor{
		"arity": {array: make([]valkeygo.ValkeyMessage, 1)},
		"load":  {arrayErr: boom},
	} {
		t.Run(name, func(t *testing.T) {
			transport := &NativeTransport{executor: executor}
			if _, _, _, err := transport.Load(t.Context(), "key"); err == nil {
				t.Fatal("Load() succeeded")
			}
		})
	}
	transport := &NativeTransport{executor: stubNativeExecutor{intErr: boom, pingErr: boom}}
	if _, err := transport.CompareAndSwap(t.Context(), "key", 0, nil); !errors.Is(err, boom) {
		t.Fatalf("CompareAndSwap() error = %v", err)
	}
	if err := transport.Ping(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("Ping() error = %v", err)
	}
}

type stubTransport struct {
	data       []byte
	revision   uint64
	exists     bool
	loadErr    error
	swapped    bool
	swapErr    error
	pingErr    error
	loadedKey  string
	swappedKey string
}

func (transport *stubTransport) Load(_ context.Context, key string) ([]byte, uint64, bool, error) {
	transport.loadedKey = key
	return transport.data, transport.revision, transport.exists, transport.loadErr
}

func (transport *stubTransport) CompareAndSwap(
	_ context.Context,
	key string,
	_ uint64,
	_ []byte,
) (bool, error) {
	transport.swappedKey = key
	return transport.swapped, transport.swapErr
}

func (transport *stubTransport) Ping(context.Context) error { return transport.pingErr }

func TestBackendCoversLifecycleAndFailureMapping(t *testing.T) {
	t.Parallel()

	transport := &stubTransport{
		data: []byte(`{"ok":true}`), revision: 4, exists: true, swapped: true,
	}
	backend := NewBackend(transport, Config{})
	data, revision, exists, err := backend.Load(t.Context(), "tenant-a")
	if err != nil || !exists || revision != 4 || string(data) != `{"ok":true}` {
		t.Fatalf("Load() = (%s, %d, %t, %v)", data, revision, exists, err)
	}
	if err := backend.CompareAndSwap(t.Context(), "tenant-a", 4, []byte(`{}`)); err != nil {
		t.Fatalf("CompareAndSwap() error = %v", err)
	}
	if transport.loadedKey == "tenant-a" || transport.loadedKey != transport.swappedKey {
		t.Fatalf("hashed keys = (%q, %q)", transport.loadedKey, transport.swappedKey)
	}
	if health := backend.Health(t.Context()); !health.Healthy {
		t.Fatalf("Health() = %#v", health)
	}
	if err := backend.Close(t.Context()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if New(transport, Config{Prefix: "custom"}, featureflags.DefaultLimits()) == nil {
		t.Fatal("New() returned nil")
	}

	boom := errors.New("valkey unavailable")
	transport.loadErr = boom
	transport.swapErr = boom
	transport.pingErr = boom
	if _, _, _, err := backend.Load(t.Context(), "tenant-a"); !errors.Is(err, boom) {
		t.Fatalf("Load(failure) error = %v", err)
	}
	if err := backend.CompareAndSwap(t.Context(), "tenant-a", 4, nil); !errors.Is(err, boom) {
		t.Fatalf("CompareAndSwap(failure) error = %v", err)
	}
	if health := backend.Health(t.Context()); health.Healthy || health.Code != "valkey_unavailable" {
		t.Fatalf("Health(failure) = %#v", health)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := backend.Load(cancelled, "tenant-a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(cancelled) error = %v", err)
	}
	if err := backend.CompareAndSwap(cancelled, "tenant-a", 4, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompareAndSwap(cancelled) error = %v", err)
	}
	if health := backend.Health(cancelled); health.Code != "context_cancelled" {
		t.Fatalf("Health(cancelled) = %#v", health)
	}
	if err := backend.Close(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close(cancelled) error = %v", err)
	}
}

func (conflictTransport) CompareAndSwap(context.Context, string, uint64, []byte) (bool, error) {
	return false, nil
}

func (conflictTransport) Ping(context.Context) error { return nil }

func TestBackendMapsFailedCompareAndSwapToStorageConflict(t *testing.T) {
	t.Parallel()

	backend := NewBackend(conflictTransport{}, Config{Prefix: "test"})
	err := backend.CompareAndSwap(context.Background(), "tenant-a", 2, []byte(`{}`))
	if !errors.Is(err, featureflags.ErrStorageConflict) {
		t.Fatalf("CompareAndSwap() error = %v, want ErrStorageConflict", err)
	}
}
