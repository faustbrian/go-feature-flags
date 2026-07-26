// Package featureflags provides deterministic, tenant-scoped feature
// management and evaluation.
//
// Callers create immutable snapshots from a Provider and pass an explicit
// Context to typed evaluation methods. Feature flags are product and rollout
// controls; they must not be used as authorization decisions.
package featureflags
