//go:build webbundle

package web

import (
	"embed"
	"io/fs"
)

//go:embed dist
var assets embed.FS

// Assets returns the immutable production Web bundle rooted at dist.
func Assets() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}
