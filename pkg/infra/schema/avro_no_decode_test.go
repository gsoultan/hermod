package schema

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSchemaPackageNeverDecodesAvro holds up the security exemption recorded in
// scripts/govulncheck.sh.
//
// github.com/hamba/avro/v2 carries three unfixed denial-of-service advisories
// (GO-2026-5046/5047/5048). All three live in the decoder: a hostile Avro
// stream declares an enormous array or map block count and the decoder either
// spins on it or allocates until the process is killed. All three need
// untrusted Avro input to be *decoded*.
//
// Hermod does not decode Avro. It parses a schema and marshals a map to
// validate it — encode only, no reader, no stream — which is why the advisories
// are accepted rather than blocking the build. That argument is only as good as
// it stays true, and "nobody will add an avro.Unmarshal" is not a control. This
// test is the control: introduce any decode entry point and the build fails,
// with a pointer to the exemption that has just stopped being valid.
//
// If Hermod ever genuinely needs to decode Avro, this test failing is the
// correct outcome. Do not delete it — remove the exemption, and bound the
// decode with Config.MaxMapAllocSize and a size cap on the reader.
func TestSchemaPackageNeverDecodesAvro(t *testing.T) {
	// Entry points into the vulnerable decoder paths.
	forbidden := map[string]string{
		"Unmarshal":     "decodes an Avro payload",
		"NewDecoder":    "constructs a stream decoder",
		"NewReader":     "constructs a stream reader",
		"NewDecoderFor": "constructs a stream decoder",
	}

	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving package dir: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(root, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		// Find the local name the avro module is imported under, if at all.
		alias := ""
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.Contains(path, "hamba/avro") {
				continue
			}
			alias = "avro"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
		}
		if alias == "" {
			continue
		}

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
				pos := fset.Position(sel.Pos())
				t.Errorf("%s:%d: %s.%s %s.\n"+
					"hamba/avro has three unfixed decoder denial-of-service advisories "+
					"(GO-2026-5046/5047/5048), and they are exempted in "+
					"scripts/govulncheck.sh precisely because Hermod never decodes Avro. "+
					"Adding a decode path invalidates that exemption: remove it, and bound "+
					"the decode with Config.MaxMapAllocSize and a reader size cap.",
					name, pos.Line, alias, sel.Sel.Name, why)
			}
			return true
		})
	}
}

// TestAvroValidatorRejectsAHostileSchemaWithoutHanging is the abuse case for the
// one piece of attacker-influenced input that does reach the library: the schema
// string. Schemas arrive from the schema registry, so a caller with write access
// there controls this value. Parsing must fail cleanly rather than hang or
// panic.
func TestAvroValidatorRejectsAHostileSchemaWithoutHanging(t *testing.T) {
	hostile := []struct {
		name   string
		schema string
	}{
		{"empty", ""},
		{"not json", "{{{{"},
		{"unknown type", `{"type":"nonsense"}`},
		{"deeply nested", `{"type":"array","items":` + strings.Repeat(`{"type":"array","items":`, 200) + `"string"` + strings.Repeat("}", 200) + `}`},
		{"huge name", `{"type":"record","name":"` + strings.Repeat("a", 100000) + `","fields":[]}`},
		{"null bytes", "{\"type\":\"record\",\"name\":\"a\x00b\",\"fields\":[]}"},
	}

	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("parsing a hostile schema panicked: %v", r)
					}
				}()
				// An error is the expected outcome; a hang or a panic is not.
				_, _ = NewAvroValidator(tc.schema)
			}()

			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("parsing a hostile schema did not return; a registry write could wedge the process")
			}
		})
	}
}
