# OpenFeature adapter

The `openfeature` package adapts one native provider and one fixed tenant to the
OpenFeature Go SDK. Fixing the tenant at construction prevents evaluation
context from switching storage partitions.

Boolean, string, integer, float, and structured native values map to matching
OpenFeature resolution methods. OpenFeature has no exact decimal value type;
decimal flags are intentionally unavailable through this adapter. Native
versions and matched strategy names are exposed as bounded metadata.

OpenFeature targeting key maps to native subject. Flattened string fields map
to attributes; supported scalar and structured values map to typed facts.
Mapping does not mutate native strategy order, dependency behavior, group
inheritance, snapshot semantics, or reasons.

Native management, groups, dependencies, staged changes, schedules, audit,
cleanup, import/export, cache refresh, and provider health do not round-trip
through the OpenFeature evaluation contract. Use the native provider for those
capabilities.

OpenFeature hooks and events are SDK concerns. The adapter exposes configured
hooks but does not synthesize provider events from native management changes,
because the native provider contract has no ordered change-event stream. Its
event channel therefore remains silent and closes exactly once at shutdown.
The adapter exposes provider metadata, initialization, shutdown, and evaluation
without installing global hooks or a global client. The application owns SDK
lifecycle.
