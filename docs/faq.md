# FAQ

## Can a flag authorize an operation?

No. Flags control product rollout. Use an authorization system for access
decisions and enforce it independently of flag state.

## Why is time explicit?

It makes schedules reproducible and avoids hidden clock, timezone, and test
dependencies.

## Why does a snapshot reject another tenant?

It prevents a caller from accidentally evaluating one tenant's definitions
with another tenant's context.

## What happens during provider failure?

The provider returns an error. A configured cache may return a bounded stale
snapshot only under `FailOpen`; `FailClosed` preserves the error.

## Can custom strategies be exported?

Not by default. Portable documents support only built-in deterministic
strategies. An unsupported custom strategy returns `ErrUnsupportedStrategy`.

## Does scheduled activation start a goroutine?

No. The application calls `ApplyScheduled` and owns scheduling, cancellation,
and shutdown.

## How should context privacy be handled?

Prefer opaque IDs and coarse attributes. Do not include secrets or unnecessary
personal data. The package bounds and avoids logging context, but the caller
still owns data minimization.
