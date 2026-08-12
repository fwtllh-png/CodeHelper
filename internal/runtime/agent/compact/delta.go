package compact

type FailureDelta struct {
	Failures []Failure `json:"failures,omitempty"`
}

func (f *Failures) Delta() FailureDelta {
	return FailureDelta{Failures: f.List()}
}

func ApplyFailureDelta(delta FailureDelta) *Failures {
	result := NewFailures()
	for index := len(delta.Failures) - 1; index >= 0; index-- {
		failure := delta.Failures[index]
		key := failure.Kind + "\x00" + failure.Name + "\x00" + failure.Reason
		copy := failure
		result.records[key] = &copy
		result.order = append(result.order, key)
	}
	return result
}
