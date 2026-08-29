# feature-flags

[![CI](https://github.com/faustbrian/go-feature-flags/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/faustbrian/go-feature-flags/actions/workflows/ci.yml)
[![CodeQL](https://img.shields.io/badge/CodeQL-required-blue)](https://github.com/faustbrian/go-feature-flags/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/badge/coverage-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Mutation](https://img.shields.io/badge/mutation-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Documentation](https://img.shields.io/badge/docs-checked_in_CI-blue)](docs/)
[![Go Reference](https://pkg.go.dev/badge/github.com/faustbrian/go-feature-flags.svg)](https://pkg.go.dev/github.com/faustbrian/go-feature-flags)
[![Release](https://img.shields.io/github/v/release/faustbrian/go-feature-flags?sort=semver)](https://github.com/faustbrian/go-feature-flags/releases)
[![Go](https://img.shields.io/badge/go-1.26.6-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Deterministic, tenant-safe feature management and rollout evaluation for Go.
The native API supports richer policies than OpenFeature; the OpenFeature
provider is an optional interoperability adapter.

## Quick start

```go
package main

import (
    "context"
    "fmt"

    featureflags "github.com/faustbrian/go-feature-flags"
)

func main() {
    provider := featureflags.NewMemoryProvider(featureflags.DefaultLimits())
    _, err := provider.Create(context.Background(), "tenant-a",
        featureflags.Definition{
            Key:       "checkout.redesign",
            Type:      featureflags.TypeBoolean,
            Default:   featureflags.BooleanValue(false),
            Lifecycle: featureflags.LifecycleActive,
            Variants: map[string]featureflags.Value{
                "enabled": featureflags.BooleanValue(true),
            },
            Strategies: []featureflags.Strategy{
                featureflags.PercentageStrategy{
                    Name: "ten-percent", Variant: "enabled",
                    Seed: "checkout-v1", Threshold: 10_000,
                },
            },
        }, "deployment-controller")
    if err != nil {
        panic(err)
    }

    snapshot, err := provider.Snapshot(context.Background(), "tenant-a")
    if err != nil {
        panic(err)
    }
    detail, err := snapshot.Boolean("checkout.redesign", featureflags.Context{
        Tenant: "tenant-a", Subject: "customer-123",
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(detail.Value, detail.Variant, detail.Reason)
}
```

`Threshold` has five decimal digits of percentage precision. `10_000` is
10%, and `100_000` is 100%. Assignment hashes the seed, feature, tenant, and
subject with stable length-delimited SHA-256 input.

## Packages

| Package | Purpose |
|---|---|
| root | native values, strategies, snapshots, providers, cache, fleet refresh, import/export |
| `memory` | shared conformance test for the in-process provider |
| `postgres` | atomic PostgreSQL document backend and provider |
| `valkey` | atomic Valkey document backend and provider |
| `openfeature` | fixed-tenant OpenFeature provider adapter |
| `featureflagstest` | provider conformance suite for custom backends |

## Guarantees and boundaries

- Evaluation uses only caller-supplied context; there is no global client,
  hidden clock, or context scraping. `Fleet` is an explicit optional refresher.
- Snapshots deep-copy definitions and bind one tenant for request consistency.
- Every management mutation uses optimistic feature or group versions.
- Memory, PostgreSQL, and Valkey share the same provider contract.
- Cache fallback is explicitly fail-open or fail-closed and time-bounded.
- Fleet startup, jitter, provider load, watcher delivery, invalidation
  convergence, degraded evaluation, and shutdown are explicit and bounded.
- Feature flags are not authentication or authorization controls.

See [the native reference](docs/native-api.md),
[provider operations](docs/providers.md), [OpenFeature mapping](docs/openfeature.md),
[verification](docs/verification.md), [security](SECURITY.md),
[fleet and Kubernetes operation](docs/fleet.md),
[cookbook](docs/cookbook.md), and [FAQ](docs/faq.md).

## Development

```sh
make check
```

The shared `golib` tool starts disposable PostgreSQL and Valkey fixtures for
the declared integration gates. Run `make check` from the repository root.

The minimum toolchain is Go 1.26.6.

## License

MIT.
