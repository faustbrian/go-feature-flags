package featureflags

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"
)

const tenantStateVersion = 1

// DocumentBackend atomically stores one opaque tenant state document.
type DocumentBackend interface {
	// Load returns copied document bytes, causal revision and explicit presence; errors are
	// not misses.
	Load(context.Context, string) ([]byte, uint64, bool, error)
	// CompareAndSwap atomically replaces only expected revision; mismatch returns
	// ErrStorageConflict.
	CompareAndSwap(context.Context, string, uint64, []byte) error
	// Health reports readiness without treating unavailability as a flag value.
	Health(context.Context) ProviderHealth
	// Close releases only explicitly owned resources using the supplied context.
	Close(context.Context) error
}

// DurableProvider applies the native Provider contract over an atomic
// document backend such as PostgreSQL or Valkey. Use NewDurableProvider with
// explicit valid limits and a concurrent backend. Each operation loads the full
// tenant document; mutations retry storage conflicts up to MaxStorageRetries
// additional attempts with no backoff. External IO honors backend contexts, not
// forced deadlines. Close delegates backend ownership; it does not gate admission.
type DurableProvider struct {
	backend DocumentBackend
	limits  Limits
}

// NewDurableProvider retains backend and limits without validation, opening or migrating
// storage.
func NewDurableProvider(backend DocumentBackend, limits Limits) *DurableProvider {
	return &DurableProvider{backend: backend, limits: limits}
}

// TenantByteLimit returns the configured tenant-key byte limit.
func (p *DurableProvider) TenantByteLimit() int { return p.limits.MaxKeyBytes }

// Capabilities reports native management capabilities.
func (*DurableProvider) Capabilities() Capabilities {
	return Capabilities{
		OptimisticConcurrency: true, AtomicMutations: true, Snapshots: true,
		Audit: true, Groups: true, ImportExport: true,
	}
}

// Health reports readiness without treating unavailability as a flag value.
func (p *DurableProvider) Health(ctx context.Context) ProviderHealth {
	return p.backend.Health(ctx)
}

// Close releases resources according to the implementation ownership contract.
func (p *DurableProvider) Close(ctx context.Context) error { return p.backend.Close(ctx) }

// Create creates a copied definition at version 1; existing tombstones also conflict.
// Storage conflicts trigger bounded whole-document retries.
func (p *DurableProvider) Create(ctx context.Context, tenant string, definition Definition, actor string) (Definition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (Definition, error) {
		return provider.Create(ctx, tenant, definition, actor)
	})
}

// Update replaces a live definition only at the expected version and increments it.
// Storage conflicts trigger bounded whole-document retries.
func (p *DurableProvider) Update(ctx context.Context, tenant string, definition Definition, expected uint64, actor string) (Definition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (Definition, error) {
		return provider.Update(ctx, tenant, definition, expected, actor)
	})
}

// Activate sets a live definition active at its expected version. Storage conflicts
// trigger bounded whole-document retries.
func (p *DurableProvider) Activate(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (Definition, error) {
		return provider.Activate(ctx, tenant, key, expected, actor)
	})
}

// Deactivate sets a live definition inactive at its expected version. Storage conflicts
// trigger bounded whole-document retries.
func (p *DurableProvider) Deactivate(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (Definition, error) {
		return provider.Deactivate(ctx, tenant, key, expected, actor)
	})
}

// Delete archives a live definition as a restorable tombstone at its expected version.
// Storage conflicts trigger bounded whole-document retries.
func (p *DurableProvider) Delete(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (Definition, error) {
		return provider.Delete(ctx, tenant, key, expected, actor)
	})
}

// Restore restores a tombstone as inactive at its expected version. Storage conflicts
// trigger bounded whole-document retries.
func (p *DurableProvider) Restore(ctx context.Context, tenant, key string, expected uint64, actor string) (Definition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (Definition, error) {
		return provider.Restore(ctx, tenant, key, expected, actor)
	})
}

// CreateGroup creates a version 1 group after validating the combined graph. Storage
// conflicts trigger bounded whole-document retries.
func (p *DurableProvider) CreateGroup(ctx context.Context, tenant string, group GroupDefinition, actor string) (GroupDefinition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (GroupDefinition, error) {
		return provider.CreateGroup(ctx, tenant, group, actor)
	})
}

