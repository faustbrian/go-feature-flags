package featureflags

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// StagedChange is an immutable pending feature replacement.
type StagedChange struct {
	ID              uint64
	Definition      Definition
	ExpectedVersion uint64
	ApplyAt         time.Time
	Actor           string
}

func (p *MemoryProvider) StageUpdate(
	ctx context.Context,
	tenant string,
	definition Definition,
	expectedVersion uint64,
	applyAt time.Time,
	actor string,
) (StagedChange, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return StagedChange{}, err
	}
	if err := definition.Validate(p.limits); err != nil {
		return StagedChange{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	record, exists := p.tenants[tenant][definition.Key]
	if !exists || record.deleted {
		return StagedChange{}, fmt.Errorf("tenant %q feature %q: %w", tenant, definition.Key, ErrNotFound)
	}
	if record.definition.Version != expectedVersion {
		return StagedChange{}, versionConflict(tenant, definition.Key, record.definition.Version, expectedVersion)
	}
	if len(p.staged[tenant]) >= p.limits.MaxStagedChanges {
		return StagedChange{}, fmt.Errorf("tenant %q staged changes exceed %d", tenant, p.limits.MaxStagedChanges)
	}
	for _, staged := range p.staged[tenant] {
		if staged.Definition.Key == definition.Key {
			return StagedChange{}, fmt.Errorf("tenant %q feature %q has staged change: %w", tenant, definition.Key, ErrAlreadyExists)
		}
	}
	if p.staged[tenant] == nil {
		p.staged[tenant] = make(map[uint64]StagedChange)
	}
	p.nextStage[tenant]++
	change := StagedChange{
		ID: p.nextStage[tenant], Definition: cloneDefinition(definition),
		ExpectedVersion: expectedVersion, ApplyAt: applyAt, Actor: actor,
	}
	p.staged[tenant][change.ID] = change
	p.appendAudit(tenant, definition.Key, AuditStageUpdate, actor, expectedVersion)

	return cloneStagedChange(change), nil
}

func (p *MemoryProvider) ApplyStage(
	ctx context.Context,
	tenant string,
	id uint64,
	actor string,
) (Definition, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return Definition{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	results, err := p.applyStagesLocked(tenant, []uint64{id}, actor)
	if err != nil {
		return Definition{}, err
	}

	return results[0], nil
}

func (p *MemoryProvider) ApplyScheduled(
	ctx context.Context,
	tenant string,
	now time.Time,
	actor string,
) ([]Definition, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, fmt.Errorf("scheduled application time is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]uint64, 0)
	for id, change := range p.staged[tenant] {
		if !change.ApplyAt.IsZero() && !change.ApplyAt.After(now) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		return []Definition{}, nil
	}

	return p.applyStagesLocked(tenant, ids, actor)
}

func (p *MemoryProvider) StagedChanges(ctx context.Context, tenant string) ([]StagedChange, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	ids := make([]uint64, 0, len(p.staged[tenant]))
	for id := range p.staged[tenant] {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	changes := make([]StagedChange, 0, len(ids))
	for _, id := range ids {
		changes = append(changes, cloneStagedChange(p.staged[tenant][id]))
	}

	return changes, nil
}

func (p *MemoryProvider) applyStagesLocked(tenant string, ids []uint64, actor string) ([]Definition, error) {
	records := cloneMemoryRecords(p.tenants[tenant])
	results := make([]Definition, 0, len(ids))
	for _, id := range ids {
		change, exists := p.staged[tenant][id]
		if !exists {
			return nil, fmt.Errorf("tenant %q staged change %d: %w", tenant, id, ErrNotFound)
		}
		record, exists := records[change.Definition.Key]
		if !exists || record.deleted {
			return nil, fmt.Errorf("tenant %q feature %q: %w", tenant, change.Definition.Key, ErrNotFound)
		}
		if record.definition.Version != change.ExpectedVersion {
			return nil, versionConflict(tenant, change.Definition.Key, record.definition.Version, change.ExpectedVersion)
		}
		updated := cloneDefinition(change.Definition)
		updated.Version = record.definition.Version + 1
		record.definition = updated
		records[updated.Key] = record
		results = append(results, cloneDefinition(updated))
	}
	if _, err := NewSnapshotWithGroups(definitionsFromRecords(records), p.groupDefinitionsLocked(tenant), p.limits); err != nil {
		return nil, err
	}
	p.tenants[tenant] = records
	for index, id := range ids {
		delete(p.staged[tenant], id)
		p.appendAudit(tenant, results[index].Key, AuditApplyStage, actor, results[index].Version)
	}

	return results, nil
}

func cloneStagedChange(change StagedChange) StagedChange {
	change.Definition = cloneDefinition(change.Definition)

	return change
}
