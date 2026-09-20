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

// TestNoPackageDecodesAvro is the module-wide form of the control that
// pkg/infra/schema has held since the hamba/avro advisories were accepted.
//
// GO-2026-5046 / 5047 / 5048 (CVE-2026-46385) are denial-of-service flaws in
// github.com/hamba/avro/v2's array and map decoders: a record declaring a block
// of up to math.MaxInt64 elements followed by a truncated body makes the
// decoder spin over that count without re-checking the reader's error state,
// pinning a CPU core until the process is killed. The module is archived
// upstream and every published version through v2.31.0 is affected; the only
// patched code is a third-party fork.
//
// scripts/govulncheck.sh accepts all three advisories on one ground: Hermod
// never decodes Avro. It parses schemas and marshals its own maps, both of
// which are encode-side.
//
// The existing guards in pkg/infra/schema enforce that ground *within that one
// package* — they glob "*.go" in their own directory. That was sufficient while
// pkg/infra/schema was the only importer of the library. It stopped being
// sufficient when pkg/comm/formatter/schemaregistry began importing it to
// encode Confluent-framed records: a decode call added there would invalidate
// the exemption with nothing failing.
//
// This test closes that hole by walking the whole module. If Avro decoding is
// ever genuinely needed — reading a Confluent-framed Avro topic is the obvious
// reason, and it is a real gap — the correct sequence is to migrate off the
// archived module first, not to delete this test. The advisory's own
// remediation names github.com/iskorotkov/avro/v2 >= v2.33.0 as the maintained
// fork carrying the fix.
func TestNoPackageDecodesAvro(t *testing.T) {
	const avroModule = "github.com/hamba/avro/v2"

	// Entry points that reach the vulnerable decoder. Kept as the union of the
	// two lists the pkg/infra/schema guards use, so widening the scope does not
	// narrow the coverage.
	forbidden := map[string]string{
		"Unmarshal":           "decodes an Avro payload",
		"NewDecoder":          "constructs a stream decoder",
		"NewDecoderFor":       "constructs a stream decoder",
		"NewDecoderForSchema": "constructs a stream decoder",
		"NewReader":           "constructs a stream reader",
		"Read":                "reads from an Avro stream",
	}

	// Directories that are not this module's Go source.
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "ui": true,
		"graphify-out": true, "audit-shots": true, "uploads": true,
	}

	fset := token.NewFileSet()
	var checked int

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

		// Find the local name hamba/avro is imported under, if at all.
		alias := ""
		for _, imp := range file.Imports {
			if strings.Trim(imp.Path.Value, `"`) != avroModule {
				continue
			}
			alias = "avro"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
		}
		if alias == "" {
			return nil
		}
		checked++

		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != alias {
				return true
			}
			if why, bad := forbidden[sel.Sel.Name]; bad {
				t.Errorf("%s:%d: %s.%s %s.\n"+
					"github.com/hamba/avro/v2 has three unfixed decoder denial-of-service "+
					"advisories (GO-2026-5046/5047/5048), and scripts/govulncheck.sh exempts "+
					"them only because Hermod never decodes Avro. This call invalidates that "+
					"exemption. The module is archived with no patched release: migrate to "+
					"github.com/iskorotkov/avro/v2 >= v2.33.0, bound the decode with "+
					"Config.MaxMapAllocSize and a reader size cap, and remove the exemption "+
					"— do not delete this test.",
					path, fset.Position(sel.Pos()).Line, alias, sel.Sel.Name, why)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}

	// If the import disappears entirely this test silently stops testing
	// anything, and the exemption in scripts/govulncheck.sh becomes dead
	// config that nobody notices. Either is worth knowing about.
	if checked == 0 {
		t.Errorf("no non-test file imports %s — if the dependency is gone, "+
			"remove the exemptions from scripts/govulncheck.sh and delete this guard",
			avroModule)
	}
}