// UpdateGroup replaces an expected-version group after validating its affected graph.
// Storage conflicts trigger bounded whole-document retries.
func (p *DurableProvider) UpdateGroup(ctx context.Context, tenant string, group GroupDefinition, expected uint64, actor string) (GroupDefinition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (GroupDefinition, error) {
		return provider.UpdateGroup(ctx, tenant, group, expected, actor)
	})
}

// DeleteGroup removes an expected-version group only when unreferenced. Storage conflicts
// trigger bounded whole-document retries.
func (p *DurableProvider) DeleteGroup(ctx context.Context, tenant, groupKey string, expected uint64, actor string) (GroupDefinition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (GroupDefinition, error) {
		return provider.DeleteGroup(ctx, tenant, groupKey, expected, actor)
	})
}

// AssignGroup appends a group to an expected-version feature; an existing assignment is
// unchanged. Storage conflicts trigger bounded whole-document retries.
func (p *DurableProvider) AssignGroup(ctx context.Context, tenant, featureKey, groupKey string, expected uint64, actor string) (Definition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (Definition, error) {
		return provider.AssignGroup(ctx, tenant, featureKey, groupKey, expected, actor)
	})
}

// RemoveGroup removes an existing assignment from an expected-version feature. Storage
// conflicts trigger bounded whole-document retries.
func (p *DurableProvider) RemoveGroup(ctx context.Context, tenant, featureKey, groupKey string, expected uint64, actor string) (Definition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (Definition, error) {
		return provider.RemoveGroup(ctx, tenant, featureKey, groupKey, expected, actor)
	})
}

// Snapshot returns a tenant-bound graph copy; malformed cross-references remain errors.
// Each call reloads the whole tenant document.
func (p *DurableProvider) Snapshot(ctx context.Context, tenant string) (Snapshot, error) {
	provider, _, err := p.load(ctx, tenant)
	if err != nil {
		return Snapshot{}, err
	}
	return provider.Snapshot(ctx, tenant)
}

// Audit returns owned retained entries for the resource key, not durable audit delivery.
// Each call reloads the whole tenant document.
func (p *DurableProvider) Audit(ctx context.Context, tenant, key string) ([]AuditEntry, error) {
	provider, _, err := p.load(ctx, tenant)
	if err != nil {
		return nil, err
	}
	return provider.Audit(ctx, tenant, key)
}

// ExportDocument returns owned portable bytes excluding tombstones, audit and staged
// state. Each call reloads the whole tenant document.
func (p *DurableProvider) ExportDocument(ctx context.Context, tenant string) ([]byte, error) {
	provider, _, err := p.load(ctx, tenant)
	if err != nil {
		return nil, err
	}
	return provider.ExportDocument(ctx, tenant)
}

// ImportDocument plans or atomically applies a portable document; inspect report and error
// together. Storage conflicts trigger bounded whole-document retries.
func (p *DurableProvider) ImportDocument(ctx context.Context, tenant string, data []byte, options ImportOptions, actor string) (ImportReport, error) {
	if options.DryRun {
		provider, _, err := p.load(ctx, tenant)
		if err != nil {
			return ImportReport{}, err
		}
		return provider.ImportDocument(ctx, tenant, data, options, actor)
	}
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (ImportReport, error) {
		return provider.ImportDocument(ctx, tenant, data, options, actor)
	})
}

// StageUpdate copies a pending expected-version replacement without applying it. Storage
// conflicts trigger bounded whole-document retries.
func (p *DurableProvider) StageUpdate(
	ctx context.Context,
	tenant string,
	definition Definition,
	expected uint64,
	applyAt time.Time,
	actor string,
) (StagedChange, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (StagedChange, error) {
		return provider.StageUpdate(ctx, tenant, definition, expected, applyAt, actor)
	})
}

// ApplyStage atomically applies one pending replacement after rechecking its version.
// Storage conflicts trigger bounded whole-document retries.
func (p *DurableProvider) ApplyStage(
	ctx context.Context,
	tenant string,
	id uint64,
	actor string,
) (Definition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (Definition, error) {
		return provider.ApplyStage(ctx, tenant, id, actor)
	})
}

