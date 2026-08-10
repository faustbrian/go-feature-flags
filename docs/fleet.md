# Fleet refresh and Kubernetes operation

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

`Fleet` owns one tenant and one process-local active snapshot. It adds an
optional refresher to the deterministic `Snapshot` evaluator; it does not add
cluster-wide coordination, provider discovery, or authorization.

## Startup contract

`Start` completes `Bootstrap` before launching one refresher. Readiness can be
published only after `Start` returns successfully and `Status` is inspected.
Applications MUST NOT publish fleet-dependent readiness before that point.
The `Start` context owns the refresher lifetime; canceling it stops the fleet.

| Source state | Bootstrap result |
|---|---|
| complete fresh provider snapshot | validate the entire graph, activate atomically, and become ready |
| empty provider | ready only when `AllowEmptyBootstrap` is explicit |
| stale provider or cache | usable only when `AllowStaleBootstrap` is explicit and age is no greater than `MaxStaleness`; status is degraded after `FreshFor` |
| malformed, partial, or excessively future-dated candidate | reject it; never replace the active snapshot |
| unavailable provider | try the configured `SnapshotCache`; fail when it has no valid bounded last-known-good candidate |

`SnapshotCandidate` carries opaque revision, bounded provenance, source time,
and the immutable snapshot. `ProviderSnapshotLoader` derives a stable SHA-256
content revision from a native provider snapshot without performing a second
provider read. It uses the adapter clock as source time and the portable export
codec for revision material. Providers with a durable commit timestamp or a
custom, non-exportable strategy must implement `SnapshotLoader` and supply
their provider-owned revision and source time instead. Durable and distributed
cache adapters implement `SnapshotCache`; the fleet never guesses whether
stale or malformed bytes are safe.

A loader revision MUST identify the complete snapshot graph and MUST NOT be
reused for different flag or group state. `SourceTime` MUST be the time that
revision became authoritative, not a cache read time, except when using
`ProviderSnapshotLoader` with its explicitly documented load-time semantics.

## Refresh and resilience composition

Every replacement is fully loaded and reconstructed through
`NewSnapshotWithGroups` before the active pointer changes. A provider error,
invalid graph, wrong tenant, reordered source time, or excessive age leaves the
last-known-good snapshot untouched.

`RefreshInterval`, deterministic replica-specific `MaxRefreshJitter`,
`MinRefreshInterval`, `LoadTimeout`, `MaxConcurrentProviderLoads`, `MaxWaiters`, and
`MaxProviderLoads` bound periodic and invalidation-triggered work. Concurrent
callers share one refresh flight. `DeterministicFleetJitter` hashes replica ID
and refresh sequence, so identically configured pods do not synchronize their
provider reads. `MaxFutureSkew` explicitly bounds accepted provider clock skew;
future source time cannot silently extend freshness beyond that allowance.
`Clock` implementations MUST be safe for concurrent use. Fleet retains the
greatest locally observed time so a wall-clock rollback cannot revive freshness
or extend a degraded last-known-good policy.

Configuration rejects more than 65,536 refresh waiters, 1,024 provider
attempts per executor call, 1,024 concurrent physical provider loads, 10,000
invalidation streams, or `DefaultLimits().MaxFeatures` policies. Applications
SHOULD configure materially smaller limits from measured pod and provider
capacity; these maxima are allocation safety ceilings, not defaults.

`RefreshExecutor` is the public composition seam for the repository retry,
circuit-breaker, bulkhead, adaptive-throttle, concurrency-limit, and shared
resilience-budget modules. Compose those public packages once around the
supplied `RefreshOperation`; do not copy their algorithms into a loader and do
not add another retry loop inside the provider. The fleet independently caps
physical provider calls with `MaxProviderLoads`, even when a supplied executor
is defective. `SnapshotCache` is the corresponding public cache seam.

An application-owned executor composes the public `retry.Do`,
`breaker.Execute`, `bulkhead.Execute`, `throttle.Execute`, and
`concurrencylimit.Execute` functions in the required order. Retry policies own
their public local or shared budget; the fleet does not clone retry, breaker,
bulkhead, throttle, concurrency, or budget state. All process-local fleet
limits still apply outside that composition.

Local rejections from a breaker, bulkhead, throttle, limiter, or shared budget
MUST remain local classifications. Configure `FailureClassifier` to map public
integration errors to `FleetFailureRetryExhausted`,
`FleetFailureCircuitOpen`, `FleetFailureBulkhead`, `FleetFailureThrottled`,
`FleetFailureConcurrency`, or `FleetFailureBudgetExhausted`. Fleet validates
the returned code and falls back to `FleetFailureProvider` for unknown values;
raw errors are returned to the caller but never retained in status. A retry
classifier MUST NOT convert local rejection, caller cancellation, or fleet
shutdown into a provider failure.

