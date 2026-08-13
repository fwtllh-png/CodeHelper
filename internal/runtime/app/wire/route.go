package wire

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
)

func resolveExecRoute(options execRouteOptions) (model.ReadyRoute, error) {
	if options.ProviderID == "" || options.ModelID == "" {
		return model.ReadyRoute{}, errors.New("--provider and --model are required without --provider-fixture")
	}
	var catalog *model.Catalog
	if options.BaseURL == "" {
		catalog = model.DefaultCatalog()
	} else {
		provenance := model.ProvenanceCLI
		if options.Fixture {
			provenance = model.ProvenanceFixture
		}
		kind := model.ProviderCustom
		if options.ProviderID == "local" {
			kind = model.ProviderLocal
		}
		credential := model.CredentialRef{}
		if options.APIKeyEnv != "" {
			credential = model.CredentialRef{Kind: "env", Name: options.APIKeyEnv}
		}
		if options.Model == nil {
			return model.ReadyRoute{}, errors.New("custom endpoint requires explicit model metadata")
		}
		descriptor := *options.Model
		if descriptor.ID != options.ModelID {
			return model.ReadyRoute{}, errors.New("custom model metadata id does not match --model")
		}
		var err error
		catalog, err = model.NewCatalog(model.Provider{
			ID: options.ProviderID, Kind: kind, Endpoint: options.BaseURL,
			Protocol: options.Protocol, Credential: credential, Provenance: provenance,
			Models: map[string]model.Model{options.ModelID: descriptor},
		})
		if err != nil {
			return model.ReadyRoute{}, err
		}
	}
	resolver, err := model.NewResolver(catalog)
	if err != nil {
		return model.ReadyRoute{}, err
	}
	provenance := model.ProvenanceCLI
	if options.Fixture {
		provenance = model.ProvenanceFixture
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: options.ProviderID, ModelID: options.ModelID, Provenance: provenance,
	})
	if err != nil {
		return model.ReadyRoute{}, err
	}
	if options.Credential.Kind != "" || options.Credential.Name != "" {
		route = route.WithCredential(options.Credential)
	}
	return route, nil
}

func fixtureModel(id string) *model.Model {
	return &model.Model{
		ID: id, CanonicalID: id, WireID: id,
		Limits: model.Limits{ContextTokens: 1_000_000, MaxOutputTokens: 64_000},
		Capabilities: model.Capabilities{
			Streaming: true, Reasoning: true, ToolCalls: true, NativeSearch: true,
			Vision: true, ImageInput: true, PromptCache: true,
		},
		Pricing: model.Pricing{
			CachedInputPerMillion: new(float64),
			Currency:              "USD", Known: true, Provenance: model.ProvenanceFixture,
		},
		MetadataProvenance: model.MetadataProvenance{
			CanonicalID: model.ProvenanceFixture, WireID: model.ProvenanceFixture,
			Limits: model.ProvenanceFixture, Capabilities: model.ProvenanceFixture,
			Pricing: model.ProvenanceFixture,
		},
		Provenance: model.ProvenanceFixture,
	}
}

func parseProtocol(value string) (model.WireProtocol, error) {
	wireProtocol := model.WireProtocol(value)
	switch wireProtocol {
	case model.ProtocolOpenAIChat, model.ProtocolOpenAIResponses, model.ProtocolAnthropic:
		return wireProtocol, nil
	default:
		return "", fmt.Errorf("unsupported protocol %q", value)
	}
}

func resolveFixturePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	repositoryPath := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", path))
	if _, err := os.Stat(repositoryPath); err != nil {
		return "", err
	}
	return repositoryPath, nil
}

func defaultPromptBudgets() map[string]promptcontext.Budget {
	return map[string]promptcontext.Budget{
		promptcontext.PartitionBase:         {MaxBytes: 32 << 10, MaxTokens: 8 << 10},
		promptcontext.PartitionMode:         {MaxBytes: 1 << 10, MaxTokens: 256},
		promptcontext.PartitionRepository:   {MaxBytes: 128 << 10, MaxTokens: 32 << 10},
		promptcontext.PartitionWorkingSet:   {MaxBytes: 256 << 10, MaxTokens: 64 << 10},
		promptcontext.PartitionSkills:       {MaxBytes: promptcontext.MaxSkillsPromptBytes, MaxTokens: promptcontext.MaxFragmentTokens},
		promptcontext.PartitionConstitution: {MaxBytes: 8 << 10, MaxTokens: promptcontext.MaxFragmentTokens},
		promptcontext.PartitionToolPrefix:   {MaxBytes: 16 << 10, MaxTokens: 4 << 10},
		// The catalog grows with every registered tool, including the MCP and
		// plugin ones a session discovers, so it needs a ceiling of its own.
		promptcontext.PartitionToolCatalog: {MaxBytes: 16 << 10, MaxTokens: 4 << 10},
		// The volatile partitions are sent on every sample rather than once per
		// session, so their ceilings are the tightest of the lot. Configuration
		// overrides them; these are only the fallback when none is supplied.
		promptcontext.PartitionRepoMap:          {MaxBytes: 8 << 10, MaxTokens: 2 << 10},
		promptcontext.PartitionWorkingSetLedger: {MaxBytes: 8 << 10, MaxTokens: 2 << 10},
		promptcontext.PartitionEvidence:         {MaxBytes: 4 << 10, MaxTokens: 1 << 10},
		// The method is a constant in the stable prefix, so its ceiling only has to
		// be larger than the text it carries.
		promptcontext.PartitionCodingPolicy: {MaxBytes: 2 << 10, MaxTokens: 512},
	}
}
