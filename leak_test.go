package featureflags

import (
	"context"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestNoGoroutineLeaks(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	provider := NewMemoryProvider(DefaultLimits())
	clock := &manualCacheClock{now: time.Now()}
	cached, err := NewCachedProvider(provider, CacheConfig{
		Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: time.Minute,
		FailurePolicy: FailClosed, MaxTenants: 1,
	})
	if err != nil {
		t.Fatalf("NewCachedProvider() error = %v", err)
	}
	if err := cached.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
