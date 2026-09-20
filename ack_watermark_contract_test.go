package hermod

// A source that persists a cursor must advance it on acknowledgement.
//
// Ten SQL and CDC sources were fixed for this once. The social and HTTP
// pollers were not part of that sweep, and eight of them still advanced their
// *stored* cursor inside Read: a page of a hundred items was recorded as
// consumed the moment it was fetched, so a crash after delivering the first
// lost the other ninety-nine. Nothing fetches that window again. At-most-once,
// in connectors documented as at-least-once, with no error anywhere.
//
// The shape is always the same and always invisible: `Ack` is `return nil`,
// and `GetState` hands back a field that `Read` moved. So it is checked here
// rather than remembered — a new connector with a stored cursor and a no-op
// Ack fails the build, in the plain unit job, needing nothing but the source
// tree.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const sourceDir = "pkg/comm/source"

// exempt lists sources this check would otherwise flag, with the reason. An
// entry is a claim that has been checked, not a way to quieten the build.
var exempt = map[string]string{
	"googleanalytics": "lastFetch records when the source last polled, not what it consumed: " +
		"it gates the poll interval and appears in the message id, and nothing filters the " +
		"API query by it. Advancing it on read loses nothing.",
}

// bodyIsTrivial reports whether fn does nothing but return nil.
func bodyIsTrivial(fn *ast.FuncDecl) bool {
	if fn.Body == nil || len(fn.Body.List) != 1 {
		return false
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	id, ok := ret.Results[0].(*ast.Ident)
	return ok && id.Name == "nil"
}

func methodName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	return fn.Name.Name
}

// receiverType names the type a method hangs off, with any pointer stripped.
//
// The check is per type, not per package. A package can hold more than one
// source — pkg/comm/source/file has GenericFileSource, which stores a
// watermark, alongside CSVSource, which stores nothing and quite correctly has
// a no-op Ack. Aggregating the two reads as one offender that does not exist,
// and the only ways out of that are an exemption for a source that is not
// broken, or looking at the right unit.
func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	// A generic receiver is Name[T]; the type is under the index.
	switch e := expr.(type) {
	case *ast.IndexExpr:
		expr = e.X
	case *ast.IndexListExpr:
		expr = e.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// sourceKind is what the check needs to know about one receiver type.
type sourceKind struct {
	storesCursor bool
	trivialAck   bool
}

// offendingTypes names the types in a package that persist a cursor and never
// act on an acknowledgement.
//
// Files are parsed one at a time rather than through parser.ParseDir, which is
// deprecated for not understanding build tags.
func offendingTypes(t *testing.T, pkgPath string) []string {
	t.Helper()

	entries, err := os.ReadDir(pkgPath)
	if err != nil {
		t.Fatalf("reading %s: %v", pkgPath, err)
	}
	kinds := map[string]*sourceKind{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(pkgPath, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s/%s: %v", pkgPath, name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			recv := receiverType(fn)
			if recv == "" {
				continue
			}
			k := kinds[recv]
			if k == nil {
				k = &sourceKind{}
				kinds[recv] = k
			}
			switch methodName(fn) {
			case "GetState":
				k.storesCursor = true
			case "Ack":
				if bodyIsTrivial(fn) {
					k.trivialAck = true
				}
			}
		}
	}

	var out []string
	for name, k := range kinds {
		if k.storesCursor && k.trivialAck {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func TestASourceThatStoresACursorAdvancesItOnAck(t *testing.T) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatalf("reading %s: %v", sourceDir, err)
	}

	var offenders []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bad := offendingTypes(t, filepath.Join(sourceDir, e.Name()))
		if len(bad) == 0 {
			continue
		}
		if _, ok := exempt[e.Name()]; ok {
			continue
		}
		offenders = append(offenders, e.Name()+" ("+strings.Join(bad, ", ")+")")
	}

	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf(`these sources persist a cursor but never act on an acknowledgement:

	%s

Each has a GetState that stores a position and an Ack that does nothing, which
means the stored position can only have come from Read. A cursor written at
fetch time says a whole page was consumed before any of it was delivered, so a
restart mid-page loses the remainder — silently, and permanently.

Use pkg/infra/ackwatermark: record each message with Emitted(id, cursor) as it
goes out, call Ack(id) from the source's Ack, and return Mark() from GetState.
For an API whose cursor addresses a page rather than an item, give every item
but the last an empty cursor so the token is only stored once the whole page is
acknowledged.

pkg/comm/source/slack is the worked example, and slack_ack_test.go shows what
proving it looks like.`, strings.Join(offenders, "\n\t"))
	}
}

// An exemption that no longer applies is worse than no exemption: it hides a
// regression in the one place nobody looks. If a source on the list has been
// fixed, the entry has to go.
func TestNoExemptionIsStale(t *testing.T) {
	for name := range exempt {
		if len(offendingTypes(t, filepath.Join(sourceDir, name))) == 0 {
			t.Errorf("%q is on the exemption list but no longer trips the check; remove the entry", name)
		}
	}
}

// The gate is only meaningful if it can see the sources at all.
func TestTheAckContractGateSeesTheSources(t *testing.T) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatalf("reading %s: %v", sourceDir, err)
	}
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	if dirs < 20 {
		t.Errorf("only %d source packages found under %s; the contract test above "+
			"passes trivially if it is looking in the wrong place", dirs, sourceDir)
	}
}
