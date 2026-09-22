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

// sinkRoot is the subtree this guard governs. Failing when it scans nothing is
// deliberate: a guard that silently governs an empty set is worse than no guard,
// because it reports success.
const sinkRoot = "pkg/comm/sink"

// TestSinksDoNotMutateMessagesTheyAreGiven keeps a sink from writing to a
// message it did not create.
//
// The engine fans a message out to its sinks by handing every one of them the
// *same* object: runner.go retains it per target and dispatches the targets with
// swg.Go, one goroutine each. Nothing serialises those goroutines against each
// other. That is safe only for as long as they are all readers — and today they
// are, which is why nothing has crashed here.
//
// A single mutating sink ends that. SetPayload (and SetAfter, which is an alias
// for it) does `clear(m.data)`, so an enrichment written into one sink's copy
// would empty the data map a neighbouring sink is reading to build its row. Two
// outcomes, both bad: `fatal error: concurrent map read and map write` if the
// timing lands that way, and a silently truncated row if it does not. The second
// is worse, because nothing reports it.
//
// This exists because the safety property is otherwise invisible. The data map
// has no equivalent of the locked MetadataValue accessor that fixed the metadata
// half of this — a path walk cannot be a single-key read — so ownership is the
// whole defence, and ownership is not something a reader of one sink can see. It
// is precisely the shape that already bit OrderingKey: a correctness argument
// that held when it was written, a caller added later that broke it, and no test
// in between.
//
// Constructing and populating a message is fine — Browse does it on every SQL
// sink. The rule is narrower than "no mutation": mutate what you made, never
// what you were handed.
//
// Known limitation, verified against planted violations rather than assumed:
// ownership is recognised syntactically, by an assignment from AcquireMessage or
// Clone in the same function. A message obtained through a local helper that
// wraps one of those is *not* recognised, and mutating it would be reported. If
// that happens the code is fine and this test is wrong — inline the constructor
// or widen messagesConstructedIn. Do not delete the test.
func TestSinksDoNotMutateMessagesTheyAreGiven(t *testing.T) {
	// Writes that reach the data map or the payload bytes. SetMetadata is
	// deliberately absent: it takes the message's write lock, every reader of
	// that map now goes through the locked MetadataValue, and two sinks writing
	// metadata concurrently is therefore safe.
	mutators := map[string]string{
		"SetData":       "writes a field into the data map",
		"SetPayload":    "replaces the payload and clears the data map",
		"SetAfter":      "is an alias for SetPayload, so it clears the data map too",
		"SetBefore":     "rewrites the before-image bytes in place",
		"ClearPayloads": "drops the payload and the data map",
	}

	fset := token.NewFileSet()
	var filesScanned int

	err := filepath.WalkDir(sinkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
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
		filesScanned++

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			owned := messagesConstructedIn(fn)

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				why, bad := mutators[sel.Sel.Name]
				if !bad {
					return true
				}
				recv, ok := sel.X.(*ast.Ident)
				if !ok || owned[recv.Name] {
					return true
				}
				t.Errorf("%s:%d: %s.%s() %s on a message this function did not create.\n"+
					"The engine hands the same message object to every sink and dispatches "+
					"them one goroutine each, so they are safe only while they are all "+
					"readers. Mutating a message you were given empties or rewrites what a "+
					"neighbouring sink is concurrently reading — a fatal concurrent map "+
					"access if the timing lands, a silently truncated row if it does not.\n"+
					"Build your own message with message.AcquireMessage(), or Clone() the "+
					"one you were handed, and write to that.",
					path, fset.Position(sel.Pos()).Line, recv.Name, sel.Sel.Name, why)
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", sinkRoot, err)
	}
	if filesScanned == 0 {
		t.Fatalf("scanned no Go files under %s — the sink packages moved and this "+
			"guard is now governing nothing. Point sinkRoot at their new home.", sinkRoot)
	}
}

// messagesConstructedIn returns the identifiers in fn that hold a message the
// function made itself, and may therefore write to freely.
//
// Two ways to come by one: AcquireMessage from the pool, or Clone of somebody
// else's. Clone counts because it deep-copies the data map — that is the whole
// reason foreach and the PII scan are safe today.
func messagesConstructedIn(fn *ast.FuncDecl) map[string]bool {
	owned := map[string]bool{}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range assign.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			if sel.Sel.Name != "AcquireMessage" && sel.Sel.Name != "Clone" {
				continue
			}
			if i >= len(assign.Lhs) {
				continue
			}
			if id, ok := assign.Lhs[i].(*ast.Ident); ok {
				owned[id.Name] = true
			}
		}
		return true
	})

	return owned
}
