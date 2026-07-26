package featureflags

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

const (
	documentFormat  = "go-feature-flags"
	documentVersion = 1
)

type documentWire struct {
	Format   string           `json:"format"`
	Version  int              `json:"version"`
	Features []definitionWire `json:"features"`
	Groups   []groupWire      `json:"groups,omitempty"`
}

type definitionWire struct {
	Key          string               `json:"key"`
	Type         Type                 `json:"type"`
	Default      valueWire            `json:"default"`
	Variants     map[string]valueWire `json:"variants,omitempty"`
	Metadata     map[string]string    `json:"metadata,omitempty"`
	Owner        string               `json:"owner,omitempty"`
	Lifecycle    Lifecycle            `json:"lifecycle,omitempty"`
	Dependencies []dependencyWire     `json:"dependencies,omitempty"`
	Groups       []string             `json:"groups,omitempty"`
	Tags         []string             `json:"tags,omitempty"`
	Version      uint64               `json:"version,omitempty"`
	Strategies   []strategyWire       `json:"strategies,omitempty"`
}

type dependencyWire struct {
	FeatureKey      string `json:"feature_key"`
	RequiredVariant string `json:"required_variant"`
}

type groupWire struct {
	Key        string            `json:"key"`
	Parent     string            `json:"parent,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Owner      string            `json:"owner,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Version    uint64            `json:"version,omitempty"`
	Strategies []strategyWire    `json:"strategies,omitempty"`
}

type valueWire struct {
	Type       Type            `json:"type"`
	Boolean    *bool           `json:"boolean,omitempty"`
	String     *string         `json:"string,omitempty"`
	Integer    *int64          `json:"integer,omitempty"`
	Float      *float64        `json:"float,omitempty"`
	Structured json.RawMessage `json:"structured,omitempty"`
}

