package featureflags

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxFleetIdentityLength          = 128
	maxFleetWaiters                 = 65_536
	maxFleetProviderLoads           = 1_024
	maxFleetConcurrentProviderLoads = 1_024
	maxFleetInvalidationStreams     = 10_000
)

var (
	ErrNoUsableSnapshot    = errors.New("feature flag fleet has no usable snapshot")
	ErrMalformedSnapshot   = errors.New("feature flag fleet snapshot is malformed")
	ErrSnapshotStale       = errors.New("feature flag fleet snapshot is too stale")
	ErrSnapshotFuture      = errors.New("feature flag fleet snapshot source time exceeds allowed clock skew")
	ErrUnsafeFlagPolicy    = errors.New("feature flag fleet policy is unsafe for a security-sensitive flag")
	ErrRefreshLoadLimit    = errors.New("feature flag fleet provider load limit exceeded")
	ErrRefreshWaiterLimit  = errors.New("feature flag fleet refresh waiter limit exceeded")
	ErrFleetStopped        = errors.New("feature flag fleet is stopped")
	ErrSnapshotReordered   = errors.New("feature flag fleet snapshot is older than the active revision")
	ErrInvalidInvalidation = errors.New("feature flag fleet invalidation is invalid")
	ErrInvalidationStreams = errors.New("feature flag fleet invalidation stream limit exceeded")
)

// SnapshotCandidate is a fully loaded snapshot and its provider-owned causal
// metadata. Revision must identify the complete graph and must not be reused
// for different state. SourceTime is the time that revision became
// authoritative. A fleet validates the complete snapshot before activation.
type SnapshotCandidate struct {
	Snapshot   Snapshot
	Revision   string
	Provenance string
	SourceTime time.Time
}

// SnapshotLoader loads one complete replacement candidate. Implementations
// must not return partial state as a successful candidate.
type SnapshotLoader interface {
	Load(context.Context, string) (SnapshotCandidate, error)
}

// SnapshotLoadFunc adapts a function to SnapshotLoader.
type SnapshotLoadFunc func(context.Context, string) (SnapshotCandidate, error)

func (function SnapshotLoadFunc) Load(ctx context.Context, tenant string) (SnapshotCandidate, error) {
	return function(ctx, tenant)
}

// ProviderSnapshotLoader adapts the native Provider contract and derives a
// stable content revision from the fully validated immutable snapshot.
type ProviderSnapshotLoader struct {
	provider   Provider
	clock      CacheClock
	provenance string
}

// NewProviderSnapshotLoader constructs a provider adapter with bounded public
// provenance metadata.
func NewProviderSnapshotLoader(provider Provider, clock CacheClock, provenance string) (*ProviderSnapshotLoader, error) {
	if provider == nil || clock == nil || provenance == "" || cmp.Compare(len(provenance), maxFleetIdentityLength) == 1 {
		return nil, fmt.Errorf("provider loader dependencies and provenance are invalid")
	}
	return &ProviderSnapshotLoader{provider: provider, clock: clock, provenance: provenance}, nil
}

// Load returns a complete candidate without a second provider read.
func (loader *ProviderSnapshotLoader) Load(ctx context.Context, tenant string) (SnapshotCandidate, error) {
	snapshot, err := loader.provider.Snapshot(ctx, tenant)
	if err != nil {
		return SnapshotCandidate{}, err
	}
	definitions := make([]Definition, 0, len(snapshot.definitions))
	for _, definition := range snapshot.definitions {
		definitions = append(definitions, definition)
	}
	groups := make([]GroupDefinition, 0, len(snapshot.groups))
	for _, group := range snapshot.groups {
		groups = append(groups, group)
	}
	document, err := Export(definitions, groups, snapshot.limits)
	if err != nil {
		return SnapshotCandidate{}, err
	}
	digest := sha256.Sum256(document)
	return SnapshotCandidate{
		Snapshot: snapshot, Revision: fmt.Sprintf("%x", digest),
		Provenance: loader.provenance, SourceTime: loader.clock.Now().Round(0),
	}, nil
}

// SnapshotCache is an optional durable or distributed last-known-good store.
// Cache ownership and shutdown remain with the caller.
type SnapshotCache interface {
	Load(context.Context, string) (SnapshotCandidate, bool, error)
	Store(context.Context, string, SnapshotCandidate) error
}

// RefreshOperation is one complete provider load.
type RefreshOperation func(context.Context) (SnapshotCandidate, error)

// RefreshExecutor composes caller-owned retry, breaker, bulkhead, throttle,
// concurrency-limit, cache, and shared-budget policies around provider work.
// Fleet still enforces MaxProviderLoads even if an executor retries.
type RefreshExecutor interface {
	Execute(context.Context, RefreshOperation) (SnapshotCandidate, error)
}

// RefreshExecuteFunc adapts a function to RefreshExecutor.
type RefreshExecuteFunc func(context.Context, RefreshOperation) (SnapshotCandidate, error)

func (function RefreshExecuteFunc) Execute(ctx context.Context, operation RefreshOperation) (SnapshotCandidate, error) {
	return function(ctx, operation)
}

// FleetFailureClassifier maps caller-owned resilience rejection errors into a
// bounded fleet status code. Fleet-owned validation and cancellation errors
// always retain their built-in classification.
type FleetFailureClassifier interface {
	Classify(error) FleetFailureCode
}

// FleetFailureClassifyFunc adapts a function to FleetFailureClassifier.
type FleetFailureClassifyFunc func(error) FleetFailureCode

func (function FleetFailureClassifyFunc) Classify(err error) FleetFailureCode {
	return function(err)
}

// FleetSleeper provides cancelable refresh scheduling.
type FleetSleeper interface {
	Sleep(context.Context, time.Duration) error
}