## Invalidation histories

An `Invalidation` has tenant, stream, monotonically increasing sequence,
revision, and observation time. The fleet retains one sequence per bounded
stream.

| Delivery | Result |
|---|---|
| same sequence | duplicate; no provider read |
| lower sequence | reordered; no provider read |
| next sequence and current revision | current; no provider read |
| sequence gap | loss is observable and one refresh is requested |
| different revision | one refresh is requested |
| delayed event | sequence and revision rules still apply; `Delay` remains observable |

Invalidation is a hint, not state. Lost Valkey publications are repaired by
the periodic refresh. `ConvergenceWindow` must cover the configured refresh,
jitter, and load timeout bound. `Status` exposes revision, provenance, age,
provider-load count, invalidation gaps, refresh waiters, watcher-running state,
and convergence state without retaining tenant values, raw provider errors, or
evaluation context.
`LastRefreshFailure`, `LastCacheFailure`, and `LastWatcherFailure` use bounded
failure codes. A cache store failure never rolls back a fully validated
activation; it is reported independently and a later successful store clears
it.

An optional `InvalidationWatcher` lets `Start` own exactly one cooperative
delivery loop. `Next` receives the fleet tenant and lifecycle context and MUST
unblock when that context is cancelled. The watcher owns its transport,
reconnection, and retry policy; Fleet adds no nested retry. Invalid events are
reported as `FleetFailureInvalidation`, watcher termination is reported as
`FleetFailureWatcher`, and provider refresh failures retain their provider or
caller-owned resilience classification. Periodic refresh continues after a
watcher terminates.

## Degraded evaluation

Per-flag `FlagPolicy` selects fail-closed, boolean fail-open, explicit typed
default, or bounded last-known-good behavior. Every fallback has a distinct
evaluation reason. A security-sensitive flag may use only fail-closed or
bounded last-known-good; construction rejects fail-open and default policies.
After its last-known-good limit, evaluation fails regardless of provider or
cache history.

Fail-closed returns `ErrSnapshotStale` and no flag value. Fail-open returns
boolean `true`; it is invalid for non-boolean evaluation. Explicit default
returns the configured typed value, and last-known-good evaluates the retained
snapshot only through its per-flag staleness limit.

Applications MUST mark every security-sensitive flag with
`SecuritySensitive`. Fleet copies and validates the policy map at construction;
an outage does not rewrite the selected mode or its staleness bound.

Flags remain product rollout inputs. Authentication and authorization must be
enforced independently before and after evaluation. Applications MUST NOT use
a flag result as proof of authorization.

## Kubernetes lifecycle

- **Cold pod:** call `Start` before readiness. Failed bootstrap keeps the pod
  unready; an explicitly accepted stale cache starts degraded and observable.
- **Rolling revisions and split traffic:** old and new pods may expose different
  revisions only inside `ConvergenceWindow`. Export fleet status with pod and
  application revision labels, not tenant or flag values.
- **Scale-up and HPA:** each new pod performs one bootstrap load. Stable
  replica-specific jitter spreads subsequent refreshes; provider capacity must
  include cold-start amplification and `MaxConcurrentProviderLoads` per fleet
  instance. Use the shared executor for a pod-wide or fleet-wide budget.
- **Provider outage and PostgreSQL failover:** current snapshots remain active.
  Per-flag policy controls stale evaluation; retries remain bounded by the
  executor, timeout, shared budget, and provider-load cap.
- **Valkey invalidation loss:** a sequence gap triggers refresh; a completely
  lost notification or terminated watcher converges through the periodic
  refresh.
- **Scale-down and SIGTERM:** fail readiness, call `Shutdown` with a pod
  termination deadline, then close caller-owned provider and cache resources.
  Shutdown cancels fleet provider and watcher work, joins the refresher,
  watcher, and refresh calls, and rejects new bootstrap, start, refresh, and
  activation work. Evaluation from the immutable current snapshot remains
  available for already admitted requests and still obeys freshness and
  per-flag degraded policy. Shutdown never closes caller-owned dependencies.

Set the Kubernetes termination grace period longer than the application drain
plus fleet shutdown budget. A loader that ignores cancellation retains its
provider slot and can cause `Shutdown` to return its context deadline; that gap
must remain visible rather than being reported as a clean stop.

[RFC2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC8174]: https://www.rfc-editor.org/rfc/rfc8174