type strategyWire struct {
	Kind          string            `json:"kind"`
	Name          string            `json:"name"`
	Variant       string            `json:"variant"`
	Tenants       []string          `json:"tenants,omitempty"`
	Subjects      []string          `json:"subjects,omitempty"`
	Environments  []string          `json:"environments,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	AllowTenants  []string          `json:"allow_tenants,omitempty"`
	DenyTenants   []string          `json:"deny_tenants,omitempty"`
	AllowSubjects []string          `json:"allow_subjects,omitempty"`
	DenySubjects  []string          `json:"deny_subjects,omitempty"`
	Seed          string            `json:"seed,omitempty"`
	Threshold     uint32            `json:"threshold,omitempty"`
	NotBefore     string            `json:"not_before,omitempty"`
	NotAfter      string            `json:"not_after,omitempty"`
	Location      string            `json:"location,omitempty"`
	Windows       []weeklyWire      `json:"windows,omitempty"`
	Fact          string            `json:"fact,omitempty"`
	Equals        *valueWire        `json:"equals,omitempty"`
}

type weeklyWire struct {
	Weekday     int `json:"weekday"`
	StartMinute int `json:"start_minute"`
	EndMinute   int `json:"end_minute"`
}

// Export encodes definitions and groups in a versioned deterministic JSON
// format. Strategy order is preserved because it affects precedence.
func Export(definitions []Definition, groups []GroupDefinition, limits Limits) ([]byte, error) {
	if _, err := NewSnapshotWithGroups(definitions, groups, limits); err != nil {
		return nil, err
	}
	features := append([]Definition(nil), definitions...)
	sort.Slice(features, func(i, j int) bool { return features[i].Key < features[j].Key })
	groupDefinitions := append([]GroupDefinition(nil), groups...)
	sort.Slice(groupDefinitions, func(i, j int) bool { return groupDefinitions[i].Key < groupDefinitions[j].Key })

	document := documentWire{Format: documentFormat, Version: documentVersion}
	for _, definition := range features {
		wire, err := encodeDefinition(definition)
		if err != nil {
			return nil, err
		}
		document.Features = append(document.Features, wire)
	}
	for _, group := range groupDefinitions {
		wire, err := encodeGroup(group)
		if err != nil {
			return nil, err
		}
		document.Groups = append(document.Groups, wire)
	}

	return json.Marshal(document)
}

// Import decodes and validates a deterministic export document.
func Import(data []byte, limits Limits) ([]Definition, []GroupDefinition, error) {
	if len(data) > limits.MaxImportBytes {
		return nil, nil, fmt.Errorf("import exceeds %d bytes: %w", limits.MaxImportBytes, ErrImportLimit)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document documentWire
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, fmt.Errorf("decode feature document: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, nil, err
	}
	if document.Format != documentFormat || document.Version != documentVersion {
		return nil, nil, fmt.Errorf("unsupported document %q version %d", document.Format, document.Version)
	}
	definitions := make([]Definition, 0, len(document.Features))
	for _, wire := range document.Features {
		definition, err := decodeDefinition(wire)
		if err != nil {
			return nil, nil, err
		}
		definitions = append(definitions, definition)
	}
	groups := make([]GroupDefinition, 0, len(document.Groups))
	for _, wire := range document.Groups {
		group, err := decodeGroup(wire)
		if err != nil {
			return nil, nil, err
		}
		groups = append(groups, group)
	}
	if _, err := NewSnapshotWithGroups(definitions, groups, limits); err != nil {
		return nil, nil, err
	}

	return definitions, groups, nil
}

func encodeDefinition(definition Definition) (definitionWire, error) {
	wire := definitionWire{
		Key:       definition.Key,
		Type:      definition.Type,
		Default:   encodeValue(definition.Default),
		Metadata:  cloneStringMap(definition.Metadata),
		Owner:     definition.Owner,
		Lifecycle: definition.Lifecycle,
		Groups:    sortedStrings(definition.Groups),
		Tags:      sortedStrings(definition.Tags),
		Version:   definition.Version,
	}
	if definition.Variants != nil {
		wire.Variants = make(map[string]valueWire, len(definition.Variants))
		for name, value := range definition.Variants {
			wire.Variants[name] = encodeValue(value)
		}
	}
	dependencies := append([]Dependency(nil), definition.Dependencies...)
	sort.Slice(dependencies, func(i, j int) bool {
		if dependencies[i].FeatureKey == dependencies[j].FeatureKey {
			return dependencies[i].RequiredVariant < dependencies[j].RequiredVariant
		}
		return dependencies[i].FeatureKey < dependencies[j].FeatureKey
	})
	for _, dependency := range dependencies {
		wire.Dependencies = append(wire.Dependencies, dependencyWire(dependency))
	}
	for _, strategy := range definition.Strategies {
		encoded, err := encodeStrategy(strategy)
		if err != nil {
			return definitionWire{}, fmt.Errorf("feature %q: %w", definition.Key, err)
		}
		wire.Strategies = append(wire.Strategies, encoded)
	}

	return wire, nil
}

func decodeDefinition(wire definitionWire) (Definition, error) {
	defaultValue, err := decodeValue(wire.Default)
	if err != nil {
		return Definition{}, fmt.Errorf("feature %q default: %w", wire.Key, err)
	}
	definition := Definition{
		Key:       wire.Key,
		Type:      wire.Type,
		Default:   defaultValue,
		Metadata:  cloneStringMap(wire.Metadata),
		Owner:     wire.Owner,
		Lifecycle: wire.Lifecycle,
		Groups:    append([]string(nil), wire.Groups...),
		Tags:      append([]string(nil), wire.Tags...),
		Version:   wire.Version,
	}
	if wire.Variants != nil {
		definition.Variants = make(map[string]Value, len(wire.Variants))
		for name, encoded := range wire.Variants {
			value, err := decodeValue(encoded)
			if err != nil {
				return Definition{}, fmt.Errorf("feature %q variant %q: %w", wire.Key, name, err)
			}
			definition.Variants[name] = value
		}
	}
	for _, dependency := range wire.Dependencies {
		definition.Dependencies = append(definition.Dependencies, Dependency(dependency))
	}
	for _, encoded := range wire.Strategies {
		strategy, err := decodeStrategy(encoded)
		if err != nil {
			return Definition{}, fmt.Errorf("feature %q: %w", wire.Key, err)
		}
		definition.Strategies = append(definition.Strategies, strategy)
	}

	return definition, nil
}

func encodeGroup(group GroupDefinition) (groupWire, error) {
	wire := groupWire{
		Key:      group.Key,
		Parent:   group.Parent,
		Metadata: cloneStringMap(group.Metadata),
		Owner:    group.Owner,
		Tags:     sortedStrings(group.Tags),
		Version:  group.Version,
	}
	for _, strategy := range group.Strategies {
		encoded, err := encodeStrategy(strategy)
		if err != nil {
			return groupWire{}, fmt.Errorf("group %q: %w", group.Key, err)
		}
		wire.Strategies = append(wire.Strategies, encoded)
	}

	return wire, nil
}

func decodeGroup(wire groupWire) (GroupDefinition, error) {
	group := GroupDefinition{
		Key:      wire.Key,
		Parent:   wire.Parent,
		Metadata: cloneStringMap(wire.Metadata),
		Owner:    wire.Owner,
		Tags:     append([]string(nil), wire.Tags...),
		Version:  wire.Version,
	}
	for _, encoded := range wire.Strategies {
		strategy, err := decodeStrategy(encoded)
		if err != nil {
			return GroupDefinition{}, fmt.Errorf("group %q: %w", wire.Key, err)
		}
		group.Strategies = append(group.Strategies, strategy)
	}

	return group, nil
}

func encodeValue(value Value) valueWire {
	wire := valueWire{Type: value.Type()}
	switch value.Type() {
	case TypeBoolean:
		wire.Boolean = &value.boolean
	case TypeString, TypeDecimal:
		wire.String = &value.text
	case TypeInteger:
		wire.Integer = &value.integer
	case TypeFloat:
		wire.Float = &value.floating
	case TypeStructured:
		wire.Structured = append(json.RawMessage(nil), value.structured...)
	}

	return wire
}

func decodeValue(wire valueWire) (Value, error) {
	switch wire.Type {
	case TypeBoolean:
		if wire.Boolean == nil {
			break
		}
		return BooleanValue(*wire.Boolean), nil
	case TypeString:
		if wire.String == nil {
			break
		}
		return StringValue(*wire.String), nil
	case TypeInteger:
		if wire.Integer == nil {
			break
		}
		return IntegerValue(*wire.Integer), nil
	case TypeFloat:
		if wire.Float == nil {
			break
		}
		return FloatValue(*wire.Float), nil
	case TypeDecimal:
		if wire.String == nil {
			break
		}
		return DecimalValue(*wire.String), nil
	case TypeStructured:
		if wire.Structured == nil {
			break
		}
		return StructuredValue(wire.Structured), nil
	}

	return Value{}, fmt.Errorf("value payload does not match type %q: %w", wire.Type, ErrInvalidValue)
}

func encodeStrategy(strategy Strategy) (strategyWire, error) {
	wire := strategyWire{Name: strategy.StrategyName(), Variant: strategy.TargetVariant()}
	switch value := strategy.(type) {
	case ExactTargetStrategy:
		wire.Kind = "exact"
		wire.Tenants = sortedStrings(value.Tenants)
		wire.Subjects = sortedStrings(value.Subjects)
		wire.Environments = sortedStrings(value.Environments)
		wire.Attributes = cloneStringMap(value.Attributes)
	case PercentageStrategy:
		wire.Kind = "percentage"
		wire.Seed = value.Seed
		wire.Threshold = value.Threshold
	case SetStrategy:
		wire.Kind = "set"
		wire.AllowTenants = sortedStrings(value.AllowTenants)
		wire.DenyTenants = sortedStrings(value.DenyTenants)
		wire.AllowSubjects = sortedStrings(value.AllowSubjects)
		wire.DenySubjects = sortedStrings(value.DenySubjects)
	case TimeWindowStrategy:
		wire.Kind = "time_window"
		wire.NotBefore = formatOptionalTime(value.NotBefore)
		wire.NotAfter = formatOptionalTime(value.NotAfter)
	case ScheduleStrategy:
		wire.Kind = "schedule"
		wire.Location = value.Location
		for _, window := range value.Windows {
			wire.Windows = append(wire.Windows, weeklyWire{
				Weekday: int(window.Weekday), StartMinute: window.StartMinute, EndMinute: window.EndMinute,
			})
		}
	case FactStrategy:
		wire.Kind = "fact"
		wire.Fact = value.Fact
		encoded := encodeValue(value.Equals)
		wire.Equals = &encoded
	default:
		return strategyWire{}, fmt.Errorf("strategy %q: %w", strategy.StrategyName(), ErrUnsupportedStrategy)
	}

	return wire, nil
}

func decodeStrategy(wire strategyWire) (Strategy, error) {
	switch wire.Kind {
	case "exact":
		return ExactTargetStrategy{
			Name: wire.Name, Variant: wire.Variant, Tenants: wire.Tenants, Subjects: wire.Subjects,
			Environments: wire.Environments, Attributes: wire.Attributes,
		}, nil
	case "percentage":
		return PercentageStrategy{
			Name: wire.Name, Variant: wire.Variant, Seed: wire.Seed, Threshold: wire.Threshold,
		}, nil
	case "set":
		return SetStrategy{
			Name: wire.Name, Variant: wire.Variant,
			AllowTenants: wire.AllowTenants, DenyTenants: wire.DenyTenants,
			AllowSubjects: wire.AllowSubjects, DenySubjects: wire.DenySubjects,
		}, nil
	case "time_window":
		notBefore, err := parseOptionalTime(wire.NotBefore)
		if err != nil {
			return nil, err
		}
		notAfter, err := parseOptionalTime(wire.NotAfter)
		if err != nil {
			return nil, err
		}
		return TimeWindowStrategy{
			Name: wire.Name, Variant: wire.Variant, NotBefore: notBefore, NotAfter: notAfter,
		}, nil
	case "schedule":
		windows := make([]WeeklyWindow, 0, len(wire.Windows))
		for _, window := range wire.Windows {
			windows = append(windows, WeeklyWindow{
				Weekday: time.Weekday(window.Weekday), StartMinute: window.StartMinute, EndMinute: window.EndMinute,
			})
		}
		return ScheduleStrategy{
			Name: wire.Name, Variant: wire.Variant, Location: wire.Location, Windows: windows,
		}, nil
	case "fact":
		if wire.Equals == nil {
			return nil, fmt.Errorf("fact strategy %q has no value", wire.Name)
		}
		value, err := decodeValue(*wire.Equals)
		if err != nil {
			return nil, err
		}
		return FactStrategy{
			Name: wire.Name, Variant: wire.Variant, Fact: wire.Fact, Equals: value,
		}, nil
	default:
		return nil, fmt.Errorf("strategy kind %q: %w", wire.Kind, ErrUnsupportedStrategy)
	}
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}

	return value.UTC().Format(time.RFC3339Nano)
}

func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse strategy time: %w", err)
	}

	return parsed, nil
}

func sortedStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := append([]string(nil), values...)
	sort.Strings(cloned)

	return cloned
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing data: %w", err)
	}

	return fmt.Errorf("feature document contains trailing data")
}