// FleetJitter derives a bounded delay for one replica and refresh sequence.
type FleetJitter interface {
	Delay(string, uint64, time.Duration) (time.Duration, error)
}

type systemFleetSleeper struct{}

func (systemFleetSleeper) Sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// DeterministicFleetJitter spreads replicas without ambient randomness.
type DeterministicFleetJitter struct{}

// Delay returns a stable value in the inclusive configured bound.
func (DeterministicFleetJitter) Delay(replica string, sequence uint64, maximum time.Duration) (time.Duration, error) {
	if replica == "" || cmp.Compare(len(replica), maxFleetIdentityLength) == 1 || sequence == 0 || cmp.Compare(maximum, time.Duration(0)) == -1 {
		return 0, fmt.Errorf("fleet jitter input is invalid")
	}
	if maximum == 0 {
		return 0, nil
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("go-feature-flags/fleet-jitter/v1"))
	_, _ = hash.Write([]byte(replica))
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], sequence)
	_, _ = hash.Write(encoded[:])
	sum := hash.Sum(nil)
	value := binary.BigEndian.Uint64(sum[:8]) % (uint64(maximum) + 1)
	return time.Duration(value), nil
}

// DegradedMode defines the explicit per-flag behavior while no fresh snapshot
// is available.
type DegradedMode uint8

const (
	DegradedFailClosed DegradedMode = iota + 1
	DegradedFailOpen
	DegradedDefault
	DegradedLastKnownGood
)

// FlagPolicy controls one flag during a provider outage or stale period.
type FlagPolicy struct {
	Mode              DegradedMode
	Default           Value
	MaxStaleness      time.Duration
	SecuritySensitive bool
}

// FleetConfig contains finite process-local limits. Clock must support
// concurrent calls; Fleet prevents a later clock rollback from reviving
// freshness. MaxProviderLoads limits attempts made by one executor call, while
// MaxConcurrentProviderLoads limits physical loader calls in one Fleet.
// MaxFutureSkew bounds future provider source time. Cluster- or pod-wide retry,
// concurrency, and load budgets belong in the shared RefreshExecutor. Fleet
// does not provide cluster-wide exclusion or authorization semantics.
type FleetConfig struct {
	Tenant                     string
	ReplicaID                  string
	Loader                     SnapshotLoader
	Cache                      SnapshotCache
	Executor                   RefreshExecutor
	FailureClassifier          FleetFailureClassifier
	Clock                      CacheClock
	Sleeper                    FleetSleeper
	Jitter                     FleetJitter
	RefreshInterval            time.Duration
	MinRefreshInterval         time.Duration
	MaxRefreshJitter           time.Duration
	LoadTimeout                time.Duration
	FreshFor                   time.Duration
	MaxStaleness               time.Duration
	MaxFutureSkew              time.Duration
	ConvergenceWindow          time.Duration
	MaxWaiters                 int
	MaxProviderLoads           int
	MaxConcurrentProviderLoads int
	MaxInvalidationStreams     int
	MaxPolicies                int
	AllowEmptyBootstrap        bool
	AllowStaleBootstrap        bool
	Policies                   map[string]FlagPolicy
}

// FleetState is the low-cardinality lifecycle state of one tenant fleet.
type FleetState string

const (
	FleetCold     FleetState = "cold"
	FleetReady    FleetState = "ready"
	FleetDegraded FleetState = "degraded"
	FleetStopped  FleetState = "stopped"
)

// ActiveSnapshot is an immutable snapshot plus explicit age, revision, and
// provenance metadata.
type ActiveSnapshot struct {
	Snapshot    Snapshot
	Revision    string
	Provenance  string
	SourceTime  time.Time
	ActivatedAt time.Time
}

// Age reports non-negative source age and tolerates wall-clock rollback.
func (snapshot ActiveSnapshot) Age(now time.Time) time.Duration {
	return boundedAge(now, snapshot.SourceTime)
}

// FleetFailureCode is a bounded low-cardinality operational failure.
type FleetFailureCode string

const (
	FleetFailureNone            FleetFailureCode = ""
	FleetFailureProvider        FleetFailureCode = "provider_failure"
	FleetFailureInvalidSnapshot FleetFailureCode = "invalid_snapshot"
	FleetFailureStaleSnapshot   FleetFailureCode = "stale_snapshot"
	FleetFailureLoadLimit       FleetFailureCode = "provider_load_limit"
	FleetFailureWaiterLimit     FleetFailureCode = "waiter_limit"
	FleetFailureCancelled       FleetFailureCode = "cancelled"
	FleetFailureStopped         FleetFailureCode = "stopped"
	FleetFailureScheduler       FleetFailureCode = "scheduler_failure"
	FleetFailureCacheLoad       FleetFailureCode = "cache_load_failure"
	FleetFailureCacheStore      FleetFailureCode = "cache_store_failure"
	FleetFailureRetryExhausted  FleetFailureCode = "retry_exhausted"
	FleetFailureCircuitOpen     FleetFailureCode = "circuit_open"
	FleetFailureBulkhead        FleetFailureCode = "bulkhead_rejected"
	FleetFailureThrottled       FleetFailureCode = "throttled"
	FleetFailureConcurrency     FleetFailureCode = "concurrency_rejected"
	FleetFailureBudgetExhausted FleetFailureCode = "budget_exhausted"
)

// FleetStatus is a bounded observable view. It never retains provider errors,
// flag values, or evaluation context.
type FleetStatus struct {
	State               FleetState
	Revision            string
	Provenance          string
	Age                 time.Duration
	ProviderLoads       uint64
	LastRefreshFailure  FleetFailureCode
	LastCacheFailure    FleetFailureCode
	InvalidationGaps    uint64
	ConvergenceDeadline time.Time
	ConvergenceBreached bool
	Refreshing          bool
	RefreshWaiters      int
}

