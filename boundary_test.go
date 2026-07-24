package stremio_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestModuleImportsOnlyPublishedContracts is the module boundary made
// executable: the Stremio module must use only the published *contract* modules
// — the SDK (mosaic-sdk) and the shared SDUI contract (mosaic-sdui, which a
// module contributes settings UI with, ADR 0038) — and the standard library. It
// is a separate Go module, so Go itself already rejects a Platform-internal
// import; this parse keeps the intent explicit and catches a third-party
// dependency creeping in too (ADR 0008, ADR 0016, ADR 0025).
func TestModuleImportsOnlyPublishedContracts(t *testing.T) {
	const (
		sdkPrefix      = "github.com/mosaic-media/sdk/"
		sduiPrefix     = "github.com/mosaic-media/contracts/"
		platformPrefix = "github.com/mosaic-media/platform/"
		// This module's own path. cmd/ imports the capability it serves, which
		// is the one self-import there is and is not a boundary crossing.
		selfPath = "github.com/mosaic-media/module-stremio-addons"
	)

	// Walked rather than a flat ReadDir of ".". The module gained a cmd/
	// directory when it learned to run as its own process (ADR 0064), and a
	// check that only looked at the root would have declared the boundary clean
	// while never reading the one file that imports the harness.
	var sources []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		sources = append(sources, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, name := range sources {
		checked++

		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", name, err)
			}
			switch {
			// Standard-library imports have no dot in their first segment.
			case !strings.Contains(strings.SplitN(path, "/", 2)[0], "."):
			case path == selfPath || strings.HasPrefix(path, selfPath+"/"):
				// The module importing itself: cmd/ serving the capability.
			case strings.HasPrefix(path, sdkPrefix):
				// The published SDK — the primary contract a module builds
				// against. sdk/host sits under this prefix, which is correct:
				// the harness is published beside the contract precisely so a
				// module needs no dependency the SDK did not already sanction
				// (ADR 0064).
			case strings.HasPrefix(path, sduiPrefix):
				// The shared SDUI contract — a module builds its own settings UI
				// with the producer binding (ADR 0038, ADR 0025).
			case strings.HasPrefix(path, platformPrefix):
				t.Errorf("%s imports private Platform package %q; a module may import only the SDK", name, path)
			default:
				t.Errorf("%s imports third-party package %q; the Stremio module may use only the SDK and the standard library", name, path)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no non-test source files were checked; the boundary test is not looking at anything")
	}
}