// ApplyScheduled atomically applies due nonzero-time stages in ID order; any failure
// aborts all. Storage conflicts trigger bounded whole-document retries.
func (p *DurableProvider) ApplyScheduled(
	ctx context.Context,
	tenant string,
	now time.Time,
	actor string,
) ([]Definition, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) ([]Definition, error) {
		return provider.ApplyScheduled(ctx, tenant, now, actor)
	})
}

// StagedChanges returns owned pending changes in ID order. Each call reloads the whole
// tenant document.
func (p *DurableProvider) StagedChanges(ctx context.Context, tenant string) ([]StagedChange, error) {
	provider, _, err := p.load(ctx, tenant)
	if err != nil {
		return nil, err
	}

	return provider.StagedChanges(ctx, tenant)
}

// Cleanup performs only explicitly selected tenant maintenance. Storage conflicts trigger
// bounded whole-document retries.
func (p *DurableProvider) Cleanup(
	ctx context.Context,
	tenant string,
	options CleanupOptions,
) (CleanupReport, error) {
	return durableMutate(p, ctx, tenant, func(provider *MemoryProvider) (CleanupReport, error) {
		return provider.Cleanup(ctx, tenant, options)
	})
}

type loadedRevision struct{ revision uint64 }

// Retry reconstructs all tenant state before repeating the mutation. Custom
// strategies must tolerate repeated validation/snapshot callbacks; only a
// successful document CAS publishes feature, group, audit and stage changes.
func durableMutate[T any](provider *DurableProvider, ctx context.Context, tenant string, mutation func(*MemoryProvider) (T, error)) (T, error) {
	var zero T
	if err := providerInput(ctx, tenant, provider.limits.MaxKeyBytes); err != nil {
		return zero, err
	}
	for attempt := 0; attempt <= provider.limits.MaxStorageRetries; attempt++ {
		memory, loaded, err := provider.load(ctx, tenant)
		if err != nil {
			return zero, err
		}
		result, err := mutation(memory)
		if err != nil {
			return zero, err
		}
		data, err := marshalTenantState(memory, tenant)
		if err != nil {
			return zero, err
		}
		if err := provider.backend.CompareAndSwap(ctx, tenant, loaded.revision, data); err != nil {
			if errors.Is(err, ErrStorageConflict) {
				continue
			}
			return zero, err
		}
		return result, nil
	}
	return zero, ErrStorageConflict
}

func (p *DurableProvider) load(ctx context.Context, tenant string) (*MemoryProvider, loadedRevision, error) {
	if err := providerInput(ctx, tenant, p.limits.MaxKeyBytes); err != nil {
		return nil, loadedRevision{}, err
	}
	data, revision, exists, err := p.backend.Load(ctx, tenant)
	if err != nil {
		return nil, loadedRevision{}, err
	}
	if !exists {
		return NewMemoryProvider(p.limits), loadedRevision{revision: revision}, nil
	}
	memory, err := unmarshalTenantState(data, tenant, p.limits)
	if err != nil {
		return nil, loadedRevision{}, err
	}
	return memory, loadedRevision{revision: revision}, nil
}

type tenantStateWire struct {
	Version   int                `json:"version"`
	Records   []tenantRecordWire `json:"records,omitempty"`
	Groups    []groupWire        `json:"groups,omitempty"`
	Audit     []AuditEntry       `json:"audit,omitempty"`
	Staged    []stagedChangeWire `json:"staged,omitempty"`
	NextStage uint64             `json:"next_stage,omitempty"`
}

type tenantRecordWire struct {
	Definition definitionWire `json:"definition"`
	Deleted    bool           `json:"deleted,omitempty"`
}

type stagedChangeWire struct {
	ID              uint64         `json:"id"`
	Definition      definitionWire `json:"definition"`
	ExpectedVersion uint64         `json:"expected_version"`
	ApplyAt         time.Time      `json:"apply_at,omitempty"`
	Actor           string         `json:"actor,omitempty"`
}

