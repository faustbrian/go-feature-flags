package featureflags

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// CacheClock keeps cache freshness deterministic in tests and applications.
type CacheClock interface{ Now() time.Time }

// FailurePolicy controls whether a bounded cached snapshot may survive a
// provider outage.
type FailurePolicy uint8

const (
	FailClosed FailurePolicy = iota + 1
	FailOpen
)

type CacheConfig struct {
	Clock                CacheClock
	MaxStaleness         time.Duration
	MaxOutageStaleness   time.Duration
	FailurePolicy        FailurePolicy
	MaxTenants           int
	MaxFeaturesPerTenant int
}

type cachedSnapshot struct {
	snapshot Snapshot
	fetched  time.Time
}

// CachedProvider adds explicit bounded-stale snapshots without a hidden
// refresher. Refresh ownership remains with the caller.
type CachedProvider struct {
	provider Provider
	config   CacheConfig
	mu       sync.RWMutex
	entries  map[string]cachedSnapshot
}

func NewCachedProvider(provider Provider, config CacheConfig) (*CachedProvider, error) {
	if provider == nil || config.Clock == nil {
		return nil, fmt.Errorf("cache provider and clock are required")
	}
	if config.MaxStaleness <= 0 || config.MaxOutageStaleness < config.MaxStaleness {
		return nil, fmt.Errorf("cache staleness bounds are invalid")
	}
	if config.FailurePolicy != FailOpen && config.FailurePolicy != FailClosed {
		return nil, fmt.Errorf("cache failure policy is invalid")
	}
	if config.MaxTenants <= 0 {
		return nil, fmt.Errorf("cache tenant bound must be positive")
	}
	if config.MaxFeaturesPerTenant <= 0 {
		config.MaxFeaturesPerTenant = DefaultLimits().MaxFeatures
	}

	return &CachedProvider{
		provider: provider,
		config:   config,
		entries:  make(map[string]cachedSnapshot),
	}, nil
}

func (provider *CachedProvider) Capabilities() Capabilities {
	return provider.provider.Capabilities()
}

func (provider *CachedProvider) Health(ctx context.Context) ProviderHealth {
	return provider.provider.Health(ctx)
}

func (provider *CachedProvider) Close(ctx context.Context) error {
	provider.mu.Lock()
	provider.entries = make(map[string]cachedSnapshot)
	provider.mu.Unlock()

	return provider.provider.Close(ctx)
}

func (provider *CachedProvider) Snapshot(ctx context.Context, tenant string) (Snapshot, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return Snapshot{}, err
	}
	now := provider.config.Clock.Now()
	provider.mu.RLock()
	entry, exists := provider.entries[tenant]
	provider.mu.RUnlock()
	if exists && boundedAge(now, entry.fetched) <= provider.config.MaxStaleness {
		return entry.snapshot, nil
	}
	snapshot, err := provider.Refresh(ctx, tenant)
	if err == nil {
		return snapshot, nil
	}
	if provider.config.FailurePolicy == FailOpen && exists &&
		boundedAge(now, entry.fetched) <= provider.config.MaxOutageStaleness {
		return entry.snapshot, nil
	}

	return Snapshot{}, err
}

// Refresh synchronously replaces one cached tenant snapshot.
func (provider *CachedProvider) Refresh(ctx context.Context, tenant string) (Snapshot, error) {
	snapshot, err := provider.provider.Snapshot(ctx, tenant)
	if err != nil {
		return Snapshot{}, err
	}
	if len(snapshot.definitions) > provider.config.MaxFeaturesPerTenant {
		return Snapshot{}, fmt.Errorf("cached feature count exceeds %d", provider.config.MaxFeaturesPerTenant)
	}
	entry := cachedSnapshot{snapshot: snapshot, fetched: provider.config.Clock.Now()}
	provider.mu.Lock()
	if _, exists := provider.entries[tenant]; !exists && len(provider.entries) >= provider.config.MaxTenants {
		provider.evictOldestLocked()
	}
	provider.entries[tenant] = entry
	provider.mu.Unlock()

	return snapshot, nil
}

