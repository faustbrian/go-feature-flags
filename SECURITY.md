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
scraping. Applications own refresh and scheduling goroutines and must cancel
and join them during shutdown.
