package extension

import (
	"sort"
	"sync"
	"time"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

const maxExtensionTraces = 256

type controlObservability struct {
	mu      sync.Mutex
	metrics protocol.ExtensionControlMetrics
	traces  []protocol.ExtensionControlTrace
	alerts  map[string]protocol.ExtensionControlAlert
}

func newControlObservability() *controlObservability {
	return &controlObservability{
		alerts: make(map[string]protocol.ExtensionControlAlert),
	}
}

func (o *controlObservability) begin() time.Time {
	o.mu.Lock()
	o.metrics.Operations++
	o.mu.Unlock()
	return time.Now()
}

func (o *controlObservability) finish(
	operation protocol.ExtensionControlOperation,
	started time.Time,
	status, alertCode string,
) {
	now := time.Now().UTC()
	o.mu.Lock()
	defer o.mu.Unlock()
	switch status {
	case "committed", "reconciled":
		o.metrics.Committed++
		if operation.Action == protocol.ExtensionActionRevoke {
			o.metrics.Revokes++
		}
	case "duplicate":
		o.metrics.Duplicates++
	default:
		o.metrics.Failed++
	}
	o.traces = append(o.traces, protocol.ExtensionControlTrace{
		OperationID: operation.ID, Action: operation.Action, Kind: operation.Kind,
		Status: status, DurationMS: uint64(max(0, time.Since(started).Milliseconds())),
		OccurredAt: now,
	})
	if len(o.traces) > maxExtensionTraces {
		o.traces = append(
			[]protocol.ExtensionControlTrace(nil),
			o.traces[len(o.traces)-maxExtensionTraces:]...,
		)
	}
	if alertCode != "" {
		alert := o.alerts[alertCode]
		alert.Code = alertCode
		alert.Count++
		alert.LastSeenAt = now
		o.alerts[alertCode] = alert
	}
}

func (o *controlObservability) subscriberDropped() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.metrics.SubscriberDrops++
	alert := o.alerts["subscriber_overflow"]
	alert.Code = "subscriber_overflow"
	alert.Count++
	alert.LastSeenAt = time.Now().UTC()
	o.alerts[alert.Code] = alert
}

func (o *controlObservability) snapshot() protocol.ExtensionControlDiagnostics {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := protocol.ExtensionControlDiagnostics{
		Metrics: o.metrics,
		Traces:  append([]protocol.ExtensionControlTrace(nil), o.traces...),
		Alerts:  make([]protocol.ExtensionControlAlert, 0, len(o.alerts)),
	}
	for _, alert := range o.alerts {
		result.Alerts = append(result.Alerts, alert)
	}
	sort.Slice(result.Alerts, func(i, j int) bool {
		return result.Alerts[i].Code < result.Alerts[j].Code
	})
	return result
}