// Invalidation is a tenant- and stream-scoped causal refresh hint.
type Invalidation struct {
	Tenant     string
	Stream     string
	Sequence   uint64
	Revision   string
	ObservedAt time.Time
}

// InvalidationDisposition classifies an invalidation delivery.
type InvalidationDisposition string

const (
	InvalidationCurrent   InvalidationDisposition = "current"
	InvalidationDuplicate InvalidationDisposition = "duplicate"
	InvalidationReordered InvalidationDisposition = "reordered"
	InvalidationRefreshed InvalidationDisposition = "refreshed"
	InvalidationPending   InvalidationDisposition = "pending"
)

// InvalidationResult reports causal and bounded-convergence state.
type InvalidationResult struct {
	Disposition         InvalidationDisposition
	Gap                 bool
	PreviousSequence    uint64
	ActiveRevision      string
	Delay               time.Duration
	ConvergenceDeadline time.Time
}

type invalidationStream struct{ last uint64 }

// RefreshDisposition identifies whether a refresh activated provider state or
// was bounded without another provider load.
type RefreshDisposition string

const (
	RefreshActivated RefreshDisposition = "activated"
	RefreshUnchanged RefreshDisposition = "unchanged"
	RefreshDeferred  RefreshDisposition = "deferred"
)

// RefreshResult is the active state after one refresh request.
type RefreshResult struct {
	Active      ActiveSnapshot
	Disposition RefreshDisposition
	Coalesced   bool
}

type refreshFlight struct {
	done   chan struct{}
	result RefreshResult
	err    error
}

// Fleet owns one tenant's atomic active snapshot and refresh lifecycle.
type Fleet struct {
	config FleetConfig

	clockMu             sync.Mutex
	lastObservedAt      time.Time
	mu                  sync.RWMutex
	active              ActiveSnapshot
	hasActive           bool
	state               FleetState
	lastRefreshFailure  FleetFailureCode
	lastCacheFailure    FleetFailureCode
	providerLoads       atomic.Uint64
	refresh             *refreshFlight
	waiters             int
	lastRefreshAt       time.Time
	closed              bool
	refreshWG           sync.WaitGroup
	invalidations       map[string]invalidationStream
	invalidationGaps    uint64
	convergence         time.Time
	convergenceRevision string
	lifecycleCtx        context.Context
	lifecycleCancel     context.CancelFunc
	runCancel           context.CancelFunc
	runWG               sync.WaitGroup
	started             bool
	starting            bool
	loadSlots           chan struct{}
	loadWG              sync.WaitGroup
	bootstrapWG         sync.WaitGroup
	shutdownOnce        sync.Once
	shutdownDone        chan struct{}
}

// NewFleet validates all bounds and copies per-flag policy state.
func NewFleet(config FleetConfig) (*Fleet, error) {
	if config.Tenant == "" || cmp.Compare(len(config.Tenant), maxFleetIdentityLength) == 1 {
		return nil, fmt.Errorf("fleet tenant must be non-empty and at most %d bytes", maxFleetIdentityLength)
	}
	if config.ReplicaID == "" || cmp.Compare(len(config.ReplicaID), maxFleetIdentityLength) == 1 {
		return nil, fmt.Errorf("fleet replica id must be non-empty and at most %d bytes", maxFleetIdentityLength)
	}
	if config.Loader == nil || config.Clock == nil {
		return nil, fmt.Errorf("fleet loader and clock are required")
	}
	if config.Sleeper == nil {
		config.Sleeper = systemFleetSleeper{}
	}
	if config.Jitter == nil {
		config.Jitter = DeterministicFleetJitter{}
	}
	if cmp.Compare(config.RefreshInterval, time.Duration(0)) != 1 {
		return nil, fmt.Errorf("fleet time bounds are invalid")
	}
	if cmp.Compare(config.MinRefreshInterval, time.Duration(0)) != 1 {
		return nil, fmt.Errorf("fleet time bounds are invalid")
	}
	if cmp.Compare(config.MinRefreshInterval, config.RefreshInterval) == 1 {
		return nil, fmt.Errorf("fleet time bounds are invalid")
	}
	if cmp.Compare(config.MaxRefreshJitter, time.Duration(0)) == -1 {
		return nil, fmt.Errorf("fleet time bounds are invalid")
	}
	if cmp.Compare(config.MaxFutureSkew, time.Duration(0)) == -1 {
		return nil, fmt.Errorf("fleet time bounds are invalid")
	}
	if cmp.Compare(config.LoadTimeout, time.Duration(0)) != 1 {
		return nil, fmt.Errorf("fleet time bounds are invalid")
	}
	if cmp.Compare(config.FreshFor, time.Duration(0)) != 1 {
		return nil, fmt.Errorf("fleet time bounds are invalid")
	}
	if cmp.Compare(config.MaxStaleness, config.FreshFor) == -1 {
		return nil, fmt.Errorf("fleet time bounds are invalid")
	}
	if cmp.Compare(config.MaxRefreshJitter, time.Duration(math.MaxInt64)-config.RefreshInterval) == 1 {
		return nil, fmt.Errorf("fleet refresh bound overflows time.Duration")
	}
	refreshBound := config.RefreshInterval + config.MaxRefreshJitter
	if cmp.Compare(config.LoadTimeout, time.Duration(math.MaxInt64)-refreshBound) == 1 {
		return nil, fmt.Errorf("fleet refresh bound overflows time.Duration")
	}
	refreshBound += config.LoadTimeout
	if cmp.Compare(config.ConvergenceWindow, refreshBound) == -1 {
		return nil, fmt.Errorf("fleet convergence window is shorter than the refresh bound")
	}
	if cmp.Compare(config.MaxWaiters, 0) != 1 || cmp.Compare(config.MaxProviderLoads, 0) != 1 ||
		cmp.Compare(config.MaxConcurrentProviderLoads, 0) != 1 || cmp.Compare(config.MaxInvalidationStreams, 0) != 1 ||
		cmp.Compare(config.MaxPolicies, 0) != 1 {
		return nil, fmt.Errorf("fleet resource bounds must be positive")
	}
	if cmp.Compare(config.MaxWaiters, maxFleetWaiters) == 1 || cmp.Compare(config.MaxProviderLoads, maxFleetProviderLoads) == 1 ||
		cmp.Compare(config.MaxConcurrentProviderLoads, maxFleetConcurrentProviderLoads) == 1 ||
		cmp.Compare(config.MaxInvalidationStreams, maxFleetInvalidationStreams) == 1 ||
		cmp.Compare(config.MaxPolicies, DefaultLimits().MaxFeatures) == 1 {
		return nil, fmt.Errorf("fleet resource bounds exceed supported maxima")
	}
	if cmp.Compare(len(config.Policies), config.MaxPolicies) == 1 {
		return nil, fmt.Errorf("fleet policy count exceeds %d", config.MaxPolicies)
	}
	policies := make(map[string]FlagPolicy, len(config.Policies))
	for key, policy := range config.Policies {
		if key == "" || cmp.Compare(len(key), DefaultLimits().MaxKeyBytes) == 1 {
			return nil, fmt.Errorf("fleet policy feature key is invalid")
		}
		if policy.Mode < DegradedFailClosed || policy.Mode > DegradedLastKnownGood {
			return nil, fmt.Errorf("fleet policy for %q has an invalid degraded mode", key)
		}
		if policy.SecuritySensitive && (policy.Mode == DegradedFailOpen || policy.Mode == DegradedDefault) {
			return nil, fmt.Errorf("%w: %s", ErrUnsafeFlagPolicy, key)
		}
		if policy.Mode == DegradedLastKnownGood {
			if policy.MaxStaleness <= 0 {
				policy.MaxStaleness = config.MaxStaleness
			}
			if policy.MaxStaleness > config.MaxStaleness {
				return nil, fmt.Errorf("fleet policy for %q exceeds maximum staleness", key)
			}
		}
		policies[key] = policy
	}
	config.Policies = policies

	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	return &Fleet{
		config: config, state: FleetCold,
		invalidations: make(map[string]invalidationStream),
		lifecycleCtx:  lifecycleCtx, lifecycleCancel: lifecycleCancel,
		loadSlots: make(chan struct{}, config.MaxConcurrentProviderLoads), shutdownDone: make(chan struct{}),
	}, nil
}

