package web

import (
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

//go:embed web-operation-exposure.json
var operationExposureContract []byte

type operationExposureRegistry struct {
	Version    int `json:"version"`
	Operations []struct {
		Kind            protocol.OperationKind `json:"operation_kind"`
		Disposition     string                 `json:"disposition"`
		IntentSchema    string                 `json:"web_intent_schema"`
		IdentityBinding string                 `json:"identity_binding"`
		AdmissionPolicy string                 `json:"admission_policy"`
		RequiredSurface string                 `json:"required_surface"`
		Qualification   string                 `json:"qualification"`
	} `json:"operations"`
}

func TestWebOperationExposureClassifiesEveryProtocolOperation(t *testing.T) {
	var registry operationExposureRegistry
	if err := json.Unmarshal(operationExposureContract, &registry); err != nil {
		t.Fatal(err)
	}
	if registry.Version != 1 {
		t.Fatalf("exposure registry version = %d", registry.Version)
	}
	kinds := protocol.OperationKinds()
	if len(webOperationExposure) != len(kinds) ||
		len(registry.Operations) != len(kinds) {
		t.Fatalf(
			"Web operation exposure has code=%d registry=%d entries, protocol has %d",
			len(webOperationExposure), len(registry.Operations),
			len(kinds),
		)
	}
	registered := make(map[protocol.OperationKind]bool, len(registry.Operations))
	for _, entry := range registry.Operations {
		if registered[entry.Kind] {
			t.Fatalf("duplicate operation %q", entry.Kind)
		}
		registered[entry.Kind] = true
		exposed, classified := webOperationExposure[entry.Kind]
		if !classified {
			t.Errorf("registered operation %q is unknown to the Web Host", entry.Kind)
			continue
		}
		if entry.Qualification == "" || entry.IdentityBinding == "" ||
			entry.AdmissionPolicy == "" {
			t.Errorf("operation %q has incomplete qualification metadata", entry.Kind)
		}
		switch entry.Disposition {
		case "exposed":
			if !exposed || entry.IntentSchema == "" || entry.RequiredSurface == "" {
				t.Errorf("operation %q has an incomplete exposed contract", entry.Kind)
			}
		case "denied":
			if exposed || entry.IntentSchema != "" || entry.RequiredSurface != "" ||
				entry.AdmissionPolicy != "deny" {
				t.Errorf("operation %q has an invalid denied contract", entry.Kind)
			}
		default:
			t.Errorf("operation %q has disposition %q", entry.Kind, entry.Disposition)
		}
	}
	for _, kind := range kinds {
		if _, classified := webOperationExposure[kind]; !classified {
			t.Errorf("operation %q has no explicit Web exposure decision", kind)
		}
		if !registered[kind] {
			t.Errorf("operation %q is missing from the exposure registry", kind)
		}
	}
}
