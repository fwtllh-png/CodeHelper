package protocol

import "testing"

func TestSessionToolCatalogValidatesFiniteUnifiedSources(t *testing.T) {
	catalog := SessionToolCatalog{
		Version: SessionToolCatalogVersion, CatalogID: "catalog-1",
		Generation: 2, Digest: "digest-2",
		Tools: []SessionToolCatalogEntry{{
			ID: "builtin:file_read", Name: "file_read", Description: "Read a file",
			SourceKind: "builtin", SourceLabel: "QCode",
			Capability: "read", AccessMode: "read",
			RiskLevel: "low", SandboxRequirement: "none",
			PolicyState: "deferred", PolicyReason: "requires validated resources",
			ConstitutionState: "deferred", ConstitutionReason: "enforced at call time",
			Availability: "available",
			State:        "eager", Revision: 1, Enabled: true, Guarded: true,
		}},
	}
	if err := catalog.Validate(); err != nil {
		t.Fatal(err)
	}
	catalog.Tools[0].SourceKind = "forged"
	if err := catalog.Validate(); err == nil {
		t.Fatal("invalid source kind was accepted")
	}
}
