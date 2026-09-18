package factory

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The factory dispatches on cfg.Type with a switch, so there is no list of
// connector types to read at runtime. Rather than keep another copy by hand —
// the exact failure this file exists to prevent — read the case labels out of
// the switch itself.
func typeSwitchCases(t *testing.T, funcName string) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "factory.go", nil, 0)
	if err != nil {
		t.Fatalf("parse factory.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == funcName {
			fn = fd
			break
		}
	}
	if fn == nil {
		t.Fatalf("%s not found in factory.go; this test must move with the code", funcName)
	}

	var types []string
	ast.Inspect(fn, func(n ast.Node) bool {
		cc, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range cc.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && v != "" {
				types = append(types, v)
			}
		}
		return true
	})

	slices.Sort(types)
	types = slices.Compact(types)
	if len(types) == 0 {
		t.Fatalf("no case labels found in %s", funcName)
	}
	return types
}

// The editor asks the API which dead-letter sinks it can offer recovery for.
// When this list and the factory switches disagree, the editor either offers a
// workflow that StartWorkflow then refuses, or hides a feature that works. Both
// had happened.
func TestDLQRecoveryListMatchesFactory(t *testing.T) {
	sinkTypes := typeSwitchCases(t, "createSinkBase")
	sourceTypes := typeSwitchCases(t, "createSourceBase")
	listed := DLQRecoveryCapableSinkTypes()

	for _, typ := range listed {
		if !slices.Contains(sinkTypes, typ) {
			t.Errorf("%q is listed as DLQ-recovery capable but createSinkBase has no case for it", typ)
		}
		if !slices.Contains(sourceTypes, typ) {
			t.Errorf("%q is listed as DLQ-recovery capable but createSourceBase has no case for it; "+
				"the editor would offer it and StartWorkflow would refuse the workflow", typ)
		}
	}

	for _, typ := range sinkTypes {
		if slices.Contains(sourceTypes, typ) && !slices.Contains(listed, typ) {
			t.Errorf("%q is both a sink and a source but is missing from the DLQ-recovery "+
				"list; the editor would grey the checkbox out and the feature would be "+
				"unreachable for it", typ)
		}
	}
}

func TestSupportsDLQRecovery(t *testing.T) {
	if !SupportsDLQRecovery("postgres") {
		t.Error("postgres should support DLQ recovery")
	}
	// A sink with no source implementation: offering it enables a checkbox
	// whose workflow cannot start.
	if SupportsDLQRecovery("elasticsearch") {
		t.Error("elasticsearch is not a source type and must not claim recovery support")
	}
	if SupportsDLQRecovery("nonsense") {
		t.Error("an unknown type must not claim recovery support")
	}
}

// DLQRecoveryCapableSinkTypes hands out a copy; a caller sorting or truncating
// it must not corrupt the package's list.
func TestDLQRecoveryListIsCopied(t *testing.T) {
	got := DLQRecoveryCapableSinkTypes()
	if len(got) == 0 {
		t.Fatal("empty recovery list")
	}
	got[0] = "clobbered"
	if DLQRecoveryCapableSinkTypes()[0] == "clobbered" {
		t.Error("DLQRecoveryCapableSinkTypes exposed its backing array")
	}
}

// Guard the helper itself: a parse that silently found nothing would make the
// drift test above pass for the wrong reason.
func TestTypeSwitchCasesFindsRealTypes(t *testing.T) {
	sinkTypes := typeSwitchCases(t, "createSinkBase")
	for _, want := range []string{"postgres", "kafka"} {
		if !slices.Contains(sinkTypes, want) {
			t.Errorf("createSinkBase cases missing %q; got %s", want, strings.Join(sinkTypes, ", "))
		}
	}
}
