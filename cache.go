package featureflags

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

// CacheClock keeps cache freshness deterministic in tests and applications.
type CacheClock interface {
	// Now supplies bounded concurrent wall-clock time; rollback makes cache age zero.
	Now() time.Time
}

// FailurePolicy controls whether a bounded cached snapshot may survive a
// provider outage.
type FailurePolicy uint8

const (
	// FailClosed propagates refresh errors rather than using expired cache entries.
	FailClosed FailurePolicy = iota + 1
	// FailOpen permits cached fallback only through MaxOutageStaleness.
	FailOpen
)

// CacheConfig sets bounded retained snapshot policy, not total heap or remote consistency.
type CacheConfig struct {
	// Clock is retained and must support bounded concurrent calls.
	Clock CacheClock
	// MaxStaleness is the inclusive age for a cached hit.
	MaxStaleness time.Duration
	// MaxOutageStaleness is the inclusive fallback age under FailOpen, at least MaxStaleness.
	MaxOutageStaleness time.Duration
	// FailurePolicy selects error propagation or bounded fallback; zero is invalid.
	FailurePolicy FailurePolicy
	// MaxTenants is the positive retained tenant count, not total process memory.
	MaxTenants int
	// MaxTenantBytes bounds tenant keys before cache access or delegation.
	// Zero inherits a discoverable provider limit; custom providers without
	// that capability require an explicit positive limit.
	MaxTenantBytes int
	// MaxFeaturesPerTenant bounds snapshot definitions; non-positive selects DefaultLimits.
	MaxFeaturesPerTenant int
}

type cachedSnapshot struct {
	snapshot Snapshot
	fetched  time.Time
}

// Publication ordering exists only while a tenant has outstanding refreshes.
// Failed fetches do not fence successful earlier fetches. Mutation invalidation
// removes this state so pre-mutation reads cannot repopulate the cache.
type cacheRefreshState struct {
	running   uint64
	next      uint64
	published uint64
}

// CachedProvider adds explicit bounded-stale snapshots without a hidden
// refresher. Refresh ownership remains with the caller. Concurrent refreshes
// are not coalesced; older admitted reads cannot publish after a newer successful
// refresh or local mutation invalidation. This orders local publication, not
// remote commits. Close clears entries and delegates without closing admission.
type CachedProvider struct {
	provider  Provider
	config    CacheConfig
	mu        sync.RWMutex
	entries   map[string]cachedSnapshot
	refreshes map[string]*cacheRefreshState
}

