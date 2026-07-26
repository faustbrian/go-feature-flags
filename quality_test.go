package featureflags

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func FuzzImportNeverPanics(f *testing.F) {
	f.Add([]byte(`{"format":"go-feature-flags","version":1,"features":[]}`))
	f.Add([]byte(`null`))
	f.Add([]byte{0xff, 0x00, 0x01})

	f.Fuzz(func(t *testing.T, data []byte) {
		limits := DefaultLimits()
		limits.MaxImportBytes = 64 * 1024
		if len(data) > limits.MaxImportBytes {
			t.Skip()
		}
		_, _, _ = Import(data, limits)
	})
}

func FuzzBucketIsStableAndBounded(f *testing.F) {
	f.Add("seed", "flag", "tenant", "subject")
	f.Add("", "", "", "")

	f.Fuzz(func(t *testing.T, seed, feature, tenant, subject string) {
		first := Bucket(seed, feature, tenant, subject)
		second := Bucket(seed, feature, tenant, subject)
		if first != second {
			t.Fatalf("Bucket() changed from %d to %d", first, second)
		}
		if uint64(first) >= bucketPrecision {
			t.Fatalf("Bucket() = %d, want less than %d", first, bucketPrecision)
		}
	})
}

func FuzzDefinitionValidationNeverPanics(f *testing.F) {
	f.Add("flag", "control", "enabled", "alice")
	f.Add("", "", "", "")
	f.Add(strings.Repeat("k", 257), strings.Repeat("v", 8*1024+1), "variant", "subject")

	f.Fuzz(func(t *testing.T, key, defaultValue, variantValue, subject string) {
		if len(key)+len(defaultValue)+len(variantValue)+len(subject) > 64*1024 {
			t.Skip()
		}
		definition := Definition{
			Key: key, Type: TypeString, Default: StringValue(defaultValue),
			Lifecycle: LifecycleActive,
			Variants:  map[string]Value{"selected": StringValue(variantValue)},
			Strategies: []Strategy{ExactTargetStrategy{
				Name: "target", Variant: "selected", Subjects: []string{subject},
			}},
		}
		if err := definition.Validate(DefaultLimits()); err != nil {
			return
		}
		snapshot, err := NewSnapshot([]Definition{definition}, DefaultLimits())
		if err != nil {
			t.Fatalf("NewSnapshot(valid definition) error = %v", err)
		}
		_, _ = snapshot.String(key, Context{Subject: subject})
	})
}

func FuzzContextEvaluationNeverPanics(f *testing.F) {
	f.Add("tenant-a", "alice", "plan", "pro", []byte(`{"age":10}`))
	f.Add("", "", "", "", []byte{0xff})
	f.Add(strings.Repeat("t", 8*1024+1), "subject", strings.Repeat("k", 257), "value", []byte(`null`))

	snapshot, err := NewSnapshot([]Definition{{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Lifecycle: LifecycleActive,
	}}, DefaultLimits())
	if err != nil {
		f.Fatalf("NewSnapshot() error = %v", err)
	}
	f.Fuzz(func(t *testing.T, tenant, subject, key, value string, fact []byte) {
		if len(tenant)+len(subject)+len(key)+len(value)+len(fact) > 64*1024 {
			t.Skip()
		}
		_, _ = snapshot.Boolean("flag", Context{
			Tenant: tenant, Subject: subject,
			Attributes: map[string]string{key: value},
			Facts:      map[string]Value{key: StructuredValue(fact)},
		})
	})
}

func TestSnapshotsRemainConsistentDuringConcurrentUpdates(t *testing.T) {
	provider := NewMemoryProvider(DefaultLimits())
	created, err := provider.Create(t.Context(), "tenant-a", Definition{
		Key: "flag", Type: TypeInteger, Default: IntegerValue(1),
		Lifecycle: LifecycleActive,
	}, "writer")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	const updates = 100
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	wait.Add(2)
	go func() {
		defer wait.Done()
		version := created.Version
		for value := int64(2); value <= updates+1; value++ {
			updated, updateErr := provider.Update(t.Context(), "tenant-a", Definition{
				Key: "flag", Type: TypeInteger, Default: IntegerValue(value),
				Lifecycle: LifecycleActive,
			}, version, "writer")
			if updateErr != nil {
				errorsSeen <- updateErr
				return
			}
			version = updated.Version
		}
	}()
	go func() {
		defer wait.Done()
		for range updates {
			snapshot, snapshotErr := provider.Snapshot(t.Context(), "tenant-a")
			if snapshotErr != nil {
				errorsSeen <- snapshotErr
				return
			}
			detail, evaluationErr := snapshot.Integer(
				"flag", Context{Tenant: "tenant-a"},
			)
			if evaluationErr != nil {
				errorsSeen <- evaluationErr
				return
			}
			if detail.Value != int64(detail.Version) {
				errorsSeen <- fmt.Errorf(
					"snapshot mixed value %d and version %d", detail.Value, detail.Version,
				)
				return
			}
		}
	}()
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
}

