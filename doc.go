// Package featureflags provides deterministic, tenant-scoped feature
// management and evaluation.
//
// Callers create immutable snapshots from a Provider and pass an explicit
// Context to typed evaluation methods. Feature flags are product and rollout
// controls; they must not be used as authorization decisions.
//
// NewSnapshotWithGroups validates and copies graphs for concurrent reads.
// Evaluation order is inactive default, dependencies, child-first groups in
// attachment order, local ordered strategies, then default; the first match wins.
// Native Context maps are borrowed. Custom Strategy callbacks must be bounded,
// panic-free and immutable after snapshotting; no callback sandbox is provided.
//
// Providers own tenant-scoped management. Use DefaultLimits explicitly; limits
// are selected count/byte bounds, not total heap or deadline guarantees. Caller
// identifiers can appear in errors/audit; authentication and redaction are external.
// CachedProvider is process-local stale caching. Fleet owns optional background
// refresh/watch work, not backend/client cleanup or cluster-wide convergence.
package featureflags
