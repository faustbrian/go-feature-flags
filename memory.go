package featureflags

import (
	"context"
	"fmt"
	"sync"
)

// MemoryProvider is an in-process management provider with tenant-isolated
// state and optimistic concurrency.
type MemoryProvider struct {
	mu        sync.RWMutex
	limits    Limits
	tenants   map[string]map[string]memoryRecord
	groups    map[string]map[string]GroupDefinition
	audit     map[string][]AuditEntry
	staged    map[string]map[uint64]StagedChange
	nextStage map[string]uint64
}

type memoryRecord struct {
	definition Definition
	deleted    bool
}

func NewMemoryProvider(limits Limits) *MemoryProvider {
	return &MemoryProvider{
		limits:    limits,
		tenants:   make(map[string]map[string]memoryRecord),
		groups:    make(map[string]map[string]GroupDefinition),
		audit:     make(map[string][]AuditEntry),
		staged:    make(map[string]map[uint64]StagedChange),
		nextStage: make(map[string]uint64),
	}
}

func (*MemoryProvider) Capabilities() Capabilities {
	return Capabilities{
		OptimisticConcurrency: true,
		AtomicMutations:       true,
		Snapshots:             true,
		Audit:                 true,
		Groups:                true,
		ImportExport:          true,
	}
}

func (*MemoryProvider) Health(ctx context.Context) ProviderHealth {
	if ctx.Err() != nil {
		return ProviderHealth{Code: "context_cancelled"}
	}

	return ProviderHealth{Healthy: true, Code: "ready"}
}

func (*MemoryProvider) Close(ctx context.Context) error { return ctx.Err() }

