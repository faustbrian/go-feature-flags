package featureflags

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrAlreadyExists identifies feature already exists.
	ErrAlreadyExists = errors.New("feature already exists")
	// ErrConflict identifies feature version conflict.
	ErrConflict = errors.New("feature version conflict")
	// ErrNotFound identifies feature not found.
	ErrNotFound = errors.New("feature not found")
	// ErrTenantRequired identifies tenant is required.
	ErrTenantRequired = errors.New("tenant is required")
	// ErrTenantMismatch identifies evaluation context tenant does not match snapshot tenant.
	ErrTenantMismatch = errors.New("evaluation context tenant does not match snapshot tenant")
	// ErrContextLimit identifies evaluation context exceeds limit.
	ErrContextLimit = errors.New("evaluation context exceeds limit")
	// ErrDependencyCycle identifies feature dependency cycle.
	ErrDependencyCycle = errors.New("feature dependency cycle")
	// ErrBatchLimit identifies evaluation batch exceeds limit.
	ErrBatchLimit = errors.New("evaluation batch exceeds limit")
	// ErrGroupCycle identifies feature group inheritance cycle.
	ErrGroupCycle = errors.New("feature group inheritance cycle")
	// ErrGroupInUse identifies feature group is in use.
	ErrGroupInUse = errors.New("feature group is in use")
	// ErrInvalidValue identifies invalid feature value.
	ErrInvalidValue = errors.New("invalid feature value")
	// ErrImportLimit identifies feature import exceeds limit.
	ErrImportLimit = errors.New("feature import exceeds limit")
	// ErrStateLimit identifies provider state exceeds limit.
	ErrStateLimit = errors.New("provider state exceeds limit")
	// ErrUnsupportedStrategy identifies strategy cannot be serialized.
	ErrUnsupportedStrategy = errors.New("strategy cannot be serialized")
	// ErrImportConflict identifies feature import conflict.
	ErrImportConflict = errors.New("feature import conflict")
	// ErrStorageConflict identifies provider storage conflict.
	ErrStorageConflict = errors.New("provider storage conflict")
)

// Capabilities declares the management semantics implemented by a provider.
type Capabilities struct {
	// OptimisticConcurrency reports expected-version mutation support.
	OptimisticConcurrency bool
	// AtomicMutations reports all-or-none native changes, not a cluster transaction.
	AtomicMutations bool
	// Snapshots reports consistent immutable evaluation snapshots.
	Snapshots bool
	// Audit reports bounded retained mutation history.
	Audit bool
	// Groups reports reusable inherited policy management.
	Groups bool
	// ImportExport reports portable document planning/application support.
	ImportExport bool
}

// ProviderHealth is a low-cardinality provider readiness result.
type ProviderHealth struct {
	// Healthy reports readiness, not an evaluation answer.
	Healthy bool
	// Code is a low-cardinality status; custom providers must exclude sensitive data.
	Code string
}

