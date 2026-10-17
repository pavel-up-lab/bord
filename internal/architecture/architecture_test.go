package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDependenciesPointInward(t *testing.T) {
	root := filepath.Clean("../..")
	for _, layer := range []string{"domain", "app"} {
		dir := filepath.Join(root, "internal", layer)
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() {
				return nil
			}
			files := token.NewFileSet()
			pkgs, err := parser.ParseDir(files, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, pkg := range pkgs {
				for _, file := range pkg.Files {
					for _, imp := range file.Imports {
						importPath, _ := strconv.Unquote(imp.Path.Value)
						if !strings.HasPrefix(importPath, "bord/internal/") {
							continue
						}
						isDomain := importPath == "bord/internal/domain" || strings.HasPrefix(importPath, "bord/internal/domain/")
						if layer == "domain" || !isDomain {
							t.Errorf("%s: %s must not import adapter/internal package %q", files.Position(imp.Pos()), layer, importPath)
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
