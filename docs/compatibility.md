# Compatibility

[Documentation index](README.md)

The v2 module requires Go 1.27.0. Major releases follow semantic import
versioning. Export and durable tenant documents have separate explicit format
versions; unsupported versions fail closed.

PostgreSQL uses pgx v5 and requires a server supporting `INSERT ... ON
CONFLICT`, row locking, and `bytea`. Valkey uses RESP3 through valkey-go and Lua
scripts for compare-and-swap. Real backend behavior is verified by the same
provider conformance suite.

The OpenFeature adapter targets the OpenFeature Go SDK version declared in
`go.mod`. Its capability losses are listed in [openfeature.md](openfeature.md).

The prepared v2.0.1 patch retains those API and storage identities. It repairs
exact attribute presence, portable group precedence and local cache publication;
previously sorted group attachments require the explicit
[patch adoption](migration.md#v201-patch-adoption) steps when sorting changed
intended targeting. Release preparation is not publication or new live
PostgreSQL/Valkey qualification.
