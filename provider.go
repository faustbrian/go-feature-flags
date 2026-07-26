package featureflags

import (
	"context"
	"errors"
	"time"
)

var (
	ErrAlreadyExists       = errors.New("feature already exists")
	ErrConflict            = errors.New("feature version conflict")
	ErrNotFound            = errors.New("feature not found")
	ErrTenantRequired      = errors.New("tenant is required")
	ErrTenantMismatch      = errors.New("evaluation context tenant does not match snapshot tenant")
	ErrContextLimit        = errors.New("evaluation context exceeds limit")
	ErrDependencyCycle     = errors.New("feature dependency cycle")
	ErrBatchLimit          = errors.New("evaluation batch exceeds limit")
	ErrGroupCycle          = errors.New("feature group inheritance cycle")
	ErrGroupInUse          = errors.New("feature group is in use")
	ErrInvalidValue        = errors.New("invalid feature value")
	ErrImportLimit         = errors.New("feature import exceeds limit")
	ErrStateLimit          = errors.New("provider state exceeds limit")
	ErrUnsupportedStrategy = errors.New("strategy cannot be serialized")
	ErrImportConflict      = errors.New("feature import conflict")
	ErrStorageConflict     = errors.New("provider storage conflict")
)

// Capabilities declares the management semantics implemented by a provider.
type Capabilities struct {
	OptimisticConcurrency bool
	AtomicMutations       bool
	Snapshots             bool
	Audit                 bool
	Groups                bool
	ImportExport          bool
}

// ProviderHealth is a low-cardinality provider readiness result.
type ProviderHealth struct {
	Healthy bool
	Code    string
}

// Provider is the shared native management and snapshot contract.
type Provider interface {
	Capabilities() Capabilities
	Create(context.Context, string, Definition, string) (Definition, error)
	Update(context.Context, string, Definition, uint64, string) (Definition, error)
	Activate(context.Context, string, string, uint64, string) (Definition, error)
	Deactivate(context.Context, string, string, uint64, string) (Definition, error)
	Delete(context.Context, string, string, uint64, string) (Definition, error)
	Restore(context.Context, string, string, uint64, string) (Definition, error)
	Snapshot(context.Context, string) (Snapshot, error)
	Audit(context.Context, string, string) ([]AuditEntry, error)
	CreateGroup(context.Context, string, GroupDefinition, string) (GroupDefinition, error)
	UpdateGroup(context.Context, string, GroupDefinition, uint64, string) (GroupDefinition, error)
	DeleteGroup(context.Context, string, string, uint64, string) (GroupDefinition, error)
	AssignGroup(context.Context, string, string, string, uint64, string) (Definition, error)
	RemoveGroup(context.Context, string, string, string, uint64, string) (Definition, error)
	ExportDocument(context.Context, string) ([]byte, error)
	ImportDocument(context.Context, string, []byte, ImportOptions, string) (ImportReport, error)
	StageUpdate(context.Context, string, Definition, uint64, time.Time, string) (StagedChange, error)
	ApplyStage(context.Context, string, uint64, string) (Definition, error)
	ApplyScheduled(context.Context, string, time.Time, string) ([]Definition, error)
	StagedChanges(context.Context, string) ([]StagedChange, error)
	Cleanup(context.Context, string, CleanupOptions) (CleanupReport, error)
	Health(context.Context) ProviderHealth
	Close(context.Context) error
}

// AuditAction is a stable management-plane mutation name.
type AuditAction string

const (
	AuditCreate       AuditAction = "create"
	AuditUpdate       AuditAction = "update"
	AuditActivate     AuditAction = "activate"
	AuditDeactivate   AuditAction = "deactivate"
	AuditDelete       AuditAction = "delete"
	AuditRestore      AuditAction = "restore"
	AuditGroupCreate  AuditAction = "group_create"
	AuditGroupUpdate  AuditAction = "group_update"
	AuditGroupDelete  AuditAction = "group_delete"
	AuditAssignGroup  AuditAction = "assign_group"
	AuditRemoveGroup  AuditAction = "remove_group"
	AuditImportCreate AuditAction = "import_create"
	AuditImportUpdate AuditAction = "import_update"
	AuditStageUpdate  AuditAction = "stage_update"
	AuditApplyStage   AuditAction = "apply_stage"
)

// AuditEntry records bounded mutation metadata without evaluation context or
// feature values.
type AuditEntry struct {
	FeatureKey string
	Action     AuditAction
	Actor      string
	Version    uint64
}
