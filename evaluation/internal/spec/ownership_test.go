package spec

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionGoPackagesDoNotImportEvaluation(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	const evaluationImport = "github.com/fwtllh-png/CodeHelper/evaluation"
	for _, directory := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(
			filepath.Join(root, directory),
			func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() || filepath.Ext(path) != ".go" {
					return nil
				}
				file, err := parser.ParseFile(
					token.NewFileSet(),
					path,
					nil,
					parser.ImportsOnly,
				)
				if err != nil {
					return err
				}
				for _, imported := range file.Imports {
					value, err := strconv.Unquote(imported.Path.Value)
					if err != nil {
						return err
					}
					if value == evaluationImport ||
						strings.HasPrefix(value, evaluationImport+"/") {
						t.Errorf(
							"production package %s imports evaluation package %s",
							path,
							value,
						)
					}
				}
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}
