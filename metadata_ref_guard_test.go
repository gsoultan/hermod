package hermod

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoPackageReadsMetadataByReference keeps the metadata map from being read
// without the lock that guards it.
//
// MetadataRef returns the live map. Every write to it goes through SetMetadata,
// which holds m.mu -- so a caller that indexes or ranges the returned map holds
// no lock at all, and an unlocked read against a locked write is a race the
// runtime turns into `fatal error: concurrent map read and map write`, which no
// recover() can contain.
//
// All four call sites were reachable from a message held by more than one
// goroutine at once: runner.go fans a message out to its sinks with swg.Go, one
// goroutine per target, while the sinks' workers write delivery markers and
// trace lineage back onto it.
//
// MetadataValue is the locked single-key read, and it does not clone -- which was
// the reason MetadataRef was reached for in the first place. There is no cost
// left to trade against, so a new call site is a mistake rather than a decision.
// Use hermod.MetadataValue(msg, key).
//
// This deliberately walks the whole module rather than one package: the guards
// in pkg/infra/schema globbed their own directory and a second importer slipped
// past them, so that lesson is applied here up front. Method declarations are
// not matched -- only calls -- so implementing the interface stays legal.
func TestNoPackageReadsMetadataByReference(t *testing.T) {
	// Directories that are not this module's Go source.
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "ui": true,
		"graphify-out": true, "audit-shots": true, "uploads": true,
	}

	fset := token.NewFileSet()

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			// A file that does not parse cannot be calling anything. Build
			// failures are a different test's job.
			return nil //nolint:nilerr // parse failure is not a guard violation
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "MetadataRef" {
				return true
			}
			t.Errorf("%s:%d: reads the metadata map by reference.\n"+
				"MetadataRef hands out the live map and every write to it goes through "+
				"the locked SetMetadata, so indexing or ranging the result races that "+
				"write -- and the runtime makes a racing map read a fatal error no "+
				"recover() can contain. A message is held by several goroutines at once "+
				"whenever it fans out to more than one sink. Read the one key you want "+
				"with hermod.MetadataValue(msg, key): it takes the read lock and does "+
				"not clone the map.",
				path, fset.Position(sel.Pos()).Line)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
}