func TestCachedProviderIsRaceSafeDuringRefreshUpdateAndShutdown(t *testing.T) {
	provider := NewMemoryProvider(DefaultLimits())
	created, err := provider.Create(t.Context(), "tenant-a", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Lifecycle: LifecycleActive,
	}, "writer")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	clock := &manualCacheClock{}
	cached, err := NewCachedProvider(provider, CacheConfig{
		Clock: clock, MaxStaleness: 1, MaxOutageStaleness: 1,
		FailurePolicy: FailClosed, MaxTenants: 2,
	})
	if err != nil {
		t.Fatalf("NewCachedProvider() error = %v", err)
	}

	const iterations = 100
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 7)
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range iterations {
				snapshot, snapshotErr := cached.Snapshot(t.Context(), "tenant-a")
				if snapshotErr != nil {
					errorsSeen <- snapshotErr
					return
				}
				if _, evaluationErr := snapshot.Boolean("flag", Context{Tenant: "tenant-a"}); evaluationErr != nil {
					errorsSeen <- evaluationErr
					return
				}
			}
		}()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		for range iterations {
			if _, refreshErr := cached.Refresh(t.Context(), "tenant-a"); refreshErr != nil {
				errorsSeen <- refreshErr
				return
			}
		}
	}()
	wait.Add(1)
	go func() {
		defer wait.Done()
		version := created.Version
		for index := range iterations {
			updated, updateErr := cached.Update(t.Context(), "tenant-a", Definition{
				Key: "flag", Type: TypeBoolean, Default: BooleanValue(index%2 == 0),
				Lifecycle: LifecycleActive,
			}, version, "writer")
			if updateErr != nil {
				errorsSeen <- updateErr
				return
			}
			version = updated.Version
		}
	}()
	wait.Add(1)
	go func() {
		defer wait.Done()
		for range iterations {
			if closeErr := cached.Close(t.Context()); closeErr != nil {
				errorsSeen <- closeErr
				return
			}
		}
	}()
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
}

func BenchmarkSnapshotBoolean(b *testing.B) {
	definitions := make([]Definition, 1_000)
	for index := range definitions {
		definitions[index] = Definition{
			Key: fmt.Sprintf("flag.%04d", index), Type: TypeBoolean,
			Default: BooleanValue(false), Lifecycle: LifecycleActive,
			Variants: map[string]Value{"enabled": BooleanValue(true)},
			Strategies: []Strategy{PercentageStrategy{
				Name: "rollout", Variant: "enabled", Seed: "v1", Threshold: 50_000,
			}},
		}
	}
	snapshot, err := NewSnapshot(definitions, DefaultLimits())
	if err != nil {
		b.Fatalf("NewSnapshot() error = %v", err)
	}
	ctx := Context{Tenant: "tenant-a", Subject: "subject-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, evaluationErr := snapshot.Boolean("flag.0500", ctx); evaluationErr != nil {
			b.Fatal(evaluationErr)
		}
	}
}

func BenchmarkSnapshotDeepDependencyChain(b *testing.B) {
	limits := DefaultLimits()
	definitions := make([]Definition, limits.MaxEvaluationDepth+1)
	for index := range definitions {
		definitions[index] = Definition{
			Key: fmt.Sprintf("dependency.%02d", index), Type: TypeBoolean,
			Default: BooleanValue(false), Lifecycle: LifecycleActive,
			Variants: map[string]Value{"enabled": BooleanValue(true)},
			Strategies: []Strategy{ExactTargetStrategy{
				Name: "always", Variant: "enabled",
			}},
		}
		if index+1 < len(definitions) {
			definitions[index].Dependencies = []Dependency{{
				FeatureKey:      fmt.Sprintf("dependency.%02d", index+1),
				RequiredVariant: "enabled",
			}}
		}
	}
	snapshot, err := NewSnapshot(definitions, limits)
	if err != nil {
		b.Fatalf("NewSnapshot() error = %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, evaluationErr := snapshot.Boolean("dependency.00", Context{}); evaluationErr != nil {
			b.Fatal(evaluationErr)
		}
	}
}

func BenchmarkSnapshotMaximumContextAndBatch(b *testing.B) {
	limits := DefaultLimits()
	definitions := make([]Definition, limits.MaxBatchSize)
	requests := make([]EvaluationRequest, limits.MaxBatchSize)
	for index := range definitions {
		key := fmt.Sprintf("flag.%03d", index)
		definitions[index] = Definition{
			Key: key, Type: TypeBoolean, Default: BooleanValue(false),
			Lifecycle: LifecycleActive,
		}
		requests[index] = EvaluationRequest{Key: key, Type: TypeBoolean}
	}
	snapshot, err := NewSnapshot(definitions, limits)
	if err != nil {
		b.Fatalf("NewSnapshot() error = %v", err)
	}
	contextValue := Context{
		Attributes: make(map[string]string, limits.MaxAttributes),
		Facts:      make(map[string]Value, limits.MaxFacts),
	}
	for index := range limits.MaxAttributes {
		contextValue.Attributes[fmt.Sprintf("attribute.%03d", index)] = "value"
	}
	for index := range limits.MaxFacts {
		contextValue.Facts[fmt.Sprintf("fact.%03d", index)] = IntegerValue(int64(index))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, evaluationErr := snapshot.Batch(contextValue, requests); evaluationErr != nil {
			b.Fatal(evaluationErr)
		}
	}
}

func TestProviderInputRejectsCancelledContextBeforeTenant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := providerInput(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("providerInput() error = %v, want context.Canceled", err)
	}
}
