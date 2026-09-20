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

	"file": "genuinely the same bug, and not fixed yet. pop() advances lastMTime when a file " +
		"is dequeued, before any of its rows are delivered, and one file can yield thousands " +
		"of rows in CSV per-row mode — so a crash mid-file loses the remainder of that file " +
		"and every file sharing its mtime. The fix is the page-token shape: every row carries " +
		"an empty cursor except the last row of a fully drained file, which carries the file's " +
		"mtime. It needs the reader to signal end-of-file across all four backends, which is " +
		"why it is not a mechanical change like the others.",
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

// inspectSource reports whether a source package stores a cursor (it has a
// GetState) and whether its Ack does nothing.
//
// Files are parsed one at a time rather than through parser.ParseDir, which is
// deprecated for not understanding build tags.
func inspectSource(t *testing.T, pkgPath string) (storesCursor, trivialAck bool) {
	t.Helper()

	entries, err := os.ReadDir(pkgPath)
	if err != nil {
		t.Fatalf("reading %s: %v", pkgPath, err)
	}
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
			switch methodName(fn) {
			case "GetState":
				storesCursor = true
			case "Ack":
				if bodyIsTrivial(fn) {
					trivialAck = true
				}
			}
		}
	}
	return storesCursor, trivialAck
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
		storesCursor, trivialAck := inspectSource(t, filepath.Join(sourceDir, e.Name()))

		if storesCursor && trivialAck {
			if _, ok := exempt[e.Name()]; ok {
				continue
			}
			offenders = append(offenders, e.Name())
		}
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
		storesCursor, trivialAck := inspectSource(t, filepath.Join(sourceDir, name))
		if !storesCursor || !trivialAck {
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