// Provider is the concurrent native management and snapshot contract. Supply
// non-nil contexts, tenant scope and safe actor/resource identifiers; tenant
// binding is partitioning, not authentication. Expected versions are per
// resource. Implementations must isolate snapshots through trusted strategy
// copying, report failures rather than misses, and state Close ownership.
// Bundled providers check cancellation at admission; memory lock waits and
// trusted callbacks are not forcibly preempted. Close need not disable use.
type Provider interface {
	// Capabilities reports native management capabilities.
	Capabilities() Capabilities
	// Create creates a copied definition at version 1; existing tombstones also conflict.
	Create(context.Context, string, Definition, string) (Definition, error)
	// Update replaces a live definition at its expected version and increments it.
	Update(context.Context, string, Definition, uint64, string) (Definition, error)
	// Activate sets a live definition active at its expected version.
	Activate(context.Context, string, string, uint64, string) (Definition, error)
	// Deactivate sets a live definition inactive at its expected version.
	Deactivate(context.Context, string, string, uint64, string) (Definition, error)
	// Delete archives a live definition as a restorable tombstone at its expected version.
	Delete(context.Context, string, string, uint64, string) (Definition, error)
	// Restore restores a tombstone as inactive at its expected version.
	Restore(context.Context, string, string, uint64, string) (Definition, error)
	// Snapshot returns a copied tenant-bound graph; malformed cross-references remain errors.
	Snapshot(context.Context, string) (Snapshot, error)
	// Audit returns owned retained entries for the feature/group key, not external delivery.
	Audit(context.Context, string, string) ([]AuditEntry, error)
	// CreateGroup creates a version 1 group after validating the combined graph.
	CreateGroup(context.Context, string, GroupDefinition, string) (GroupDefinition, error)
	// UpdateGroup replaces an expected-version group after validating its affected graph.
	UpdateGroup(context.Context, string, GroupDefinition, uint64, string) (GroupDefinition, error)
	// DeleteGroup removes an expected-version group only when unreferenced.
	DeleteGroup(context.Context, string, string, uint64, string) (GroupDefinition, error)
	// AssignGroup appends a group at the expected feature version; existing assignment is
	// unchanged.
	AssignGroup(context.Context, string, string, string, uint64, string) (Definition, error)
	// RemoveGroup removes an existing group assignment at the expected feature version.
	RemoveGroup(context.Context, string, string, string, uint64, string) (Definition, error)
	// ExportDocument returns owned portable bytes excluding tombstones, audit and staged
	// state.
	ExportDocument(context.Context, string) ([]byte, error)
	// ImportDocument plans or atomically applies a portable document; inspect report and
	// error together.
	ImportDocument(context.Context, string, []byte, ImportOptions, string) (ImportReport, error)
	// StageUpdate copies a pending expected-version replacement without applying it.
	StageUpdate(context.Context, string, Definition, uint64, time.Time, string) (StagedChange, error)
	// ApplyStage atomically applies one stage after rechecking its version.
	ApplyStage(context.Context, string, uint64, string) (Definition, error)
	// ApplyScheduled atomically applies due nonzero-time stages in ID order; failure aborts
	// all.
	ApplyScheduled(context.Context, string, time.Time, string) ([]Definition, error)
	// StagedChanges returns owned pending changes in ID order.
	StagedChanges(context.Context, string) ([]StagedChange, error)
	// Cleanup performs only explicitly selected tenant maintenance.
	Cleanup(context.Context, string, CleanupOptions) (CleanupReport, error)
	// Health reports readiness without treating unavailability as a flag value.
	Health(context.Context) ProviderHealth
	// Close releases only explicitly owned resources with the supplied context.
	Close(context.Context) error
}

// AuditAction is a stable management-plane mutation name.
type AuditAction string

const (
	// AuditCreate records the create management action.
	AuditCreate AuditAction = "create"
	// AuditUpdate records the update management action.
	AuditUpdate AuditAction = "update"
	// AuditActivate records the activate management action.
	AuditActivate AuditAction = "activate"
	// AuditDeactivate records the deactivate management action.
	AuditDeactivate AuditAction = "deactivate"
	// AuditDelete records the delete management action.
	AuditDelete AuditAction = "delete"
	// AuditRestore records the restore management action.
	AuditRestore AuditAction = "restore"
	// AuditGroupCreate records the group create management action.
	AuditGroupCreate AuditAction = "group_create"
	// AuditGroupUpdate records the group update management action.
	AuditGroupUpdate AuditAction = "group_update"
	// AuditGroupDelete records the group delete management action.
	AuditGroupDelete AuditAction = "group_delete"
	// AuditAssignGroup records the assign group management action.
	AuditAssignGroup AuditAction = "assign_group"
	// AuditRemoveGroup records the remove group management action.
	AuditRemoveGroup AuditAction = "remove_group"
	// AuditImportCreate records the import create management action.
	AuditImportCreate AuditAction = "import_create"
	// AuditImportUpdate records the import update management action.
	AuditImportUpdate AuditAction = "import_update"
	// AuditStageUpdate records the stage update management action.
	AuditStageUpdate AuditAction = "stage_update"
	// AuditApplyStage records the apply stage management action.
	AuditApplyStage AuditAction = "apply_stage"
)

// AuditEntry records mutation identity without dedicated context/value fields.
// Retention count is bounded, not Actor bytes; callers must keep labels safe.
type AuditEntry struct {
	// FeatureKey names the feature or group affected by Action.
	FeatureKey string
	// Action classifies the management mutation.
	Action AuditAction
	// Actor is caller attribution; memory does not bound or sanitize its bytes.
	Actor string
	// Version is the resource version, not a global revision.
	Version uint64
}
