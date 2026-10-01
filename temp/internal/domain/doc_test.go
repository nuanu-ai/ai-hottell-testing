package domain_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestPackageImports keeps the domain free of input and output: its code imports the
// standard library and github.com/google/uuid only.
func TestPackageImports(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list package files: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		f, err := parser.ParseFile(fset, name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote import %s: %v", name, imp.Path.Value, err)
			}
			if !allowedImport(path) {
				t.Errorf("%s imports %s: domain may import the standard library and github.com/google/uuid only", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no package files found")
	}
}

// allowedImport reports whether path is in the standard library, whose first path
// element has no dot, or is github.com/google/uuid.
func allowedImport(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".") || path == "github.com/google/uuid"
}
