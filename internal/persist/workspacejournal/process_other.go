//go:build !unix

package workspacejournal

// processAlive has no portable answer here, so it says "alive". The cost is that
// recovery on this platform waits for a person to act instead of undoing an
// interrupted turn; the alternative — assuming a process is gone and rolling back
// underneath it — corrupts a live workspace.
func processAlive(int) bool { return true }
