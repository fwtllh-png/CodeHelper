package acp

import (
	"slices"
	"testing"
)

func TestCompatibilityManifestMatchesAdvertisedACP(t *testing.T) {
	if protocolVersion != compatibilityManifest.ACPProtocol.Max ||
		minProtocolVersion != compatibilityManifest.ACPProtocol.Min {
		t.Fatalf(
			"ACP versions = %d..%d, compatibility = %d..%d",
			minProtocolVersion,
			protocolVersion,
			compatibilityManifest.ACPProtocol.Min,
			compatibilityManifest.ACPProtocol.Max,
		)
	}
	for _, method := range compatibilityManifest.RequiredMethods {
		if !slices.Contains(methods, method) {
			t.Fatalf("required compatibility method %q is not advertised", method)
		}
	}
}
