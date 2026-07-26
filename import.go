package featureflags

import (
	"context"
	"fmt"
)

// ConflictPolicy controls how an import handles existing keys.
type ConflictPolicy string

const (
	ConflictFail    ConflictPolicy = "fail"
	ConflictSkip    ConflictPolicy = "skip"
	ConflictReplace ConflictPolicy = "replace"
)

// ImportOptions controls dry-run and conflict behavior.
type ImportOptions struct {
	DryRun         bool
	ConflictPolicy ConflictPolicy
}

// ImportConflict describes an existing resource without exposing its value.
type ImportConflict struct {
	Resource        string
	Key             string
	CurrentVersion  uint64
	IncomingVersion uint64
}

// ImportReport summarizes an atomic import plan or application.
type ImportReport struct {
	DryRun          bool
	CreatedFeatures int
	UpdatedFeatures int
	CreatedGroups   int
	UpdatedGroups   int
	Skipped         int
	Conflicts       []ImportConflict
}

type pendingAudit struct {
	key     string
	action  AuditAction
	version uint64
}

// ExportDocument returns a deterministic document for one tenant's active
// feature and group state.
func (p *MemoryProvider) ExportDocument(ctx context.Context, tenant string) ([]byte, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return nil, err
	}
	p.mu.RLock()
	definitions := make([]Definition, 0, len(p.tenants[tenant]))
	for _, record := range p.tenants[tenant] {
		if !record.deleted {
			definitions = append(definitions, cloneDefinition(record.definition))
		}
	}
	groups := p.groupDefinitionsLocked(tenant)
	p.mu.RUnlock()

	return Export(definitions, groups, p.limits)
}

// ImportDocument plans or atomically applies one deterministic document.
func (p *MemoryProvider) ImportDocument(
	ctx context.Context,
	tenant string,
	data []byte,
	options ImportOptions,
	actor string,
) (ImportReport, error) {
	if err := providerInput(ctx, tenant); err != nil {
		return ImportReport{}, err
	}
	if options.ConflictPolicy == "" {
		options.ConflictPolicy = ConflictFail
	}
	if options.ConflictPolicy != ConflictFail &&
		options.ConflictPolicy != ConflictSkip &&
		options.ConflictPolicy != ConflictReplace {
		return ImportReport{}, fmt.Errorf("unknown conflict policy %q", options.ConflictPolicy)
	}
	definitions, groups, err := Import(data, p.limits)
	if err != nil {
		return ImportReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return ImportReport{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	report := ImportReport{DryRun: options.DryRun}
	records := cloneMemoryRecords(p.tenants[tenant])
	groupRecords := cloneGroupMap(p.groups[tenant])
	audits := make([]pendingAudit, 0, len(definitions)+len(groups))
	for _, group := range groups {
		current, exists := groupRecords[group.Key]
		if exists {
			report.Conflicts = append(report.Conflicts, ImportConflict{
				Resource: "group", Key: group.Key,
				CurrentVersion: current.Version, IncomingVersion: group.Version,
			})
			switch options.ConflictPolicy {
			case ConflictFail, ConflictSkip:
				report.Skipped++
				continue
			case ConflictReplace:
				group.Version = current.Version + 1
				report.UpdatedGroups++
				audits = append(audits, pendingAudit{group.Key, AuditImportUpdate, group.Version})
			}
		} else {
			group.Version = 1
			report.CreatedGroups++
			audits = append(audits, pendingAudit{group.Key, AuditImportCreate, group.Version})
		}
		groupRecords[group.Key] = cloneGroup(group)
	}
	for _, definition := range definitions {
		current, exists := records[definition.Key]
		if exists {
			report.Conflicts = append(report.Conflicts, ImportConflict{
				Resource: "feature", Key: definition.Key,
				CurrentVersion: current.definition.Version, IncomingVersion: definition.Version,
			})
			switch options.ConflictPolicy {
			case ConflictFail, ConflictSkip:
				report.Skipped++
				continue
			case ConflictReplace:
				definition.Version = current.definition.Version + 1
				report.UpdatedFeatures++
				audits = append(audits, pendingAudit{definition.Key, AuditImportUpdate, definition.Version})
			}
		} else {
			definition.Version = 1
			report.CreatedFeatures++
			audits = append(audits, pendingAudit{definition.Key, AuditImportCreate, definition.Version})
		}
		records[definition.Key] = memoryRecord{definition: cloneDefinition(definition)}
	}
	if options.ConflictPolicy == ConflictFail && len(report.Conflicts) > 0 && !options.DryRun {
		return report, ErrImportConflict
	}
	active := definitionsFromRecords(records)
	groupList := groupsFromMap(groupRecords)
	if _, err := NewSnapshotWithGroups(active, groupList, p.limits); err != nil {
		return report, err
	}
	if options.DryRun {
		return report, nil
	}
	p.tenants[tenant] = records
	p.groups[tenant] = groupRecords
	for _, audit := range audits {
		p.appendAudit(tenant, audit.key, audit.action, actor, audit.version)
	}

	return report, nil
}

func cloneMemoryRecords(records map[string]memoryRecord) map[string]memoryRecord {
	cloned := make(map[string]memoryRecord, len(records))
	for key, record := range records {
		record.definition = cloneDefinition(record.definition)
		cloned[key] = record
	}

	return cloned
}

func cloneGroupMap(groups map[string]GroupDefinition) map[string]GroupDefinition {
	cloned := make(map[string]GroupDefinition, len(groups))
	for key, group := range groups {
		cloned[key] = cloneGroup(group)
	}

	return cloned
}

func definitionsFromRecords(records map[string]memoryRecord) []Definition {
	definitions := make([]Definition, 0, len(records))
	for _, record := range records {
		if !record.deleted {
			definitions = append(definitions, cloneDefinition(record.definition))
		}
	}

	return definitions
}

func groupsFromMap(groups map[string]GroupDefinition) []GroupDefinition {
	result := make([]GroupDefinition, 0, len(groups))
	for _, group := range groups {
		result = append(result, cloneGroup(group))
	}

	return result
}
