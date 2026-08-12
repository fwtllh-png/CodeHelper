package process

import "os"

// ManagedGitArguments disables background maintenance owned outside a Session.
func ManagedGitArguments(arguments []string) []string {
	return append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "maintenance.auto=false",
		"-c", "gc.auto=0",
	}, arguments...)
}

// GitExecutable avoids Apple's shim, which needs xcrun cache writes that a
// sandboxed command cannot make.
func GitExecutable() string {
	for _, candidate := range []string{
		"/Library/Developer/CommandLineTools/usr/bin/git",
		"/Applications/Xcode.app/Contents/Developer/usr/bin/git",
	} {
		if info, err := os.Stat(candidate); err == nil &&
			info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate
		}
	}
	return "git"
}
