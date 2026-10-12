package featureflags

import (
	"context"
	"fmt"
	"time"
)

// CleanupOptions selects explicit maintenance; zero fields preserve their corresponding
// state.
type CleanupOptions struct {
	// PurgeDeleted permanently removes tombstones and their pending stages.
	PurgeDeleted bool
	// DiscardStagesBefore discards nonzero scheduled stages strictly before this time.
	DiscardStagesBefore time.Time
	// KeepAudit retains the newest positive count; zero leaves audit unchanged.
	KeepAudit int
}

// CleanupReport reports completed maintenance counts, not a durable external audit.
type CleanupReport struct {
	// DeletedFeatures counts permanently purged tombstones.
	DeletedFeatures int
	// DiscardedStages counts removed pending changes.
	DiscardedStages int
	// DiscardedAudit counts removed oldest audit entries.
	DiscardedAudit int
}

// Cleanup performs only explicitly selected, tenant-scoped maintenance.
func (p *MemoryProvider) Cleanup(
	ctx context.Context,
	tenant string,
	options CleanupOptions,
) (CleanupReport, error) {
	if err := providerInput(ctx, tenant, p.limits.MaxKeyBytes); err != nil {
		return CleanupReport{}, err
	}
	if options.KeepAudit < 0 || options.KeepAudit > p.limits.MaxAuditEntries {
		return CleanupReport{}, fmt.Errorf("audit retention must be between 0 and %d", p.limits.MaxAuditEntries)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	report := CleanupReport{}
	purgedKeys := make(map[string]bool)
	if options.PurgeDeleted {
		for key, record := range p.tenants[tenant] {
			if record.deleted {
				delete(p.tenants[tenant], key)
				purgedKeys[key] = true
				report.DeletedFeatures++
			}
		}
	}
	for id, change := range p.staged[tenant] {
		discard := purgedKeys[change.Definition.Key]
		if !discard && !options.DiscardStagesBefore.IsZero() &&
			!change.ApplyAt.IsZero() && change.ApplyAt.Before(options.DiscardStagesBefore) {
			discard = true
		}
		if discard {
			delete(p.staged[tenant], id)
			report.DiscardedStages++
		}
	}
	if options.KeepAudit > 0 {
		discarded := len(p.audit[tenant]) - options.KeepAudit
		if discarded < 1 {
			return report, nil
		}
		report.DiscardedAudit = discarded
		p.audit[tenant] = append([]AuditEntry(nil), p.audit[tenant][discarded:]...)
	}

	return report, nil
}
