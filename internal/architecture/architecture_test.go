package architecture

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDependenciesPointInward(t *testing.T) {
	root := filepath.Clean("../..")
	for _, layer := range []string{"domain", "app"} {
		dir := filepath.Join(root, "internal", layer)
		pkgs, err := parser.ParseDir(token.NewFileSet(), dir, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				for _, imp := range file.Imports {
					path, _ := strconv.Unquote(imp.Path.Value)
					if !strings.HasPrefix(path, "bord/internal/") {
						continue
					}
					if layer == "domain" || !strings.HasPrefix(path, "bord/internal/domain") {
						pos := imp.Pos()
						_ = pos
						t.Errorf("%s must not import adapter/internal package %q", layer, path)
					}
				}
			}
		}
	}
}