// Bootstrap synchronously loads and validates the primary provider, then
// falls back to a bounded cached last-known-good candidate when configured.
func (fleet *Fleet) Bootstrap(ctx context.Context) (ActiveSnapshot, error) {
	if ctx == nil {
		return ActiveSnapshot{}, fmt.Errorf("bootstrap context is required")
	}
	fleet.mu.Lock()
	if fleet.closed {
		fleet.mu.Unlock()
		return ActiveSnapshot{}, ErrFleetStopped
	}
	fleet.bootstrapWG.Add(1)
	fleet.mu.Unlock()
	defer fleet.bootstrapWG.Done()

	bootstrapCtx, cancel := context.WithCancel(ctx)
	stopLifecycle := context.AfterFunc(fleet.lifecycleCtx, cancel)
	defer func() {
		stopLifecycle()
		cancel()
	}()
	candidate, primaryErr := fleet.load(bootstrapCtx)
	if primaryErr == nil {
		active, err := fleet.activate(candidate, fleet.config.AllowStaleBootstrap)
		if err == nil {
			if fleet.config.Cache != nil {
				cacheCtx, cacheCancel := fleet.boundedContext(bootstrapCtx)
				storeErr := fleet.config.Cache.Store(cacheCtx, fleet.config.Tenant, candidate)
				cacheCancel()
				if storeErr != nil {
					fleet.setCacheFailure(FleetFailureCacheStore)
				} else {
					fleet.setCacheFailure(FleetFailureNone)
				}
			}
			return active, nil
		}
		primaryErr = err
	}
	primaryFailure := fleet.classifyRefreshFailure(primaryErr)

	if fleet.config.Cache != nil {
		cacheCtx, cacheCancel := fleet.boundedContext(bootstrapCtx)
		cached, found, cacheErr := fleet.config.Cache.Load(cacheCtx, fleet.config.Tenant)
		cacheCancel()
		if cacheErr != nil {
			fleet.setCacheFailure(FleetFailureCacheLoad)
		} else {
			fleet.setCacheFailure(FleetFailureNone)
			if found {
				active, err := fleet.activate(cached, fleet.config.AllowStaleBootstrap)
				if err == nil {
					fleet.mu.Lock()
					fleet.lastRefreshFailure = primaryFailure
					fleet.mu.Unlock()
					return active, nil
				}
				cacheErr = err
				fleet.setCacheFailure(classifyFleetFailure(cacheErr))
			}
		}
		if cacheErr != nil {
			primaryErr = errors.Join(primaryErr, cacheErr)
		}
	}
	fleet.mu.Lock()
	fleet.lastRefreshFailure = primaryFailure
	fleet.mu.Unlock()
	return ActiveSnapshot{}, errors.Join(ErrNoUsableSnapshot, primaryErr)
}

