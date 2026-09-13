# Security model

Version: 1.0 (2026-09-13). Owner: go-feature-flags maintainers.

Feature definitions, imported tenant documents, evaluation contexts, and
OpenFeature flattened contexts are attacker-controlled when applications pass
request data through to this module. Provider implementations, clocks,
watchers, and hooks supplied by application code are trusted in-process
collaborators. A feature decision is product policy, not an authorization
result.

## Enforced boundaries

- Every native management and snapshot operation rejects an empty or
  over-limit tenant before retaining it or calling a durable backend.
- Evaluation validates tenant binding, context counts, key and value sizes,
  structured JSON size, dependency depth, batch size, and diagnostic output.
- The OpenFeature adapter validates its fixed tenant and flattened context
  before requesting a native snapshot. Structured values receive bounded
  depth, node, and encoded-size preflight before JSON encoding; cycles,
  unsupported values, and custom JSON or text marshalers are rejected.
  Typed-nil native providers are rejected at construction.
- Imports, durable state, audit history, staged changes, cache tenant count,
  cache feature count, invalidation history, retries, and fleet concurrency
  have explicit limits. PostgreSQL statements are parameterized and Valkey
  storage keys use a namespaced tenant digest.
- Blocking storage, watcher, and lifecycle calls receive the caller or fleet
  lifecycle context. Cache and fleet security-sensitive policies reject
  unbounded or explicit-default degradation.
- Health, cache, and fleet diagnostics use low-cardinality codes and do not
  retain tenant values, evaluation context, provider errors, or feature
  payloads.

Applications remain responsible for authenticating and authorizing management
operations, using TLS and least-privilege backend credentials, choosing
request deadlines, and excluding secrets and unnecessary personal data from
all flag inputs.

## Accepted residual risks

| Risk | Owner and rationale | Mitigation | Review condition |
| --- | --- | --- | --- |
| A trusted provider, backend, watcher, hook, or clock can block or allocate inside its own method. | Application integrator; Go interfaces cannot safely preempt arbitrary synchronous collaborator code. | Use bounded implementations, honor contexts, and enforce process resource limits. | Revisit when asynchronous or subprocess isolation becomes part of the public contract. |
| Tenant, feature, group, and actor identifiers can appear in returned management errors and audit records. | Application integrator; these identifiers are required to diagnose and attribute management operations. | Use opaque identifiers, keep secrets and personal data out of identifiers, and redact errors before external presentation. | Revisit if the package adds logging, remote error transport, or a public diagnostic sink. |
| Fail-open caches deliberately serve a bounded last-known-good snapshot during provider outages. | Application policy owner; availability may be preferable for non-security-sensitive product behavior. | Security-sensitive flags cannot use fail-open; staleness and outage windows are explicit and bounded. | Revisit whenever a flag becomes authorization-adjacent or its impact classification changes. |

Report vulnerabilities using the private process in the repository
[security policy](../SECURITY.md).
