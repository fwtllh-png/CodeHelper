//go:build !webbundle

package web

import (
	"errors"
	"io/fs"
)

// Assets fails closed when callers bypass the repository build entry point.
func Assets() (fs.FS, error) {
	return nil, errors.New("web assets are not embedded; run make build")
}
