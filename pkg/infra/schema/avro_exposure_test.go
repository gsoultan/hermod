package schema

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// avroDecodeAPIs are the entry points into the vulnerable code path.
//
// GO-2026-5046 / 5047 / 5048 (CVE-2026-46385) are denial-of-service flaws in
// github.com/hamba/avro/v2's array and map *decoders*: a payload declaring a
// block of up to math.MaxInt64 elements followed by a truncated body makes the
// decoder spin over that count without re-checking the reader's error state,
// pinning a CPU core until the process is killed. There is no fixed release of
// hamba/avro — the advisory lists every version as affected and no upstream fix
// exists (confirmed against the module's published versions, latest v2.31.0).
//
// Hermod is not exposed today, because it never decodes Avro. It parses
// operator-supplied schemas and marshals its own maps, and both directions of
// that are encode-side. That is a property of how the library is used, not a
// property of the library, so it holds only until someone adds a decode call.
//
// This test is the control that keeps the risk acceptance honest. If Avro
// decoding is introduced, this fails, and whoever adds it has to deal with the
// exposure — by bounding the input, imposing a decode deadline, or moving to a
// patched fork — rather than inheriting an assessment that quietly stopped
// being true.
var avroDecodeAPIs = []string{
	"Unmarshal",
	"NewDecoder",
	"NewDecoderForSchema",
	"NewReader",
	"Read",
}

func TestNoUntrustedAvroDecoding(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}

		// Find the local name the avro package is imported under, if at all.
		avroName := ""
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) != "github.com/hamba/avro/v2" {
				continue
			}
			avroName = "avro"
			if imp.Name != nil {
				avroName = imp.Name.Name
			}
		}
		if avroName == "" {
			continue
		}

		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != avroName {
				return true
			}
			for _, banned := range avroDecodeAPIs {
				if sel.Sel.Name == banned {
					t.Errorf("%s:%d: avro.%s reaches the decoder affected by CVE-2026-46385, which has no upstream fix. "+
						"A hostile payload can pin a CPU core until the process is killed. Bound the input and impose a decode "+
						"deadline, or move to a patched fork, before adding this.",
						file, fset.Position(sel.Pos()).Line, sel.Sel.Name)
				}
			}
			return true
		})
	}
}
