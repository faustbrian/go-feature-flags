# feature-flags

Deterministic, tenant-safe feature management and rollout evaluation for Go.
The native API supports richer policies than OpenFeature; the OpenFeature
provider is an optional interoperability adapter.

## Quick start

```go
package main

import (
    "context"
    "fmt"

    featureflags "github.com/faustbrian/golib/pkg/feature-flags"
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
| root | native values, strategies, snapshots, providers, cache, import/export |
| `memory` | shared conformance test for the in-process provider |
| `postgres` | atomic PostgreSQL document backend and provider |
| `valkey` | atomic Valkey document backend and provider |
| `openfeature` | fixed-tenant OpenFeature provider adapter |
| `featureflagstest` | provider conformance suite for custom backends |

## Guarantees and boundaries

- Evaluation uses only caller-supplied context; there is no global client,
  hidden clock, context scraping, or background refresher.
- Snapshots deep-copy definitions and bind one tenant for request consistency.
- Every management mutation uses optimistic feature or group versions.
- Memory, PostgreSQL, and Valkey share the same provider contract.
- Cache fallback is explicitly fail-open or fail-closed and time-bounded.
- Feature flags are not authentication or authorization controls.

See [the native reference](docs/native-api.md),
[provider operations](docs/providers.md), [OpenFeature mapping](docs/openfeature.md),
[hardening evidence](docs/hardening.md), [security](SECURITY.md),
[cookbook](docs/cookbook.md), and [FAQ](docs/faq.md).

## Development

```sh
make check
```

Real backend conformance requires disposable PostgreSQL and Valkey instances:

```sh
FEATURE_FLAGS_POSTGRES_DSN='postgres://...' \
FEATURE_FLAGS_VALKEY_ADDRESS='127.0.0.1:6379' \
make integration
```

The minimum toolchain is Go 1.26.5.

## License

MIT.
