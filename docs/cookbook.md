# Cookbook

## Stable progressive rollout

Create one `PercentageStrategy` with a permanent seed and raise only its
threshold. Changing the seed reshuffles assignments. Evaluate with a stable,
opaque subject and the correct tenant.

## Tenant allow list with emergency deny

Use `SetStrategy`. Deny entries take precedence over allow entries. Keep list
sizes within limits; large dynamic audiences belong in an application-owned
deterministic custom strategy or a precomputed typed fact.

## Weekly regional release

Use `ScheduleStrategy` with an IANA timezone and pass the request's explicit
decision time in `Context.Time`. Test daylight-saving transitions for the
chosen location. Do not rely on the machine timezone.

## Request-consistent evaluation

Acquire one tenant snapshot at request entry, then reuse it for every flag and
batch. Do not reacquire snapshots between prerequisite and dependent reads.

## Scheduled activation

Stage the full replacement definition with its expected version. An
application-owned scheduler calls `ApplyScheduled(ctx, tenant, now, actor)`.
On shutdown, cancel and join that scheduler before closing the provider.

## Provider conformance

Custom durable backends implement `DocumentBackend`, then wrap it with
`NewDurableProvider`. Run `featureflagstest.RunProvider` against isolated state
to prove management semantics.
