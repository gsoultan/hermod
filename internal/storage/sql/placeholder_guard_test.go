package sql

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every statement this package runs is written with `?` placeholders. Only
// prepareQuery rewrites them to `$1, $2, ...` for pgx, and the four wrappers
// below are the only things that call it.
var preparedWrappers = map[string]bool{
	"query":    true,
	"queryRow": true,
	"exec":     true,
}

// Reaching for s.db directly skips that rewrite, so the statement is shipped to
// PostgreSQL with literal `?` and fails with "syntax error at end of input".
//
// This is invisible to the rest of this package's tests because they run on
// sqlite, where `?` is the native placeholder — every one of them passes on a
// statement that cannot execute in production. GetWorkspace was written this
// way and stayed broken on PostgreSQL for as long as workspaces existed: its
// callers all treated the error as "no quota configured", so the quota simply
// never applied there and nothing reported it.
//
// The guard is structural rather than behavioural for that reason: catching it
// at runtime needs a live PostgreSQL, and by then the check it guards has
// already been silently skipped.
func TestNoQueryBypassesPlaceholderPreparation(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing package files: %v", err)
	}

	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		checked++

		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}
			// The wrappers are where the direct calls belong.
			if preparedWrappers[fn.Name.Name] {
				return false
			}
			ast.Inspect(fn, func(inner ast.Node) bool {
				call, ok := inner.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "QueryContext", "QueryRowContext", "ExecContext":
				default:
					return true
				}
				// Only s.db.X(...) is the mistake; tx.X(...) inside an explicit
				// transaction prepares its own statements.
				recv, ok := sel.X.(*ast.SelectorExpr)
				if !ok || recv.Sel.Name != "db" {
					return true
				}
				// (ctx, query) with nothing to bind is DDL or a PRAGMA — there
				// is no placeholder to rewrite, so it is not this mistake.
				if len(call.Args) <= 2 {
					return true
				}
				// An inline s.prepareQuery(...) does the rewrite by hand.
				if inner, ok := call.Args[1].(*ast.CallExpr); ok {
					if fn, ok := inner.Fun.(*ast.SelectorExpr); ok && fn.Sel.Name == "prepareQuery" {
						return true
					}
				}
				t.Errorf("%s: %s calls s.db.%s directly, which skips prepareQuery — "+
					"the `?` placeholders reach PostgreSQL unrewritten and the statement fails "+
					"with \"syntax error at end of input\". Use s.query, s.queryRow or s.exec.",
					fset.Position(call.Pos()), fn.Name.Name, sel.Sel.Name)
				return true
			})
			return false
		})
	}

	if checked == 0 {
		t.Fatal("the guard parsed no files, so it proves nothing")
	}
}