func (fleet *Fleet) load(ctx context.Context) (SnapshotCandidate, error) {
	fleet.mu.Lock()
	if fleet.closed {
		fleet.mu.Unlock()
		return SnapshotCandidate{}, ErrFleetStopped
	}
	fleet.loadWG.Add(1)
	fleet.mu.Unlock()
	defer fleet.loadWG.Done()
	loadCtx, cancel := fleet.boundedContext(ctx)
	defer cancel()
	var calls atomic.Uint64
	operation := func(operationCtx context.Context) (SnapshotCandidate, error) {
		if calls.Add(1) > uint64(fleet.config.MaxProviderLoads) {
			return SnapshotCandidate{}, ErrRefreshLoadLimit
		}
		if operationCtx == nil {
			return SnapshotCandidate{}, fmt.Errorf("refresh operation context is required")
		}
		providerCtx, providerCancel := context.WithCancel(loadCtx)
		stopOperation := context.AfterFunc(operationCtx, providerCancel)
		defer func() {
			stopOperation()
			providerCancel()
		}()
		select {
		case fleet.loadSlots <- struct{}{}:
			defer func() { <-fleet.loadSlots }()
		case <-providerCtx.Done():
			return SnapshotCandidate{}, providerCtx.Err()
		}
		if err := operationCtx.Err(); err != nil {
			return SnapshotCandidate{}, err
		}
		incrementSaturating(&fleet.providerLoads)
		return fleet.config.Loader.Load(providerCtx, fleet.config.Tenant)
	}
	if fleet.config.Executor != nil {
		return fleet.config.Executor.Execute(loadCtx, operation)
	}
	return operation(loadCtx)
}

// Start bootstraps synchronously, then owns exactly one periodic refresher.
// The context controls both startup and the refresher lifetime; cancellation
// stops the fleet.
func (fleet *Fleet) Start(ctx context.Context) (ActiveSnapshot, error) {
	if ctx == nil {
		return ActiveSnapshot{}, fmt.Errorf("start context is required")
	}
	fleet.mu.Lock()
	if fleet.closed {
		fleet.mu.Unlock()
		return ActiveSnapshot{}, ErrFleetStopped
	}
	if fleet.started || fleet.starting {
		fleet.mu.Unlock()
		return ActiveSnapshot{}, fmt.Errorf("feature flag fleet already started")
	}
	fleet.starting = true
	fleet.mu.Unlock()
	active, err := fleet.Bootstrap(ctx)
	fleet.mu.Lock()
	fleet.starting = false
	if err != nil {
		fleet.mu.Unlock()
		return ActiveSnapshot{}, err
	}
	if fleet.closed {
		fleet.mu.Unlock()
		return ActiveSnapshot{}, ErrFleetStopped
	}
	runCtx, cancel := context.WithCancel(ctx)
	fleet.runCancel = cancel
	fleet.started = true
	fleet.runWG.Add(1)
	fleet.mu.Unlock()
	go fleet.refreshLoop(runCtx)
	return active, nil
}

func (fleet *Fleet) refreshLoop(ctx context.Context) {
	defer fleet.runWG.Done()
	for sequence := uint64(1); ; sequence++ {
		jitter, err := fleet.config.Jitter.Delay(fleet.config.ReplicaID, sequence, fleet.config.MaxRefreshJitter)
		if err != nil || cmp.Compare(jitter, time.Duration(0)) == -1 ||
			cmp.Compare(jitter, fleet.config.MaxRefreshJitter) == 1 {
			fleet.recordRefreshFailure(FleetFailureScheduler)
			fleet.markStopped()
			return
		}
		if err := fleet.config.Sleeper.Sleep(ctx, fleet.config.RefreshInterval+jitter); err != nil {
			if ctx.Err() == nil {
				fleet.recordRefreshFailure(FleetFailureScheduler)
			}
			fleet.markStopped()
			return
		}
		if _, err := fleet.Refresh(ctx); errors.Is(err, ErrRefreshWaiterLimit) {
			fleet.recordRefreshFailure(fleet.classifyRefreshFailure(err))
		}
	}
}

func (fleet *Fleet) recordRefreshFailure(failure FleetFailureCode) {
	now := fleet.now()
	fleet.mu.Lock()
	fleet.lastRefreshFailure = failure
	fleet.degradeIfStaleLocked(now)
	fleet.mu.Unlock()
}

func (fleet *Fleet) degradeIfStaleLocked(now time.Time) {
	if fleet.closed {
		return
	}
	if !fleet.hasActive {
		return
	}
	if cmp.Compare(fleet.active.Age(now), fleet.config.FreshFor) == 1 {
		fleet.state = FleetDegraded
	}
}

func classifyFleetFailure(err error) FleetFailureCode {
	switch {
	case err == nil:
		return FleetFailureNone
	case errors.Is(err, ErrMalformedSnapshot), errors.Is(err, ErrNoUsableSnapshot), errors.Is(err, ErrSnapshotFuture),
		errors.Is(err, ErrSnapshotReordered):
		return FleetFailureInvalidSnapshot
	case errors.Is(err, ErrSnapshotStale):
		return FleetFailureStaleSnapshot
	case errors.Is(err, ErrRefreshLoadLimit):
		return FleetFailureLoadLimit
	case errors.Is(err, ErrRefreshWaiterLimit):
		return FleetFailureWaiterLimit
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return FleetFailureCancelled
	case errors.Is(err, ErrFleetStopped):
		return FleetFailureStopped
	default:
		return FleetFailureProvider
	}
}

func (fleet *Fleet) classifyRefreshFailure(err error) FleetFailureCode {
	builtIn := classifyFleetFailure(err)
	if builtIn != FleetFailureProvider || fleet.config.FailureClassifier == nil {
		return builtIn
	}
	classified := fleet.config.FailureClassifier.Classify(err)
	switch classified {
	case FleetFailureProvider, FleetFailureRetryExhausted, FleetFailureCircuitOpen,
		FleetFailureBulkhead, FleetFailureThrottled, FleetFailureConcurrency,
		FleetFailureBudgetExhausted:
		return classified
	default:
		return FleetFailureProvider
	}
}

