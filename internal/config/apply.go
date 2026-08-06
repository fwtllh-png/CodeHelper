package config

import (
	"time"
)

func applyInt(value *int, target *int, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyInt64(value *int64, target *int64, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyUint64(value *uint64, target *uint64, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyFloat64(value *float64, target *float64, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyBool(value *bool, target *bool, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyDuration(value *time.Duration, target *time.Duration, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyDurationString(value *string, target *time.Duration, field string, source Source, provenance map[string]Source) {
	if value == nil {
		return
	}
	parsed, err := time.ParseDuration(*value)
	if err != nil {
		*target = 0
	} else {
		*target = parsed
	}
	provenance[field] = source
}

func applyString(value *string, target *string, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}
