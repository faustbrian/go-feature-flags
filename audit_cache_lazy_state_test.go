package featureflags

import (
	"context"
	"testing"
	"time"
)

func TestCacheLazyRefreshTrackingPreservesMutationFence(t *testing.T) {
	template, underlying, definition := auditPublicationCache(t)
	// Exercise the owning cache's lazy tracking state, not an unsupported public
	// zero value: the provider, bounds, clock and retained-entry owner are valid.
	cached := &CachedProvider{
		provider: underlying,
		config:   template.config,
		entries:  make(map[string]cachedSnapshot),
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, err := cached.Refresh(ctx, "tenant-a")
		done <- err
	}()
	t.Cleanup(func() {
		underlying.unblock()
		cancel()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("owned refresh did not join after release and cancellation")
		}
	})
	auditWaitSignal(t, ctx, underlying.entered)
	definition.Default = BooleanValue(true)
	if _, err := cached.Update(ctx, "tenant-a", definition, definition.Version, "test"); err != nil {
		t.Fatal(err)
	}
	underlying.unblock()
	auditWaitRefresh(t, ctx, done)
	auditAssertLatestCache(t, ctx, cached)
	reads := underlying.reads.Load()
	auditAssertLatestCache(t, ctx, cached)
	if underlying.reads.Load() != reads {
		t.Error("updated snapshot was not retained after lazy-state mutation fencing")
	}
}
