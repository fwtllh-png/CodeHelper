//go:build !linux

package sandbox

import "errors"

func prepareLandlockInvocation(
	Policy,
	string,
	string,
	string,
	[]string,
	[]string,
	bool,
	[]string,
	[]string,
) (string, string, error) {
	return "", "", errors.New("Landlock helper is only available on Linux")
}

func createLandlockRequestRoot() (string, error) {
	return "", errors.New("Landlock helper is only available on Linux")
}