func (fleet *Fleet) markStopped() {
	fleet.mu.Lock()
	if fleet.closed {
		fleet.mu.Unlock()
		return
	}
	fleet.closed = true
	fleet.state = FleetStopped
	runCancel := fleet.runCancel
	lifecycleCancel := fleet.lifecycleCancel
	fleet.mu.Unlock()
	if runCancel != nil {
		runCancel()
	}
	lifecycleCancel()
}

// Shutdown cancels provider work, stops the refresher, and joins all refresh
// calls without closing caller-owned providers or caches.
func (fleet *Fleet) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("shutdown context is required")
	}
	fleet.markStopped()
	fleet.shutdownOnce.Do(func() {
		go func() {
			fleet.runWG.Wait()
			fleet.refreshWG.Wait()
			fleet.loadWG.Wait()
			fleet.bootstrapWG.Wait()
			close(fleet.shutdownDone)
		}()
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-fleet.shutdownDone:
		return nil
	}
}

func (fleet *Fleet) activate(candidate SnapshotCandidate, allowStale bool) (ActiveSnapshot, error) {
	now := fleet.now()
	validated, err := fleet.validateCandidate(candidate, now, allowStale)
	if err != nil {
		return ActiveSnapshot{}, err
	}
	active := ActiveSnapshot{
		Snapshot: validated, Revision: candidate.Revision,
		Provenance: candidate.Provenance, SourceTime: candidate.SourceTime.Round(0),
		ActivatedAt: now.Round(0),
	}
	fleet.mu.Lock()
	if fleet.closed {
		fleet.mu.Unlock()
		return ActiveSnapshot{}, ErrFleetStopped
	}
	if fleet.hasActive && candidate.SourceTime.Before(fleet.active.SourceTime) {
		fleet.mu.Unlock()
		return ActiveSnapshot{}, ErrSnapshotReordered
	}
	fleet.active = active
	fleet.hasActive = true
	fleet.lastRefreshFailure = FleetFailureNone
	fleet.state = FleetReady
	if cmp.Compare(active.Age(now), fleet.config.FreshFor) == 1 {
		fleet.state = FleetDegraded
	}
	fleet.mu.Unlock()
	return active, nil
}

// Refresh coalesces concurrent callers, bounds waiters and provider attempts,
// and retains the current snapshot until a complete replacement validates.
func (fleet *Fleet) Refresh(ctx context.Context) (RefreshResult, error) {
	if ctx == nil {
		return RefreshResult{}, fmt.Errorf("refresh context is required")
	}
	now := fleet.now()
	fleet.mu.Lock()
	if fleet.closed {
		fleet.mu.Unlock()
		return RefreshResult{}, ErrFleetStopped
	}
	if flight := fleet.refresh; flight != nil {
		if fleet.waiters >= fleet.config.MaxWaiters {
			fleet.mu.Unlock()
			return RefreshResult{}, ErrRefreshWaiterLimit
		}
		fleet.waiters++
		fleet.mu.Unlock()
		select {
		case <-ctx.Done():
			fleet.mu.Lock()
			fleet.waiters--
			fleet.mu.Unlock()
			return RefreshResult{}, ctx.Err()
		case <-flight.done:
			fleet.mu.Lock()
			fleet.waiters--
			fleet.mu.Unlock()
			result := flight.result
			result.Coalesced = true
			return result, flight.err
		}
	}
	if !fleet.lastRefreshAt.IsZero() && boundedAge(now, fleet.lastRefreshAt) < fleet.config.MinRefreshInterval {
		active := fleet.active
		fleet.mu.Unlock()
		return RefreshResult{Active: active, Disposition: RefreshDeferred}, nil
	}
	flight := &refreshFlight{done: make(chan struct{})}
	fleet.refresh = flight
	fleet.lastRefreshAt = now
	fleet.refreshWG.Add(1)
	fleet.mu.Unlock()

	result, err := fleet.performRefresh(ctx)
	completedAt := fleet.now()
	failure := fleet.classifyRefreshFailure(err)
	fleet.mu.Lock()
	if err != nil {
		intentionalStop := false
		if fleet.closed {
			intentionalStop = errors.Is(err, context.Canceled) || errors.Is(err, ErrFleetStopped)
		}
		if !intentionalStop {
			fleet.lastRefreshFailure = failure
		}
		fleet.degradeIfStaleLocked(completedAt)
	}
	flight.result = result
	flight.err = err
	fleet.refresh = nil
	close(flight.done)
	fleet.mu.Unlock()
	fleet.refreshWG.Done()
	return result, err
}

func (fleet *Fleet) performRefresh(ctx context.Context) (RefreshResult, error) {
	previous, existed := fleet.Current()
	candidate, err := fleet.load(ctx)
	if err != nil {
		return RefreshResult{Active: previous, Disposition: RefreshUnchanged}, err
	}
	active, err := fleet.activate(candidate, false)
	if err != nil {
		return RefreshResult{Active: previous, Disposition: RefreshUnchanged}, err
	}
	disposition := RefreshActivated
	if existed && previous.Revision == active.Revision {
		disposition = RefreshUnchanged
	}
	if fleet.config.Cache != nil {
		cacheCtx, cacheCancel := fleet.boundedContext(ctx)
		err := fleet.config.Cache.Store(cacheCtx, fleet.config.Tenant, candidate)
		cacheCancel()
		if err != nil {
			fleet.setCacheFailure(FleetFailureCacheStore)
		} else {
			fleet.setCacheFailure(FleetFailureNone)
		}
	}
	return RefreshResult{Active: active, Disposition: disposition}, nil
}

func (fleet *Fleet) setCacheFailure(failure FleetFailureCode) {
	fleet.mu.Lock()
	fleet.lastCacheFailure = failure
	fleet.mu.Unlock()
}

