package featureflags_test

import (
	"context"
	"fmt"

	featureflags "github.com/faustbrian/go-feature-flags"
)

func Example() {
	ctx := context.Background()
	provider := featureflags.NewMemoryProvider(featureflags.DefaultLimits())

	_, err := provider.Create(ctx, "tenant-a", featureflags.Definition{
		Key:       "checkout.redesign",
		Type:      featureflags.TypeBoolean,
		Default:   featureflags.BooleanValue(true),
		Lifecycle: featureflags.LifecycleActive,
	}, "deployment-controller")
	if err != nil {
		panic(err)
	}

	snapshot, err := provider.Snapshot(ctx, "tenant-a")
	if err != nil {
		panic(err)
	}
	detail, err := snapshot.Boolean("checkout.redesign", featureflags.Context{
		Tenant:  "tenant-a",
		Subject: "customer-123",
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(detail.Value, detail.Reason)
	// Output: true default
}