// NewCachedProvider validates cache bounds and retains provider/clock; no refresher is
// started.
func NewCachedProvider(provider Provider, config CacheConfig) (*CachedProvider, error) {
	if nilCacheProvider(provider) || config.Clock == nil {
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
	if config.MaxTenantBytes < 0 {
		return nil, fmt.Errorf("cache tenant byte bound must be positive")
	}
	if bounded, ok := provider.(interface{ TenantByteLimit() int }); ok {
		limit := bounded.TenantByteLimit()
		if limit <= 0 {
			return nil, fmt.Errorf("provider tenant byte bound must be positive")
		}
		if config.MaxTenantBytes == 0 {
			config.MaxTenantBytes = limit
		}
		config.MaxTenantBytes = min(config.MaxTenantBytes, limit)
	} else if config.MaxTenantBytes == 0 {
		return nil, fmt.Errorf("cache tenant byte bound is required for this provider")
	}
	if config.MaxFeaturesPerTenant <= 0 {
		config.MaxFeaturesPerTenant = DefaultLimits().MaxFeatures
	}

	return &CachedProvider{
		provider:  provider,
		config:    config,
		entries:   make(map[string]cachedSnapshot),
		refreshes: make(map[string]*cacheRefreshState),
	}, nil
}

func nilCacheProvider(provider Provider) bool {
	if provider == nil {
		return true
	}
	value := reflect.ValueOf(provider)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// Capabilities reports native management capabilities.
func (provider *CachedProvider) Capabilities() Capabilities {
	return provider.provider.Capabilities()
}

// TenantByteLimit returns the effective tenant-key limit enforced by this cache.
func (provider *CachedProvider) TenantByteLimit() int { return provider.config.MaxTenantBytes }

// Health reports readiness without treating unavailability as a flag value.
func (provider *CachedProvider) Health(ctx context.Context) ProviderHealth {
	return provider.provider.Health(ctx)
}

// Close clears local entries and delegates Close; it does not prevent later
// refresh/admission.
func (provider *CachedProvider) Close(ctx context.Context) error {
	provider.mu.Lock()
	provider.entries = make(map[string]cachedSnapshot)
	provider.refreshes = make(map[string]*cacheRefreshState)
	provider.mu.Unlock()

	return provider.provider.Close(ctx)
}

// Snapshot returns an inclusive-age cached hit or refreshes; FailOpen masks errors only
// within the outage bound.
func (provider *CachedProvider) Snapshot(ctx context.Context, tenant string) (Snapshot, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
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

// Refresh synchronously fetches one tenant snapshot. An older admitted call may
// return its snapshot but cannot overwrite newer successful publication or
// mutation invalidation. Successful fetch time, not commit time, starts age.
func (provider *CachedProvider) Refresh(ctx context.Context, tenant string) (Snapshot, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return Snapshot{}, err
	}
	state, sequence := provider.beginRefresh(tenant)
	defer provider.endRefresh(tenant, state)
	snapshot, err := provider.provider.Snapshot(ctx, tenant)
	if err != nil {
		return Snapshot{}, err
	}
	if len(snapshot.definitions) > provider.config.MaxFeaturesPerTenant {
		return Snapshot{}, fmt.Errorf("cached feature count exceeds %d", provider.config.MaxFeaturesPerTenant)
	}
	entry := cachedSnapshot{snapshot: snapshot, fetched: provider.config.Clock.Now()}
	provider.mu.Lock()
	if provider.refreshes[tenant] == state && sequence > state.published {
		if _, exists := provider.entries[tenant]; !exists && len(provider.entries) >= provider.config.MaxTenants {
			provider.evictOldestLocked()
		}
		provider.entries[tenant] = entry
		state.published = sequence
	}
	provider.mu.Unlock()

	return snapshot, nil
}

func (provider *CachedProvider) beginRefresh(tenant string) (*cacheRefreshState, uint64) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.refreshes == nil {
		provider.refreshes = make(map[string]*cacheRefreshState)
	}
	state := provider.refreshes[tenant]
	// Rotate before counter overflow; old outstanding readers retain a distinct
	// state and cannot publish into the replacement generation.
	if state == nil || state.next == ^uint64(0) {
		state = &cacheRefreshState{}
		provider.refreshes[tenant] = state
	}
	state.next++
	state.running++
	return state, state.next
}

func (provider *CachedProvider) endRefresh(tenant string, state *cacheRefreshState) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	state.running--
	if state.running == 0 && provider.refreshes[tenant] == state {
		delete(provider.refreshes, tenant)
	}
}

