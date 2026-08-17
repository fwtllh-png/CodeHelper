// Package observation defines the versioned evidence contract shared by
// runtime observation producers, durable journals, reducers, and exporters.
//
// Observation is never an execution authority. This package intentionally has
// no dependency on engine, host, tool, persistence, or orchestration packages.
package observation

type Kind string
type Owner string
type Durability string
type PayloadPolicy string
type RetentionClass string
type OTELMapping string
type Priority string

const (
	DurabilityRetained  Durability = "retained"
	DurabilityBounded   Durability = "bounded"
	DurabilityTransient Durability = "transient"

	PayloadForbidden         PayloadPolicy = "forbidden"
	PayloadOptional          PayloadPolicy = "optional"
	PayloadOptionalSensitive PayloadPolicy = "optional_sensitive"
	PayloadRequired          PayloadPolicy = "required"

	RetentionAudit      RetentionClass = "audit"
	RetentionDiagnostic RetentionClass = "diagnostic"
	RetentionSensitive  RetentionClass = "sensitive"
	RetentionEphemeral  RetentionClass = "ephemeral"

	OTELNone      OTELMapping = "none"
	OTELSpanStart OTELMapping = "span_start"
	OTELSpanEnd   OTELMapping = "span_end"
	OTELEvent     OTELMapping = "event"
	OTELMetric    OTELMapping = "metric"

	PriorityCritical Priority = "critical"
	PriorityNormal   Priority = "normal"
	PriorityBulk     Priority = "bulk"
)

type Traits struct {
	Owner        Owner          `json:"owner"`
	Durability   Durability     `json:"durability"`
	Payload      PayloadPolicy  `json:"payload"`
	Retention    RetentionClass `json:"retention"`
	Correlations []string       `json:"correlations"`
	OTEL         OTELMapping    `json:"otel"`
	Priority     Priority       `json:"priority"`
}

func TraitsFor(kind Kind) (Traits, bool) {
	value, ok := traitsFor(kind)
	if !ok {
		return Traits{}, false
	}
	value.Correlations = append([]string(nil), value.Correlations...)
	return value, true
}

func traitsFor(kind Kind) (Traits, bool) {
	value, ok := observationTraits[kind]
	return value, ok
}

func Kinds() []Kind {
	return append([]Kind(nil), observationKinds...)
}

func PriorityFor(kind Kind) (Priority, bool) {
	value, ok := traitsFor(kind)
	return value.Priority, ok
}

func PayloadPolicyFor(kind Kind) (PayloadPolicy, bool) {
	value, ok := traitsFor(kind)
	return value.Payload, ok
}
