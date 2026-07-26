# Compatibility

The module requires Go 1.26.5. Public API follows semantic versioning once a
stable release is published. Export and durable tenant documents have separate
explicit format versions; unsupported versions fail closed.

PostgreSQL uses pgx v5 and requires a server supporting `INSERT ... ON
CONFLICT`, row locking, and `bytea`. Valkey uses RESP3 through valkey-go and Lua
scripts for compare-and-swap. Real backend behavior is verified by the same
provider conformance suite.

The OpenFeature adapter targets the OpenFeature Go SDK version declared in
`go.mod`. Its capability losses are listed in [openfeature.md](openfeature.md).
