// Command schemagen writes the protocol JSON Schema to a file. It exists so the
// committed artifact under docs/protocol is produced by the same code the drift
// test compares against, rather than by hand.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: schemagen <output path>")
		os.Exit(2)
	}
	data, err := protocol.MarshalSchema()
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate schema:", err)
		os.Exit(1)
	}
	path := os.Args[1]
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "create output directory:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write schema:", err)
		os.Exit(1)
	}
}
