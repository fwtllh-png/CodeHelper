//go:build !darwin

package sandbox

func executableRuntimeDependencies(string) ([]string, []string) {
	return nil, nil
}
