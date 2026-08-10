# Security policy

Report vulnerabilities privately through GitHub Security Advisories for this
repository. Do not include production credentials, tenant documents, context,
or personal data in a report.

Feature flags are not authorization. A caller must authenticate and authorize
all management operations before invoking a provider. This module deliberately
does not embed HTTP or JSON-RPC authentication policy.

Use tenant-bound snapshots, stable opaque subject identifiers, bounded limits,
fail-closed cache policy for safety-sensitive product behavior, TLS to durable
backends, least-privilege credentials, and application-owned audit actor IDs.
Never place secrets or unnecessary personal information in evaluation context,
metadata, diagnostics, cache keys, metrics, or logs.

The package has no hidden background worker, global mutable client, or context
scraping. `Fleet` starts one refresher and at most one configured invalidation
watcher only through an explicit `Start` call and joins them through
`Shutdown`. Applications must stop fleet readiness and join the fleet before
closing caller-owned provider, cache, and watcher resources.
Validated activation is not rolled back when a last-known-good cache write
fails; bounded refresh and cache failure codes keep the two states observable.

Security-sensitive fleet policies may be fail-closed or use a separately
bounded last-known-good snapshot. Construction rejects fail-open and explicit
default behavior for such flags. This protection does not make a feature flag
an authorization decision.
