# Migration from Cline Toggl

Inventory each Toggl feature, its type, variants, activation state, strategies,
groups, and prerequisites. Assign a stable native key and rollout seed. Convert
implicit request data into explicit `Context` fields or typed facts.

Export the target native document and run `ImportDocument` with `DryRun: true`.
Resolve every conflict explicitly, then import with fail-on-conflict for the
cutover. Compare old and native decisions in shadow traffic without using
either result for authorization. Freeze bucketing vectors for representative
subjects before increasing rollout percentages.

OpenFeature migration is evaluation-only. Keep management operations on the
native provider and document decimal and policy capability losses before
moving an OpenFeature consumer.

During cutover, acquire one native snapshot per request, monitor low-cardinality
reason and error counts, and retain an application-owned rollback path to the
previous evaluator. Never dual-write without explicit conflict ownership.