func (p *MemoryProvider) Create(
	ctx context.Context,
	tenant string,
	definition Definition,
	actor string,
) (Definition, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return Definition{}, err
	}
	if err := definition.Validate(p.limits); err != nil {
		return Definition{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	features := p.tenants[tenant]
	if features == nil {
		features = make(map[string]memoryRecord)
		p.tenants[tenant] = features
	}
	if _, exists := features[definition.Key]; exists {
		return Definition{}, fmt.Errorf("tenant %q feature %q: %w", tenant, definition.Key, ErrAlreadyExists)
	}
	if len(features) >= p.limits.MaxFeatures {
		return Definition{}, fmt.Errorf("tenant %q: features exceed limit %d", tenant, p.limits.MaxFeatures)
	}

	definition.Version = 1
	stored := cloneDefinition(definition)
	features[definition.Key] = memoryRecord{definition: stored}
	p.appendAudit(tenant, definition.Key, AuditCreate, actor, stored.Version)

	return cloneDefinition(stored), nil
}

func (p *MemoryProvider) Update(
	ctx context.Context,
	tenant string,
	definition Definition,
	expectedVersion uint64,
	actor string,
) (Definition, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return Definition{}, err
	}
	if err := definition.Validate(p.limits); err != nil {
		return Definition{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	record, exists := p.tenants[tenant][definition.Key]
	if !exists || record.deleted {
		return Definition{}, fmt.Errorf("tenant %q feature %q: %w", tenant, definition.Key, ErrNotFound)
	}
	current := record.definition
	if current.Version != expectedVersion {
		return Definition{}, fmt.Errorf(
			"tenant %q feature %q: %w: have %d, expected %d",
			tenant,
			definition.Key,
			ErrConflict,
			current.Version,
			expectedVersion,
		)
	}

	definition.Version = current.Version + 1
	stored := cloneDefinition(definition)
	p.tenants[tenant][definition.Key] = memoryRecord{definition: stored}
	p.appendAudit(tenant, definition.Key, AuditUpdate, actor, stored.Version)

	return cloneDefinition(stored), nil
}

func (p *MemoryProvider) Snapshot(ctx context.Context, tenant string) (Snapshot, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return Snapshot{}, err
	}

	p.mu.RLock()
	definitions := make([]Definition, 0, len(p.tenants[tenant]))
	for _, record := range p.tenants[tenant] {
		if !record.deleted {
			definitions = append(definitions, cloneDefinition(record.definition))
		}
	}
	groups := make([]GroupDefinition, 0, len(p.groups[tenant]))
	for _, group := range p.groups[tenant] {
		groups = append(groups, cloneGroup(group))
	}
	p.mu.RUnlock()
	snapshot, err := NewSnapshotWithGroups(definitions, groups, p.limits)
	if err != nil {
		return Snapshot{}, err
	}

	return snapshot.bindTenant(tenant), nil
}

func (p *MemoryProvider) CreateGroup(
	ctx context.Context,
	tenant string,
	group GroupDefinition,
	actor string,
) (GroupDefinition, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return GroupDefinition{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	groups := p.groups[tenant]
	if groups == nil {
		groups = make(map[string]GroupDefinition)
		p.groups[tenant] = groups
	}
	if _, exists := groups[group.Key]; exists {
		return GroupDefinition{}, fmt.Errorf("tenant %q group %q: %w", tenant, group.Key, ErrAlreadyExists)
	}
	group.Version = 1
	prospective := make([]GroupDefinition, 0, len(groups)+1)
	for _, current := range groups {
		prospective = append(prospective, current)
	}
	prospective = append(prospective, group)
	cloned, err := cloneAndValidateGroups(p.activeDefinitionsLocked(tenant), prospective, p.limits)
	if err != nil {
		return GroupDefinition{}, err
	}
	stored := cloned[group.Key]
	groups[group.Key] = stored
	p.appendAudit(tenant, group.Key, AuditGroupCreate, actor, stored.Version)

	return cloneGroup(stored), nil
}

func (p *MemoryProvider) UpdateGroup(
	ctx context.Context,
	tenant string,
	group GroupDefinition,
	expectedVersion uint64,
	actor string,
) (GroupDefinition, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return GroupDefinition{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	current, exists := p.groups[tenant][group.Key]
	if !exists {
		return GroupDefinition{}, fmt.Errorf("tenant %q group %q: %w", tenant, group.Key, ErrNotFound)
	}
	if current.Version != expectedVersion {
		return GroupDefinition{}, versionConflict(tenant, group.Key, current.Version, expectedVersion)
	}
	group.Version = current.Version + 1
	prospective := p.groupDefinitionsLocked(tenant)
	for index := range prospective {
		if prospective[index].Key == group.Key {
			prospective[index] = group
		}
	}
	cloned, err := cloneAndValidateGroups(p.activeDefinitionsLocked(tenant), prospective, p.limits)
	if err != nil {
		return GroupDefinition{}, err
	}
	stored := cloned[group.Key]
	p.groups[tenant][group.Key] = stored
	p.appendAudit(tenant, group.Key, AuditGroupUpdate, actor, stored.Version)

	return cloneGroup(stored), nil
}

func (p *MemoryProvider) DeleteGroup(
	ctx context.Context,
	tenant, groupKey string,
	expectedVersion uint64,
	actor string,
) (GroupDefinition, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return GroupDefinition{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	group, exists := p.groups[tenant][groupKey]
	if !exists {
		return GroupDefinition{}, fmt.Errorf("tenant %q group %q: %w", tenant, groupKey, ErrNotFound)
	}
	if group.Version != expectedVersion {
		return GroupDefinition{}, versionConflict(tenant, groupKey, group.Version, expectedVersion)
	}
	for _, record := range p.tenants[tenant] {
		if !record.deleted && listed(record.definition.Groups, groupKey) {
			return GroupDefinition{}, fmt.Errorf("tenant %q group %q: %w", tenant, groupKey, ErrGroupInUse)
		}
	}
	for _, candidate := range p.groups[tenant] {
		if candidate.Parent == groupKey {
			return GroupDefinition{}, fmt.Errorf("tenant %q group %q: %w", tenant, groupKey, ErrGroupInUse)
		}
	}
	delete(p.groups[tenant], groupKey)
	group.Version++
	p.appendAudit(tenant, groupKey, AuditGroupDelete, actor, group.Version)

	return cloneGroup(group), nil
}

func (p *MemoryProvider) AssignGroup(
	ctx context.Context,
	tenant, featureKey, groupKey string,
	expectedVersion uint64,
	actor string,
) (Definition, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return Definition{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.groups[tenant][groupKey]; !exists {
		return Definition{}, fmt.Errorf("tenant %q group %q: %w", tenant, groupKey, ErrNotFound)
	}
	record, exists := p.tenants[tenant][featureKey]
	if !exists || record.deleted {
		return Definition{}, fmt.Errorf("tenant %q feature %q: %w", tenant, featureKey, ErrNotFound)
	}
	if record.definition.Version != expectedVersion {
		return Definition{}, versionConflict(tenant, featureKey, record.definition.Version, expectedVersion)
	}
	if listed(record.definition.Groups, groupKey) {
		return cloneDefinition(record.definition), nil
	}
	updated := cloneDefinition(record.definition)
	updated.Groups = append(updated.Groups, groupKey)
	definitions := p.activeDefinitionsLocked(tenant)
	definitions[featureKey] = updated
	if _, err := cloneAndValidateGroups(definitions, p.groupDefinitionsLocked(tenant), p.limits); err != nil {
		return Definition{}, err
	}
	updated.Version++
	record.definition = updated
	p.tenants[tenant][featureKey] = record
	p.appendAudit(tenant, featureKey, AuditAssignGroup, actor, updated.Version)

	return cloneDefinition(updated), nil
}

func (p *MemoryProvider) RemoveGroup(
	ctx context.Context,
	tenant, featureKey, groupKey string,
	expectedVersion uint64,
	actor string,
) (Definition, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return Definition{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	record, exists := p.tenants[tenant][featureKey]
	if !exists || record.deleted {
		return Definition{}, fmt.Errorf("tenant %q feature %q: %w", tenant, featureKey, ErrNotFound)
	}
	if record.definition.Version != expectedVersion {
		return Definition{}, versionConflict(tenant, featureKey, record.definition.Version, expectedVersion)
	}
	updated := cloneDefinition(record.definition)
	found := false
	groups := updated.Groups[:0]
	for _, candidate := range updated.Groups {
		if candidate == groupKey {
			found = true
			continue
		}
		groups = append(groups, candidate)
	}
	if !found {
		return Definition{}, fmt.Errorf("tenant %q feature %q group %q: %w", tenant, featureKey, groupKey, ErrNotFound)
	}
	updated.Groups = groups
	updated.Version++
	record.definition = updated
	p.tenants[tenant][featureKey] = record
	p.appendAudit(tenant, featureKey, AuditRemoveGroup, actor, updated.Version)

	return cloneDefinition(updated), nil
}

func (p *MemoryProvider) activeDefinitionsLocked(tenant string) map[string]Definition {
	definitions := make(map[string]Definition)
	for key, record := range p.tenants[tenant] {
		if !record.deleted {
			definitions[key] = cloneDefinition(record.definition)
		}
	}

	return definitions
}

func (p *MemoryProvider) groupDefinitionsLocked(tenant string) []GroupDefinition {
	groups := make([]GroupDefinition, 0, len(p.groups[tenant]))
	for _, group := range p.groups[tenant] {
		groups = append(groups, cloneGroup(group))
	}

	return groups
}

func cloneGroup(group GroupDefinition) GroupDefinition {
	group.Metadata = cloneStringMap(group.Metadata)
	group.Tags = append([]string(nil), group.Tags...)
	strategies := make([]Strategy, len(group.Strategies))
	for index, strategy := range group.Strategies {
		strategies[index] = strategy.SnapshotStrategy()
	}
	group.Strategies = strategies

	return group
}

func (p *MemoryProvider) Activate(
	ctx context.Context,
	tenant, key string,
	expectedVersion uint64,
	actor string,
) (Definition, error) {
	return p.setLifecycle(ctx, tenant, key, expectedVersion, actor, LifecycleActive, AuditActivate, false)
}

func (p *MemoryProvider) Deactivate(
	ctx context.Context,
	tenant, key string,
	expectedVersion uint64,
	actor string,
) (Definition, error) {
	return p.setLifecycle(ctx, tenant, key, expectedVersion, actor, LifecycleInactive, AuditDeactivate, false)
}

func (p *MemoryProvider) Delete(
	ctx context.Context,
	tenant, key string,
	expectedVersion uint64,
	actor string,
) (Definition, error) {
	return p.setLifecycle(ctx, tenant, key, expectedVersion, actor, LifecycleArchived, AuditDelete, true)
}

func (p *MemoryProvider) Restore(
	ctx context.Context,
	tenant, key string,
	expectedVersion uint64,
	actor string,
) (Definition, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return Definition{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	record, exists := p.tenants[tenant][key]
	if !exists || !record.deleted {
		return Definition{}, fmt.Errorf("tenant %q feature %q: %w", tenant, key, ErrNotFound)
	}
	if record.definition.Version != expectedVersion {
		return Definition{}, versionConflict(tenant, key, record.definition.Version, expectedVersion)
	}
	record.deleted = false
	record.definition.Lifecycle = LifecycleInactive
	record.definition.Version++
	p.tenants[tenant][key] = record
	p.appendAudit(tenant, key, AuditRestore, actor, record.definition.Version)

	return cloneDefinition(record.definition), nil
}

func (p *MemoryProvider) Audit(ctx context.Context, tenant, key string) ([]AuditEntry, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	entries := make([]AuditEntry, 0)
	for _, entry := range p.audit[tenant] {
		if entry.FeatureKey == key {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

func (p *MemoryProvider) setLifecycle(
	ctx context.Context,
	tenant, key string,
	expectedVersion uint64,
	actor string,
	lifecycle Lifecycle,
	action AuditAction,
	deleted bool,
) (Definition, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return Definition{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	record, exists := p.tenants[tenant][key]
	if !exists || record.deleted {
		return Definition{}, fmt.Errorf("tenant %q feature %q: %w", tenant, key, ErrNotFound)
	}
	if record.definition.Version != expectedVersion {
		return Definition{}, versionConflict(tenant, key, record.definition.Version, expectedVersion)
	}
	record.definition.Lifecycle = lifecycle
	record.definition.Version++
	record.deleted = deleted
	p.tenants[tenant][key] = record
	p.appendAudit(tenant, key, action, actor, record.definition.Version)

	return cloneDefinition(record.definition), nil
}

func (p *MemoryProvider) appendAudit(tenant, key string, action AuditAction, actor string, version uint64) {
	entries := append(p.audit[tenant], AuditEntry{
		FeatureKey: key,
		Action:     action,
		Actor:      actor,
		Version:    version,
	})
	if excess := len(entries) - p.limits.MaxAuditEntries; excess > 0 {
		entries = append([]AuditEntry(nil), entries[excess:]...)
	}
	p.audit[tenant] = entries
}

func versionConflict(tenant, key string, current, expected uint64) error {
	return fmt.Errorf(
		"tenant %q feature %q: %w: have %d, expected %d",
		tenant,
		key,
		ErrConflict,
		current,
		expected,
	)
}

func providerInput(ctx context.Context, tenant string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tenant == "" {
		return ErrTenantRequired
	}

	return nil
}