func (fleet *Fleet) boundedContext(parent context.Context) (context.Context, context.CancelFunc) {
	bounded, cancel := context.WithTimeout(parent, fleet.config.LoadTimeout)
	stopLifecycle := context.AfterFunc(fleet.lifecycleCtx, cancel)
	return bounded, func() {
		stopLifecycle()
		cancel()
	}
}

func (fleet *Fleet) validateCandidate(candidate SnapshotCandidate, now time.Time, allowStale bool) (Snapshot, error) {
	if candidate.Revision == "" || cmp.Compare(len(candidate.Revision), maxFleetIdentityLength) == 1 {
		return Snapshot{}, ErrMalformedSnapshot
	}
	if candidate.Provenance == "" || cmp.Compare(len(candidate.Provenance), maxFleetIdentityLength) == 1 {
		return Snapshot{}, ErrMalformedSnapshot
	}
	if candidate.SourceTime.IsZero() || candidate.Snapshot.definitions == nil || candidate.Snapshot.groups == nil {
		return Snapshot{}, ErrMalformedSnapshot
	}
	if candidate.Snapshot.tenant != fleet.config.Tenant {
		return Snapshot{}, ErrMalformedSnapshot
	}
	if !fleet.config.AllowEmptyBootstrap && len(candidate.Snapshot.definitions) == 0 {
		return Snapshot{}, ErrNoUsableSnapshot
	}
	if candidate.SourceTime.After(now.Add(fleet.config.MaxFutureSkew)) {
		return Snapshot{}, ErrSnapshotFuture
	}
	age := boundedAge(now, candidate.SourceTime)
	if age > fleet.config.MaxStaleness || (!allowStale && age > fleet.config.FreshFor) {
		return Snapshot{}, ErrSnapshotStale
	}
	definitions := make([]Definition, 0, len(candidate.Snapshot.definitions))
	for _, definition := range candidate.Snapshot.definitions {
		definitions = append(definitions, definition)
	}
	groups := make([]GroupDefinition, 0, len(candidate.Snapshot.groups))
	for _, group := range candidate.Snapshot.groups {
		groups = append(groups, group)
	}
	validated, err := NewSnapshotWithGroups(definitions, groups, candidate.Snapshot.limits)
	if err != nil {
		return Snapshot{}, errors.Join(ErrMalformedSnapshot, err)
	}
	return validated.bindTenant(fleet.config.Tenant), nil
}

// Current returns a copy of the current immutable active snapshot.
func (fleet *Fleet) Current() (ActiveSnapshot, bool) {
	fleet.mu.RLock()
	defer fleet.mu.RUnlock()
	return fleet.active, fleet.hasActive
}

// Status returns fresh age and lifecycle metadata.
func (fleet *Fleet) Status() FleetStatus {
	now := fleet.now()
	fleet.mu.RLock()
	status := FleetStatus{
		State: fleet.state, Revision: fleet.active.Revision,
		Provenance: fleet.active.Provenance, LastRefreshFailure: fleet.lastRefreshFailure,
		LastCacheFailure: fleet.lastCacheFailure,
		ProviderLoads:    fleet.providerLoads.Load(), InvalidationGaps: fleet.invalidationGaps,
		ConvergenceDeadline: fleet.convergence, Refreshing: fleet.refresh != nil,
		RefreshWaiters: fleet.waiters,
	}
	if fleet.hasActive {
		status.Age = fleet.active.Age(now)
		if status.State != FleetStopped && cmp.Compare(status.Age, fleet.config.FreshFor) == 1 {
			status.State = FleetDegraded
		}
	}
	if !status.ConvergenceDeadline.IsZero() && now.After(status.ConvergenceDeadline) {
		status.ConvergenceBreached = !fleet.hasActive || fleet.active.Revision != fleet.convergenceRevision
	}
	fleet.mu.RUnlock()
	return status
}

// Invalidate deterministically classifies duplicate, reordered, delayed, lost,
// and cross-revision events. It performs at most one coalesced refresh.
func (fleet *Fleet) Invalidate(ctx context.Context, event Invalidation) (InvalidationResult, error) {
	if ctx == nil {
		return InvalidationResult{}, fmt.Errorf("invalidation context is required")
	}
	now := fleet.now()
	if event.Tenant != fleet.config.Tenant || event.Stream == "" ||
		cmp.Compare(len(event.Stream), maxFleetIdentityLength) == 1 || event.Sequence == 0 ||
		event.Revision == "" || cmp.Compare(len(event.Revision), maxFleetIdentityLength) == 1 || event.ObservedAt.IsZero() {
		return InvalidationResult{}, ErrInvalidInvalidation
	}
	fleet.mu.Lock()
	stream, exists := fleet.invalidations[event.Stream]
	if !exists && len(fleet.invalidations) >= fleet.config.MaxInvalidationStreams {
		fleet.mu.Unlock()
		return InvalidationResult{}, ErrInvalidationStreams
	}
	result := InvalidationResult{
		PreviousSequence: stream.last,
		Delay:            boundedAge(now, event.ObservedAt),
		ActiveRevision:   fleet.active.Revision,
	}
	switch cmp.Compare(event.Sequence, stream.last) {
	case 0:
		result.Disposition = InvalidationDuplicate
		fleet.mu.Unlock()
		return result, nil
	case -1:
		result.Disposition = InvalidationReordered
		fleet.mu.Unlock()
		return result, nil
	}
	if stream.last == 0 {
		result.Gap = cmp.Compare(event.Sequence, uint64(1)) == 1
	} else {
		result.Gap = cmp.Compare(event.Sequence, stream.last+1) == 1
	}
	stream.last = event.Sequence
	fleet.invalidations[event.Stream] = stream
	if result.Gap {
		if fleet.invalidationGaps < math.MaxUint64 {
			fleet.invalidationGaps++
		}
	}
	if !result.Gap && fleet.hasActive && event.Revision == fleet.active.Revision {
		result.Disposition = InvalidationCurrent
		fleet.mu.Unlock()
		return result, nil
	}
	fleet.convergence = now.Add(fleet.config.ConvergenceWindow).Round(0)
	fleet.convergenceRevision = event.Revision
	result.ConvergenceDeadline = fleet.convergence
	fleet.mu.Unlock()

	refresh, err := fleet.Refresh(ctx)
	result.ActiveRevision = refresh.Active.Revision
	switch {
	case err != nil:
		result.Disposition = InvalidationPending
		return result, err
	case refresh.Disposition == RefreshDeferred:
		result.Disposition = InvalidationPending
		return result, nil
	case result.ActiveRevision != event.Revision:
		result.Disposition = InvalidationPending
		return result, nil
	}
	result.Disposition = InvalidationRefreshed
	return result, nil
}

