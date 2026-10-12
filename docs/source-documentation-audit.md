# Source documentation audit

## Acceptance and owned boundary

This Tier A pass owns truthful comments and directly contradicted maintained
prose at main e7af09251f02b9c9a99bd4fb5d1447d0a7d50950. Preserve runtime syntax,
literals, directives, schema/Lua, dependencies, API baselines and authority
fixtures. Consumers are native management/evaluation, provider composition,
request snapshots, Fleet callbacks, OpenFeature SDK clients and test factories.
Named risks are inaccurate evaluation precedence, ownership, time/consistency,
cancellation, callback and resource guarantees; runtime repair is not in scope.

## Reproducible before-write inventory

`git ls-files` and `golib source-docs inventory --json` identify one v2 module,
79 Go files: 24 production and 55 tests, in root, featureflagstest, memory
(test-only conformance namespace), openfeature, postgres and valkey. The initial
structural inventory has 512 declaration/member comment findings. Every tracked
source and test participates, including two integration-tag fixtures, fuzz seeds,
benchmarks and Examples; build-selected discovery is not the audit boundary.
The private schema embed and Lua scripts are executable inputs, not prose.
No generated/platform Go or go:generate input is present; generator execution
is inapplicable to this source pass. Native API mode is
apidiff at api/baseline.txt, not a full-prose snapshot. The declared docs operation
runs Examples only. Preserve frozen authority/history inputs; no new OpenFeature
or provider conformance claim is made.

## Missing comments versus investigated contracts

Public member/method gaps need semantic documentation, not filler. Source tracing
establishes inactive/default, dependency, child-first group and first-strategy
precedence; callbacks provide immutable snapshot behavior and remain trusted.
Native contexts borrow maps, custom strategies may block/panic, and memory cloning
invokes callbacks under locks. Limits are selected count/byte bounds, not total
heap/time caps; constructors do not normalize Limits or authenticate identifiers.

Investigated surprises: Bucket uses go-feature-flags/bucket/v1 and low32 length
framing. Native Batch is the exported method. Exact attribute lookup can match a
missing attribute to an empty target. Nonmatching strategy diagnostics are dropped.
Export sorts group attachment order even though native group order affects
precedence. CachedProvider has no refresh coalescing/revision fence; overlapping
refresh and invalidation can republish earlier state. Its Close delegates without
creating a closed admission state. Memory Close is a context-only no-op.
ProviderSnapshotLoader stamps fetch time, not authoritative commit time, and
its sorted portable digest can repeat for different native group precedence. Fleet
refresh uses the leader context, watcher failure stops that watcher without
reconnect, cancellation cannot preempt uncooperative callbacks, and stopped Fleet
evaluation can still use an active snapshot/policy. Native durable providers CAS
one tenant document and retry storage conflicts; they do not close external clients.

The dated docs/security.md Version 2.0 (2026-09-27) is preserved as reviewed
history. Its cache security-policy statement is broader than CacheConfig, which
has only global FailurePolicy and no per-flag SecuritySensitive classification.
Its audit limit is a retained count, not Actor byte validation. Current source
comments and provider guidance state these narrower limits; this pass does not
invent a new security review or runtime fix.

## Smallest sufficient proof

Use pinned Go 1.27.2 formatting, all 79 AST/significant-token/literal/directive
identity, complete relevant package renders and the declared Example operation.
Run structural source, local-doc/link and specification applicability checks and
pinned changed/staged secrets; inspect the complete final diff for usefulness and
truth. Small deterministic observations may support concrete documented surprises.
No DB/Valkey services, race/leak/fuzz/performance campaigns or release rehearsal
are needed for unchanged runtime. Exact three owned caches/helpers/renders are
removed after proof; coordinator owns remote main and exact-source hosted CI.

## Outcomes and retained limits

Pinned Go 1.27.2 formatting and all 79 tracked-file AST/significant-token/literal/
directive comparison passed. CacheClock member documentation expands the former
single-line interface and introduces one optional implicit method terminator;
its AST, signature and all significant tokens are identical. Every other file's
raw token stream, including implicit terminators, is exact. All comment groups
parse/render, and all five production packages have inspected full native GoDoc
renders. The memory directory is an external-test-only conformance namespace,
not a sixth production API. Native check-docs passes links and the one existing
Example. Structural findings went from 512 to zero; this is structure proof,
not semantic completeness by count. Local docs pass and specification checking
reports zero applicable typed decisions. No full-prose API snapshot is configured.

Two small public-contract observations used no services. An absent string
attribute matched an empty exact target. With a fixed UTC clock at
2023-11-14T22:13:20Z, group order b/a selected b natively but a after portable
round-trip; reversing native attachment order produced identical Export bytes.
Two same-version provider graphs selected a/b while their canonical loader
revisions remained equal. Current comments and guides disclose these behaviors;
portable fixtures/history and runtime algorithms remain unchanged.

Final root comment refinements received a fresh all 79 proof and native root
render; four unchanged package renders, Example and observation results were
reused with unchanged runtime/dependency/tool inputs. All exact owned caches,
helpers, probes and renders were removed immediately after inspection. The
complete Tier A diff receives ordinary truth/usefulness inspection and a pinned
changed/staged secret scan before commit. No service, race/leak, active fuzz,
performance, release rehearsal or new standards/security verdict is established.
Remote main and exact-source hosted CI remain coordinator-owned.

## Objective Enforcement

The checksum-pinned public Tools v2.4.0 verifier now supplies
`make source-docs-check` as a prerequisite of the existing local and CI Make
routes. Ordinary shared CI selects strict source-documentation checking and
requires its result. Native prerequisites, operation budgets, security and
release controls are retained. Hosted exact-head evidence is recorded by the
coordination ledger; configuration alone does not establish green delivery.
