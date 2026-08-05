package repoindex

import "strings"

// manifests are the files that answer "how is this built". The repository map
// reads them as orientation; path classification reads them as configuration.
// One list keeps the two answers from disagreeing.
var manifests = map[string]struct{}{
	"go.mod": {}, "go.work": {}, "package.json": {}, "Cargo.toml": {},
	"pyproject.toml": {}, "setup.py": {}, "requirements.txt": {},
	"Makefile": {}, "pom.xml": {}, "build.gradle": {}, "build.gradle.kts": {},
	"CMakeLists.txt": {}, "BUILD.bazel": {}, "Gemfile": {}, "composer.json": {},
}

// configExtensions are suffixes that mean configuration wherever they appear.
// Generic data formats are deliberately absent: a .json or .csv file is as often
// a fixture as a setting, and a wrong label is worse than none.
var configExtensions = []string{
	".toml", ".yaml", ".yml", ".ini", ".cfg", ".conf", ".properties", ".env",
}

// IsBuildManifest reports whether name, a base name, is a build manifest.
func IsBuildManifest(name string) bool {
	_, found := manifests[name]
	return found
}

// IsConfigPath reports whether a path holds build or configuration rather than
// code. Like IsTestPath the judgement is made from the name alone, so it is a
// hint and not a fact: a Go file that only holds settings still reads as code.
func IsConfigPath(path string) bool {
	_, name := splitPath(path)
	if IsBuildManifest(name) {
		return true
	}
	if name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile.") {
		return true
	}
	for _, extension := range configExtensions {
		if strings.HasSuffix(name, extension) {
			return true
		}
	}
	return false
}