// Boolean evaluates from the immutable current snapshot or applies the
// explicitly configured degraded policy for this flag.
func (fleet *Fleet) Boolean(key string, evaluationContext Context) (Detail[bool], error) {
	result, err := fleet.evaluate(key, TypeBoolean, evaluationContext)
	if err != nil {
		return Detail[bool]{}, err
	}
	value, _ := result.value.booleanValue()
	return detailOf(value, result), nil
}

// String evaluates a native string flag under the same fleet policy.
func (fleet *Fleet) String(key string, evaluationContext Context) (Detail[string], error) {
	result, err := fleet.evaluate(key, TypeString, evaluationContext)
	if err != nil {
		return Detail[string]{}, err
	}
	value, _ := result.value.stringValue(TypeString)
	return detailOf(value, result), nil
}

// Integer evaluates a native integer flag under the same fleet policy.
func (fleet *Fleet) Integer(key string, evaluationContext Context) (Detail[int64], error) {
	result, err := fleet.evaluate(key, TypeInteger, evaluationContext)
	if err != nil {
		return Detail[int64]{}, err
	}
	value, _ := result.value.integerValue()
	return detailOf(value, result), nil
}

// Float evaluates a native float flag under the same fleet policy.
func (fleet *Fleet) Float(key string, evaluationContext Context) (Detail[float64], error) {
	result, err := fleet.evaluate(key, TypeFloat, evaluationContext)
	if err != nil {
		return Detail[float64]{}, err
	}
	value, _ := result.value.floatValue()
	return detailOf(value, result), nil
}

// Decimal evaluates a native decimal flag under the same fleet policy.
func (fleet *Fleet) Decimal(key string, evaluationContext Context) (Detail[string], error) {
	result, err := fleet.evaluate(key, TypeDecimal, evaluationContext)
	if err != nil {
		return Detail[string]{}, err
	}
	value, _ := result.value.stringValue(TypeDecimal)
	return detailOf(value, result), nil
}

// Structured evaluates a native structured flag and returns owned bytes.
func (fleet *Fleet) Structured(key string, evaluationContext Context) (Detail[json.RawMessage], error) {
	result, err := fleet.evaluate(key, TypeStructured, evaluationContext)
	if err != nil {
		return Detail[json.RawMessage]{}, err
	}
	value, _ := result.value.structuredValue()
	return detailOf(value, result), nil
}

func (fleet *Fleet) evaluate(key string, expected Type, evaluationContext Context) (evaluationResult, error) {
	if evaluationContext.Tenant != fleet.config.Tenant {
		return evaluationResult{}, fmt.Errorf("fleet tenant %q: %w", fleet.config.Tenant, ErrTenantMismatch)
	}
	active, exists := fleet.Current()
	limits := DefaultLimits()
	if exists {
		limits = active.Snapshot.limits
	}
	if err := evaluationContext.validate(limits); err != nil {
		return evaluationResult{}, err
	}
	if exists && cmp.Compare(active.Age(fleet.now()), fleet.config.FreshFor) != 1 {
		return active.Snapshot.evaluate(key, expected, evaluationContext)
	}
	policy, configured := fleet.config.Policies[key]
	if !configured {
		return evaluationResult{}, ErrSnapshotStale
	}
	if policy.Mode == DegradedFailClosed {
		return evaluationResult{}, ErrSnapshotStale
	}
	switch policy.Mode {
	case DegradedFailOpen:
		if expected != TypeBoolean {
			return evaluationResult{}, ErrInvalidValue
		}
		return evaluationResult{value: BooleanValue(true), reason: ReasonDegradedFailOpen}, nil
	case DegradedDefault:
		if policy.Default.Type() != expected {
			return evaluationResult{}, ErrInvalidValue
		}
		if err := policy.Default.validate(limits); err != nil {
			return evaluationResult{}, err
		}
		return evaluationResult{value: policy.Default.clone(), reason: ReasonDegradedDefault}, nil
	case DegradedLastKnownGood:
		if !exists || cmp.Compare(active.Age(fleet.now()), policy.MaxStaleness) == 1 {
			return evaluationResult{}, ErrSnapshotStale
		}
		result, err := active.Snapshot.evaluate(key, expected, evaluationContext)
		if err != nil {
			return evaluationResult{}, err
		}
		result.reason = ReasonDegradedLastKnownGood
		return result, nil
	default:
		return evaluationResult{}, ErrSnapshotStale
	}
}

func (fleet *Fleet) now() time.Time {
	observed := fleet.config.Clock.Now().Round(0)
	fleet.clockMu.Lock()
	if observed.Before(fleet.lastObservedAt) {
		observed = fleet.lastObservedAt
	} else {
		fleet.lastObservedAt = observed
	}
	fleet.clockMu.Unlock()
	return observed
}

func incrementSaturating(counter *atomic.Uint64) {
	for {
		current := counter.Load()
		if current == math.MaxUint64 || counter.CompareAndSwap(current, current+1) {
			return
		}
	}
}
