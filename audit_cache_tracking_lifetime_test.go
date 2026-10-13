package featureflags

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCacheCompletedRefreshReleasesTenantTracking(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		name := "successful snapshot"
		if unavailable {
			name = "provider unavailable"
		}
		t.Run(name, func(t *testing.T) {
			cached, underlying, _ := auditPublicationCache(t)
			// Constructor-created ownership is valid; no counters or generations
			// are fabricated. No asynchronous refresh remains after this call.
			underlying.unblock()
			if unavailable {
				underlying.failAt = 1
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			snapshot, err := cached.Refresh(ctx, "tenant-a")
			if unavailable {
				if !errors.Is(err, errProviderUnavailable) {
					t.Fatalf("failed refresh lost provider outcome: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				detail, err := snapshot.Boolean("flag", Context{Tenant: "tenant-a"})
				if err != nil || detail.Value || detail.Version != 1 {
					t.Fatal("successful refresh lost the actual provider snapshot")
				}
			}
			// The public provider guide promises publication metadata only while
			// a tenant has active refreshes. Inspect that owned resource at quiescence,
			// not the specific arithmetic or private counter values.
			cached.mu.RLock()
			retained := len(cached.refreshes)
			cached.mu.RUnlock()
			if retained != 0 {
				t.Errorf("completed refresh retained metadata for %d inactive tenants", retained)
			}
		})
	}
}
