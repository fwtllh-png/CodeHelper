//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package cas

import (
	"context"
	"os"
)

func lockFile(ctx context.Context, _ *os.File, _ bool) error {
	return ctx.Err()
}

func unlockFile(_ *os.File) error {
	return nil
}