// Create creates a copied definition at version 1; existing tombstones also conflict.
// Successful writes invalidate only this local tenant entry.
func (provider *CachedProvider) Create(ctx context.Context, tenant string, definition Definition, actor string) (Definition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return Definition{}, err
	}
	result, err := provider.provider.Create(ctx, tenant, definition, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

// Update replaces a live definition only at the expected version and increments it.
// Successful writes invalidate only this local tenant entry.
func (provider *CachedProvider) Update(ctx context.Context, tenant string, definition Definition, expected uint64, actor string) (Definition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return Definition{}, err
	}
	result, err := provider.provider.Update(ctx, tenant, definition, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

// Activate sets a live definition active at its expected version. Successful writes
// invalidate only this local tenant entry.
func (provider *CachedProvider) Activate(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return Definition{}, err
	}
	result, err := provider.provider.Activate(ctx, tenant, key, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

// Deactivate sets a live definition inactive at its expected version. Successful writes
// invalidate only this local tenant entry.
func (provider *CachedProvider) Deactivate(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return Definition{}, err
	}
	result, err := provider.provider.Deactivate(ctx, tenant, key, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

// Delete archives a live definition as a restorable tombstone at its expected version.
// Successful writes invalidate only this local tenant entry.
func (provider *CachedProvider) Delete(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return Definition{}, err
	}
	result, err := provider.provider.Delete(ctx, tenant, key, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

// Restore restores a tombstone as inactive at its expected version. Successful writes
// invalidate only this local tenant entry.
func (provider *CachedProvider) Restore(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return Definition{}, err
	}
	result, err := provider.provider.Restore(ctx, tenant, key, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

// CreateGroup creates a version 1 group after validating the combined graph. Successful
// writes invalidate only this local tenant entry.
func (provider *CachedProvider) CreateGroup(ctx context.Context, tenant string, group GroupDefinition, actor string) (GroupDefinition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return GroupDefinition{}, err
	}
	result, err := provider.provider.CreateGroup(ctx, tenant, group, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

// UpdateGroup replaces an expected-version group after validating its affected graph.
// Successful writes invalidate only this local tenant entry.
func (provider *CachedProvider) UpdateGroup(ctx context.Context, tenant string, group GroupDefinition, expected uint64, actor string) (GroupDefinition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return GroupDefinition{}, err
	}
	result, err := provider.provider.UpdateGroup(ctx, tenant, group, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

// DeleteGroup removes an expected-version group only when unreferenced. Successful writes
// invalidate only this local tenant entry.
func (provider *CachedProvider) DeleteGroup(ctx context.Context, tenant, groupKey string, expected uint64, actor string) (GroupDefinition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return GroupDefinition{}, err
	}
	result, err := provider.provider.DeleteGroup(ctx, tenant, groupKey, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

// AssignGroup appends a group to an expected-version feature; an existing assignment is
// unchanged. Successful writes invalidate only this local tenant entry.
func (provider *CachedProvider) AssignGroup(ctx context.Context, tenant, featureKey, groupKey string, expected uint64, actor string) (Definition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return Definition{}, err
	}
	result, err := provider.provider.AssignGroup(ctx, tenant, featureKey, groupKey, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

// RemoveGroup removes an existing assignment from an expected-version feature. Successful
// writes invalidate only this local tenant entry.
func (provider *CachedProvider) RemoveGroup(ctx context.Context, tenant, featureKey, groupKey string, expected uint64, actor string) (Definition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return Definition{}, err
	}
	result, err := provider.provider.RemoveGroup(ctx, tenant, featureKey, groupKey, expected, actor)
	provider.invalidateOnSuccess(tenant, err)
	return result, err
}

// Audit returns owned retained entries for the resource key, not durable audit delivery.
func (provider *CachedProvider) Audit(ctx context.Context, tenant, key string) ([]AuditEntry, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return nil, err
	}

	return provider.provider.Audit(ctx, tenant, key)
}

// ExportDocument returns owned portable bytes excluding tombstones, audit and staged
// state.
func (provider *CachedProvider) ExportDocument(ctx context.Context, tenant string) ([]byte, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return nil, err
	}

	return provider.provider.ExportDocument(ctx, tenant)
}

// ImportDocument delegates import and invalidates only successful non-dry-run changes.
func (provider *CachedProvider) ImportDocument(ctx context.Context, tenant string, data []byte, options ImportOptions, actor string) (ImportReport, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return ImportReport{}, err
	}
	report, err := provider.provider.ImportDocument(ctx, tenant, data, options, actor)
	if !options.DryRun {
		provider.invalidateOnSuccess(tenant, err)
	}
	return report, err
}

// StageUpdate copies a pending expected-version replacement without applying it.
func (provider *CachedProvider) StageUpdate(
	ctx context.Context,
	tenant string,
	definition Definition,
	expected uint64,
	applyAt time.Time,
	actor string,
) (StagedChange, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return StagedChange{}, err
	}

	return provider.provider.StageUpdate(ctx, tenant, definition, expected, applyAt, actor)
}

// ApplyStage atomically applies one pending replacement after rechecking its version.
// Successful writes invalidate only this local tenant entry.
func (provider *CachedProvider) ApplyStage(
	ctx context.Context,
	tenant string,
	id uint64,
	actor string,
) (Definition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return Definition{}, err
	}
	result, err := provider.provider.ApplyStage(ctx, tenant, id, actor)
	provider.invalidateOnSuccess(tenant, err)

	return result, err
}

// ApplyScheduled invalidates the tenant only after a successful nonempty application.
func (provider *CachedProvider) ApplyScheduled(
	ctx context.Context,
	tenant string,
	now time.Time,
	actor string,
) ([]Definition, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return nil, err
	}
	result, err := provider.provider.ApplyScheduled(ctx, tenant, now, actor)
	if err == nil && len(result) > 0 {
		provider.invalidateOnSuccess(tenant, nil)
	}

	return result, err
}

// StagedChanges returns owned pending changes in ID order.
func (provider *CachedProvider) StagedChanges(ctx context.Context, tenant string) ([]StagedChange, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return nil, err
	}

	return provider.provider.StagedChanges(ctx, tenant)
}

// Cleanup invalidates only after successful feature purging.
func (provider *CachedProvider) Cleanup(
	ctx context.Context,
	tenant string,
	options CleanupOptions,
) (CleanupReport, error) {
	if err := providerInput(ctx, tenant, provider.config.MaxTenantBytes); err != nil {
		return CleanupReport{}, err
	}
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
	delete(provider.refreshes, tenant)
	provider.mu.Unlock()
}

func (provider *CachedProvider) evictOldestLocked() {
	var oldestTime time.Time
	oldestFound := false
	for _, entry := range provider.entries {
		if !oldestFound || entry.fetched.Before(oldestTime) {
			oldestTime = entry.fetched
			oldestFound = true
		}
	}

	var oldestTenant string
	tenantFound := false
	for tenant, entry := range provider.entries {
		if entry.fetched.Equal(oldestTime) &&
			(!tenantFound || strings.Compare(tenant, oldestTenant) == -1) {
			oldestTenant = tenant
			tenantFound = true
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