func (provider *CachedProvider) Create(ctx context.Context, tenant string, definition Definition, actor string) (Definition, error) {
	result, err := provider.provider.Create(ctx, tenant, definition, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

func (provider *CachedProvider) Update(ctx context.Context, tenant string, definition Definition, expected uint64, actor string) (Definition, error) {
	result, err := provider.provider.Update(ctx, tenant, definition, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

func (provider *CachedProvider) Activate(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	result, err := provider.provider.Activate(ctx, tenant, key, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

func (provider *CachedProvider) Deactivate(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	result, err := provider.provider.Deactivate(ctx, tenant, key, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

func (provider *CachedProvider) Delete(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	result, err := provider.provider.Delete(ctx, tenant, key, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

func (provider *CachedProvider) Restore(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	result, err := provider.provider.Restore(ctx, tenant, key, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

func (provider *CachedProvider) CreateGroup(ctx context.Context, tenant string, group GroupDefinition, actor string) (GroupDefinition, error) {
	result, err := provider.provider.CreateGroup(ctx, tenant, group, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

func (provider *CachedProvider) UpdateGroup(ctx context.Context, tenant string, group GroupDefinition, expected uint64, actor string) (GroupDefinition, error) {
	result, err := provider.provider.UpdateGroup(ctx, tenant, group, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

func (provider *CachedProvider) DeleteGroup(ctx context.Context, tenant, groupKey string, expected uint64, actor string) (GroupDefinition, error) {
	result, err := provider.provider.DeleteGroup(ctx, tenant, groupKey, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

func (provider *CachedProvider) AssignGroup(ctx context.Context, tenant, featureKey, groupKey string, expected uint64, actor string) (Definition, error) {
	result, err := provider.provider.AssignGroup(ctx, tenant, featureKey, groupKey, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

func (provider *CachedProvider) RemoveGroup(ctx context.Context, tenant, featureKey, groupKey string, expected uint64, actor string) (Definition, error) {
	result, err := provider.provider.RemoveGroup(ctx, tenant, featureKey, groupKey, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

func (provider *CachedProvider) Audit(ctx context.Context, tenant, key string) ([]AuditEntry, error) {
	return provider.provider.Audit(ctx, tenant, key)
}

func (provider *CachedProvider) ExportDocument(ctx context.Context, tenant string) ([]byte, error) {
	return provider.provider.ExportDocument(ctx, tenant)
}

func (provider *CachedProvider) ImportDocument(ctx context.Context, tenant string, data []byte, options ImportOptions, actor string) (ImportReport, error) {
	report, err := provider.provider.ImportDocument(ctx, tenant, data, options, actor)
	if !options.DryRun {
		provider.invalidateOnSuccess(tenant, err)
	}
	return report, err
}

func (provider *CachedProvider) StageUpdate(
	ctx context.Context,
	tenant string,
	definition Definition,
	expected uint64,
	applyAt time.Time,
	actor string,
) (StagedChange, error) {
	return provider.provider.StageUpdate(ctx, tenant, definition, expected, applyAt, actor)
}

func (provider *CachedProvider) ApplyStage(
	ctx context.Context,
	tenant string,
	id uint64,
	actor string,
) (Definition, error) {
	result, err := provider.provider.ApplyStage(ctx, tenant, id, actor)
	provider.invalidateOnSuccess(tenant, err)

	return result, err
}

func (provider *CachedProvider) ApplyScheduled(
	ctx context.Context,
	tenant string,
	now time.Time,
	actor string,
) ([]Definition, error) {
	result, err := provider.provider.ApplyScheduled(ctx, tenant, now, actor)
	if err == nil && len(result) > 0 {
		provider.invalidateOnSuccess(tenant, nil)
	}

	return result, err
}

func (provider *CachedProvider) StagedChanges(ctx context.Context, tenant string) ([]StagedChange, error) {
	return provider.provider.StagedChanges(ctx, tenant)
}

func (provider *CachedProvider) Cleanup(
	ctx context.Context,
	tenant string,
	options CleanupOptions,
) (CleanupReport, error) {
	report, err := provider.provider.Cleanup(ctx, tenant, options)
	if err == nil && report.DeletedFeatures > 0 {
		provider.invalidateOnSuccess(tenant, nil)
	}

	return report, err
}

func (provider *CachedProvider) invalidateOnSuccess(tenant string, err error) {
	if err != nil {
		return
	}
	provider.mu.Lock()
	delete(provider.entries, tenant)
	provider.mu.Unlock()
}

func (provider *CachedProvider) evictOldestLocked() {
	var oldestTenant string
	var oldestTime time.Time
	for tenant, entry := range provider.entries {
		if oldestTenant == "" || entry.fetched.Before(oldestTime) ||
			(entry.fetched.Equal(oldestTime) && tenant < oldestTenant) {
			oldestTenant = tenant
			oldestTime = entry.fetched
		}
	}
	delete(provider.entries, oldestTenant)
}

func boundedAge(now, then time.Time) time.Duration {
	if now.Before(then) {
		return 0
	}

	return now.Sub(then)
}
