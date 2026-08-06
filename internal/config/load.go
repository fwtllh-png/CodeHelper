package config

import (
	"os"
)

type LoadOptions struct {
	Path string
	// RepoPath is an untrusted project-local config (N19). When set, it is
	// applied after Path but cannot override credential/provider/model/protocol.
	RepoPath string
	// TrustRepo when true allows RepoPath to set denylisted fields (explicit opt-in).
	TrustRepo bool
	LookupEnv func(string) (string, bool)
	Overrides Overrides
}

func Load(options LoadOptions) (Snapshot, error) {
	config := Defaults()
	provenance := defaultProvenance()

	if options.Path != "" {
		if err := applyFile(options.Path, &config, provenance, SourceFile, true); err != nil {
			return Snapshot{}, err
		}
	}
	if options.RepoPath != "" {
		if err := applyFile(
			options.RepoPath, &config, provenance, SourceRepo, options.TrustRepo,
		); err != nil {
			return Snapshot{}, err
		}
	}
	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if err := applyEnvironment(lookupEnv, &config, provenance); err != nil {
		return Snapshot{}, err
	}
	applyOverrides(options.Overrides, &config, provenance)
	normalizeRoutes(&config, provenance)

	snapshot := Snapshot{Config: config, Provenance: provenance}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// normalizeRoutes folds the [vision] section into the route table.
//
// [vision] predates per-purpose routing and stays as an alias for a release
// cycle, because deleting it would leave an upgraded configuration silently
// without vision. The alias only fills a slot nobody named, so [route.vision]
// wins whenever both are present.
func normalizeRoutes(config *Config, provenance map[string]Source) {
	vision := config.Vision
	if !vision.Enabled || vision.Provider == "" || vision.Model == "" {
		return
	}
	if _, configured := config.Route.Slots["vision"]; configured {
		return
	}
	if config.Route.Slots == nil {
		config.Route.Slots = make(map[string]RouteSlot)
	}
	config.Route.Slots["vision"] = RouteSlot{Provider: vision.Provider, Model: vision.Model}

	if source, known := provenance[fieldVisionProvider]; known {
		provenance[fieldRouteProvider("vision")] = source
	}
	if source, known := provenance[fieldVisionModel]; known {
		provenance[fieldRouteModel("vision")] = source
	}
}
