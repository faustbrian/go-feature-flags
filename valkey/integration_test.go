//go:build integration

package valkey_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	featureflags "github.com/faustbrian/golib/pkg/feature-flags"
	"github.com/faustbrian/golib/pkg/feature-flags/featureflagstest"
	featurevalkey "github.com/faustbrian/golib/pkg/feature-flags/valkey"
	valkeygo "github.com/valkey-io/valkey-go"
)

func TestProviderConformance(t *testing.T) {
	address := os.Getenv("FEATURE_FLAGS_VALKEY_ADDRESS")
	if address == "" {
		t.Skip("FEATURE_FLAGS_VALKEY_ADDRESS is not set")
	}
	client, err := valkeygo.NewClient(valkeygo.ClientOption{InitAddress: []string{address}})
	if err != nil {
		t.Fatalf("valkey.NewClient() error = %v", err)
	}
	t.Cleanup(client.Close)
	transport := featurevalkey.NewNativeTransport(client)
	var sequence atomic.Uint64
	runPrefix := "feature-flags-test-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-"

	featureflagstest.RunProvider(t, func(t *testing.T) featureflags.Provider {
		t.Helper()
		prefix := runPrefix + strconv.FormatUint(sequence.Add(1), 10)
		return featurevalkey.New(
			transport,
			featurevalkey.Config{Prefix: prefix},
			featureflags.DefaultLimits(),
		)
	})
}

func TestNativeTransportRejectsCorruptStateAndCancellation(t *testing.T) {
	address := os.Getenv("FEATURE_FLAGS_VALKEY_ADDRESS")
	if address == "" {
		t.Skip("FEATURE_FLAGS_VALKEY_ADDRESS is not set")
	}
	client, err := valkeygo.NewClient(valkeygo.ClientOption{InitAddress: []string{address}})
	if err != nil {
		t.Fatalf("valkey.NewClient() error = %v", err)
	}
	t.Cleanup(client.Close)
	transport := featurevalkey.NewNativeTransport(client)

	for name, configure := range map[string]func(string) error{
		"revision": func(key string) error {
			return client.Do(t.Context(), client.B().Hset().Key(key).FieldValue().
				FieldValue("revision", "0").FieldValue("document", `{}`).Build()).Error()
		},
		"document": func(key string) error {
			return client.Do(t.Context(), client.B().Hset().Key(key).FieldValue().
				FieldValue("revision", "1").Build()).Error()
		},
		"wrong type": func(key string) error {
			return client.Do(t.Context(), client.B().Set().Key(key).Value("invalid").Build()).Error()
		},
	} {
		t.Run(name, func(t *testing.T) {
			key := "feature-flags-corrupt-" + name + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			t.Cleanup(func() {
				_ = client.Do(context.Background(), client.B().Del().Key(key).Build()).Error()
			})
			if err := configure(key); err != nil {
				t.Fatalf("configure corrupt state: %v", err)
			}
			if _, _, _, err := transport.Load(t.Context(), key); err == nil {
				t.Fatal("Load(corrupt state) succeeded")
			}
		})
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := transport.Load(cancelled, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(cancelled) error = %v", err)
	}
	if _, err := transport.CompareAndSwap(cancelled, "key", 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompareAndSwap(cancelled) error = %v", err)
	}
	if err := transport.Ping(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ping(cancelled) error = %v", err)
	}
	if err := transport.Ping(t.Context()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}