func marshalTenantState(provider *MemoryProvider, tenant string) ([]byte, error) {
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	state := tenantStateWire{Version: tenantStateVersion}
	keys := make([]string, 0, len(provider.tenants[tenant]))
	for key := range provider.tenants[tenant] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		record := provider.tenants[tenant][key]
		encoded, err := encodeDefinition(record.definition)
		if err != nil {
			return nil, err
		}
		state.Records = append(state.Records, tenantRecordWire{Definition: encoded, Deleted: record.deleted})
	}
	groupKeys := make([]string, 0, len(provider.groups[tenant]))
	for key := range provider.groups[tenant] {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)
	for _, key := range groupKeys {
		encoded, err := encodeGroup(provider.groups[tenant][key])
		if err != nil {
			return nil, err
		}
		state.Groups = append(state.Groups, encoded)
	}
	state.Audit = append([]AuditEntry(nil), provider.audit[tenant]...)
	stageIDs := make([]uint64, 0, len(provider.staged[tenant]))
	for id := range provider.staged[tenant] {
		stageIDs = append(stageIDs, id)
	}
	slices.Sort(stageIDs)
	for _, id := range stageIDs {
		change := provider.staged[tenant][id]
		encoded, err := encodeDefinition(change.Definition)
		if err != nil {
			return nil, err
		}
		state.Staged = append(state.Staged, stagedChangeWire{
			ID: change.ID, Definition: encoded, ExpectedVersion: change.ExpectedVersion,
			ApplyAt: change.ApplyAt, Actor: change.Actor,
		})
	}
	state.NextStage = provider.nextStage[tenant]
	data, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if len(data) > provider.limits.MaxStateBytes {
		return nil, fmt.Errorf("tenant state exceeds %d bytes: %w", provider.limits.MaxStateBytes, ErrStateLimit)
	}

	return data, nil
}

func unmarshalTenantState(data []byte, tenant string, limits Limits) (*MemoryProvider, error) {
	if len(data) > limits.MaxStateBytes {
		return nil, fmt.Errorf("tenant state exceeds %d bytes: %w", limits.MaxStateBytes, ErrStateLimit)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state tenantStateWire
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode tenant state: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if state.Version != tenantStateVersion {
		return nil, fmt.Errorf("unsupported tenant state version %d", state.Version)
	}
	provider := NewMemoryProvider(limits)
	provider.tenants[tenant] = make(map[string]memoryRecord, len(state.Records))
	for _, encoded := range state.Records {
		definition, err := decodeDefinition(encoded.Definition)
		if err != nil {
			return nil, err
		}
		if err := definition.Validate(limits); err != nil {
			return nil, err
		}
		if _, exists := provider.tenants[tenant][definition.Key]; exists {
			return nil, fmt.Errorf("duplicate persisted feature %q", definition.Key)
		}
		provider.tenants[tenant][definition.Key] = memoryRecord{definition: cloneDefinition(definition), deleted: encoded.Deleted}
	}
	groups := make([]GroupDefinition, 0, len(state.Groups))
	for _, encoded := range state.Groups {
		group, err := decodeGroup(encoded)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	clonedGroups, err := cloneAndValidateGroups(provider.activeDefinitionsLocked(tenant), groups, limits)
	if err != nil {
		return nil, err
	}
	provider.groups[tenant] = clonedGroups
	if len(state.Audit) > limits.MaxAuditEntries {
		return nil, fmt.Errorf("persisted audit exceeds limit %d", limits.MaxAuditEntries)
	}
	provider.audit[tenant] = append([]AuditEntry(nil), state.Audit...)
	if len(state.Staged) > limits.MaxStagedChanges {
		return nil, fmt.Errorf("persisted staged changes exceed limit %d", limits.MaxStagedChanges)
	}
	provider.staged[tenant] = make(map[uint64]StagedChange, len(state.Staged))
	for _, encoded := range state.Staged {
		definition, err := decodeDefinition(encoded.Definition)
		if err != nil {
			return nil, err
		}
		if err := definition.Validate(limits); err != nil {
			return nil, err
		}
		if encoded.ID == 0 || encoded.ID > state.NextStage {
			return nil, fmt.Errorf("persisted staged change has invalid id %d", encoded.ID)
		}
		if _, exists := provider.staged[tenant][encoded.ID]; exists {
			return nil, fmt.Errorf("duplicate persisted staged change %d", encoded.ID)
		}
		provider.staged[tenant][encoded.ID] = StagedChange{
			ID: encoded.ID, Definition: definition, ExpectedVersion: encoded.ExpectedVersion,
			ApplyAt: encoded.ApplyAt, Actor: encoded.Actor,
		}
	}
	provider.nextStage[tenant] = state.NextStage
	return provider, nil
}
