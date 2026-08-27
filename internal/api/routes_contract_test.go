package api

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The HTTP API is Hermod's real contract, and nothing was watching it.
//
// CLAUDE.md lists `buf lint` and `buf breaking` as gates, but the repository
// has no buf.yaml and the two .proto files in it are internal fixtures — the
// surface every client actually depends on is this HTTP API, and it had no
// breaking-change check of any kind. A route could be renamed, moved or
// deleted and the first report would be a 404 from the UI, or from somebody
// else's script.
//
// So the route set is inventoried and checked in. This does not describe
// request or response shapes; it holds the one property that is cheap to hold
// and expensive to lose: **the set of paths cannot change without somebody
// saying so**. Adding a route is a one-line golden update. Removing or
// renaming one is the same edit, made deliberately, in a diff a reviewer can
// see.
//
// Routes are read out of the source rather than off a live mux because
// http.ServeMux cannot be enumerated: it accepts patterns and never hands them
// back. Registering through an interface purely so a test could record them
// would mean changing seventeen production signatures to observe something the
// source already states plainly.

const routesGolden = "testdata/routes.golden"

// update rewrites the golden instead of asserting against it. Deliberately a
// flag rather than an env var, so regenerating is something you type on
// purpose and not something a stray export does for you.
var update = flag.Bool("update", false, "rewrite "+routesGolden+" from the current source")

// routeDirs are the packages that register HTTP routes. A new transport
// package must be added here, and the guard below fails if one exists that is
// not listed — an unlisted package is a whole family of routes silently
// outside the inventory, which is the failure this test exists to prevent.
var routeDirs = []string{
	"internal/api",
	"internal/approval/transport/http",
	"internal/auth/transport/http",
	"internal/dashboard/transport/http",
	"internal/files/transport/http",
	"internal/forms/transport/http",
	"internal/infra/transport/http",
	"internal/logs/transport/http",
	"internal/marketplace/transport/http",
	"internal/schema/transport/http",
	"internal/sink/transport/http",
	"internal/source/transport/http",
	"internal/sse/transport/http",
	"internal/webhooks/transport/http",
	"internal/workflow/transport/http",
	"internal/worker/transport/http",
	"internal/ws/transport/http",
}

// repoRoot is two levels up from internal/api.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s does not look like the repository root: %v", root, err)
	}
	return root
}

// collectRoutes returns every route pattern registered in dir, and every
// registration whose pattern is not a plain string literal.
func collectRoutes(t *testing.T, dir string) (routes, dynamic []string) {
	t.Helper()

	fset := token.NewFileSet()

	// The files are walked and parsed one by one rather than through
	// parser.ParseDir, which is deprecated as of Go 1.25 — and which would tell
	// us about packages when all that is wanted here is every non-test file in
	// one directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") ||
			strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", filepath.Join(dir, e.Name()), err)
		}
		{
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc" {
					return true
				}
				// Only calls on something named like a mux. Without this,
				// unrelated Handle methods on other types would be swept in.
				recv, ok := sel.X.(*ast.Ident)
				if !ok || !strings.Contains(strings.ToLower(recv.Name), "mux") {
					return true
				}

				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					pos := fset.Position(call.Pos())
					dynamic = append(dynamic, fmt.Sprintf("%s:%d", pos.Filename, pos.Line))
					return true
				}
				pattern, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				routes = append(routes, pattern)
				return true
			})
		}
	}
	return routes, dynamic
}

func allRoutes(t *testing.T) ([]string, []string) {
	t.Helper()
	root := repoRoot(t)

	var routes, dynamic []string
	for _, d := range routeDirs {
		r, dyn := collectRoutes(t, filepath.Join(root, d))
		routes = append(routes, r...)
		dynamic = append(dynamic, dyn...)
	}
	slices.Sort(routes)
	routes = slices.Compact(routes)
	return routes, dynamic
}

// TestHTTPRouteSetMatchesTheCheckedInContract is the gate. Update
// testdata/routes.golden in the same commit that changes a route, and the diff
// shows a reviewer exactly which part of the public surface moved.
func TestHTTPRouteSetMatchesTheCheckedInContract(t *testing.T) {
	got, dynamic := allRoutes(t)

	if len(dynamic) > 0 {
		t.Errorf("route patterns that are not string literals, so they cannot be inventoried: %v\n"+
			"a computed pattern is a public path nothing is watching; make it a literal, or "+
			"this gate has a hole in exactly the place someone will change next", dynamic)
	}

	if len(got) == 0 {
		t.Fatal("no routes were found at all, which means the extraction broke rather than " +
			"that the API is empty; a passing empty inventory would be worse than no test")
	}

	if *update {
		writeGolden(t, got)
		t.Logf("rewrote %s with %d routes", routesGolden, len(got))
		return
	}

	want := readGolden(t)

	added := missingFrom(got, want)
	removed := missingFrom(want, got)
	if len(added) == 0 && len(removed) == 0 {
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "the HTTP route set no longer matches %s\n", routesGolden)
	if len(removed) > 0 {
		fmt.Fprintf(&b, "\nremoved or renamed — these break every existing client:\n")
		for _, r := range removed {
			fmt.Fprintf(&b, "  - %s\n", r)
		}
	}
	if len(added) > 0 {
		fmt.Fprintf(&b, "\nadded — new public surface:\n")
		for _, r := range added {
			fmt.Fprintf(&b, "  + %s\n", r)
		}
	}
	fmt.Fprintf(&b, "\nIf every change above is intended, run:\n"+
		"  go test ./internal/api/ -run TestHTTPRouteSetMatchesTheCheckedInContract -update\n"+
		"and commit the result, so the change to the contract is visible in review.\n")
	t.Fatal(b.String())
}

func missingFrom(have, other []string) []string {
	var out []string
	for _, h := range have {
		if !slices.Contains(other, h) {
			out = append(out, h)
		}
	}
	return out
}

func writeGolden(t *testing.T, routes []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(routesGolden), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(routesGolden), err)
	}
	var b strings.Builder
	b.WriteString("# Hermod's HTTP route set — the public surface of the API.\n")
	b.WriteString("# Generated by: go test ./internal/api/ -run TestHTTPRouteSet -update\n")
	b.WriteString("# A line disappearing from here breaks every client that calls it.\n")
	for _, r := range routes {
		b.WriteString(r)
		b.WriteString("\n")
	}
	if err := os.WriteFile(routesGolden, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("writing %s: %v", routesGolden, err)
	}
}

// TestEveryRouteRegisteringPackageIsInventoried catches the hole the golden
// cannot: a new transport package whose routes are never looked at. Without
// this, adding one would silently place a whole family of endpoints outside
// the contract, and the golden would keep passing.
func TestEveryRouteRegisteringPackageIsInventoried(t *testing.T) {
	root := repoRoot(t)

	var found []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), "mux.Handle") {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		if !slices.Contains(found, rel) {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	for _, dir := range found {
		if !slices.Contains(routeDirs, filepath.ToSlash(dir)) {
			t.Errorf("%s registers HTTP routes but is not in routeDirs, so none of its "+
				"endpoints are in the contract\nadd it there and regenerate the golden, or "+
				"those routes can be changed and removed with nothing noticing", dir)
		}
	}
}

func readGolden(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(routesGolden)
	if err != nil {
		t.Fatalf("reading %s: %v\ngenerate it with -update", routesGolden, err)
	}
	var out []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}
