package evaluator

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// functionCatalogPath is the editor's list of expression functions: what the
// function picker offers, what the help shows, and the text it inserts.
//
// The editor cannot import CallFunction's switch, so it restates it, and a
// restated list drifts. Before this file there were three of them -- the
// Formulas library (13 functions), the help modal (27) and the engine (33) --
// and Set Fields, which evaluates the same expressions, offered none at all.
// These tests make the catalog a claim about the engine: a function listed
// here that the engine does not run, a function the engine runs that is not
// listed, and an example whose shown result is not what the engine answers
// are each a build failure.
const functionCatalogPath = "../../../ui/src/lib/expressionFunctions.json"

type catalogFunction struct {
	Name      string   `json:"name"`
	Category  string   `json:"category"`
	Signature string   `json:"signature"`
	Summary   string   `json:"summary"`
	Args      []string `json:"args"`
	Example   string   `json:"example"`
	Result    any      `json:"result"`
	// Volatile marks an example with no fixed answer -- the clock, a random
	// id, a secret -- so the editor shows no result for it.
	Volatile bool `json:"volatile"`
}

type functionCatalog struct {
	Source     map[string]any    `json:"source"`
	Categories []string          `json:"categories"`
	Functions  []catalogFunction `json:"functions"`
}

// unlistedFunctions are names CallFunction answers that the catalog leaves out
// on purpose. Each needs its reason: an entry here is a function no user can
// find.
var unlistedFunctions = map[string]string{
	"env": "an older spelling of secret(); listing both would offer one function twice",
}

func readFunctionCatalog(t *testing.T) functionCatalog {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(functionCatalogPath))
	if err != nil {
		t.Fatalf("cannot read the editor's function catalog at %s: %v", functionCatalogPath, err)
	}
	var c functionCatalog
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse %s: %v", functionCatalogPath, err)
	}
	if len(c.Functions) == 0 {
		t.Fatalf("%s lists no functions", functionCatalogPath)
	}
	return c
}

// engineFunctionNames reads the names CallFunction dispatches on from its
// source, so the list cannot be restated here either.
func engineFunctionNames(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "evaluator.go", nil, 0)
	if err != nil {
		t.Fatalf("parse evaluator.go: %v", err)
	}
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "CallFunction" || fn.Body == nil {
			continue
		}
		for _, stmt := range fn.Body.List {
			sw, ok := stmt.(*ast.SwitchStmt)
			if !ok {
				continue
			}
			for _, clause := range sw.Body.List {
				for _, expr := range clause.(*ast.CaseClause).List {
					lit, ok := expr.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Fatalf("CallFunction has a case that is not a string literal; this reader needs updating")
					}
					name, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("unquote %s: %v", lit.Value, err)
					}
					names = append(names, name)
				}
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("found no function names in CallFunction; has its switch moved?")
	}
	sort.Strings(names)
	return names
}

func TestFunctionCatalogListsEveryFunctionTheEngineRuns(t *testing.T) {
	catalog := readFunctionCatalog(t)

	listed := make(map[string]bool, len(catalog.Functions))
	for _, f := range catalog.Functions {
		// CallFunction lowercases the name, so toInt and toint are one function.
		key := strings.ToLower(f.Name)
		if listed[key] {
			t.Errorf("the catalog lists %q twice", f.Name)
		}
		listed[key] = true
	}

	engine := make(map[string]bool)
	for _, name := range engineFunctionNames(t) {
		engine[name] = true
		_, hidden := unlistedFunctions[name]
		switch {
		case hidden && listed[name]:
			t.Errorf("%q is both listed and in unlistedFunctions; drop one", name)
		case !hidden && !listed[name]:
			t.Errorf("the engine runs %s() but the catalog does not list it, so no one can find it; add it to %s", name, functionCatalogPath)
		}
	}
	for name := range listed {
		if !engine[name] {
			t.Errorf("the catalog offers %s() but the engine has no such function: it would write null", name)
		}
	}
	for name := range unlistedFunctions {
		if !engine[name] {
			t.Errorf("unlistedFunctions names %q, which the engine no longer runs", name)
		}
	}
}

func TestFunctionCatalogExamplesAreWhatTheEngineAnswers(t *testing.T) {
	catalog := readFunctionCatalog(t)

	categories := make(map[string]bool, len(catalog.Categories))
	for _, c := range catalog.Categories {
		categories[c] = true
	}

	msg := &mockMessage{data: catalog.Source}
	for _, f := range catalog.Functions {
		t.Run(f.Name, func(t *testing.T) {
			if strings.TrimSpace(f.Summary) == "" {
				t.Error("no summary")
			}
			if !categories[f.Category] {
				t.Errorf("category %q is not one of %v, so the picker would never show it", f.Category, catalog.Categories)
			}
			// The signature and the example are shown under the function's
			// name, and the inserted text is built from the name and args.
			for what, text := range map[string]string{"signature": f.Signature, "example": f.Example} {
				if !strings.HasPrefix(text, f.Name+"(") || !strings.HasSuffix(text, ")") {
					t.Errorf("%s %q is not a call to %s", what, text, f.Name)
				}
			}

			got := NewEvaluator().ParseAndEvaluate(msg, f.Example)
			if f.Volatile {
				if f.Result != nil {
					t.Errorf("a volatile example has no fixed result to show, but one is given: %v", f.Result)
				}
				return
			}
			if f.Result == nil {
				t.Fatalf("no result given for %s; the engine answers %s", f.Example, fixtureJSON(t, got))
			}
			if g, w := fixtureJSON(t, got), fixtureJSON(t, f.Result); g != w {
				t.Errorf("%s\n engine answers %s\ncatalog shows  %s", f.Example, g, w)
			}

			// Inside other text the picker inserts the call as a {{ }} token,
			// which a set value reads through another path.
			token := NewEvaluator().EvaluateAdvancedExpression(msg, "{{"+f.Example+"}}")
			if g, w := fixtureJSON(t, token), fixtureJSON(t, f.Result); g != w {
				t.Errorf("{{%s}}\n engine answers %s\ncatalog shows  %s", f.Example, g, w)
			}

			// What the picker inserts: the name and its placeholders. It has
			// to read as a call to this function, not as literal text.
			inserted := f.Name + "(" + strings.Join(f.Args, ", ") + ")"
			if args := NewEvaluator().parseArgs(strings.Join(f.Args, ", ")); len(args) != len(f.Args) {
				t.Errorf("inserted text %q splits into %d arguments, want %d", inserted, len(args), len(f.Args))
			}
		})
	}
}

// The editor also offers the picker on a condition, and writes the call two
// ways there: bare in the Field, as a {{ }} token in the Value. Each spelling
// is one the engine has to read as a call, and the contrast cases are why the
// other spelling is not offered.
func TestAConditionRunsACallWhereTheEditorOffersOne(t *testing.T) {
	msg := &mockMessage{data: map[string]any{"status": "PAID", "expected": "paid"}}

	cases := []struct {
		name string
		cond map[string]any
		want bool
	}{
		{"the field, as it stands", map[string]any{"field": "status", "operator": "=", "value": "paid"}, false},
		{"a call as the field", map[string]any{"field": "lower(source.status)", "operator": "=", "value": "paid"}, true},
		// Inside a call a bare name is text, so the picker writes source.status.
		{"a bare name inside the call is text", map[string]any{"field": "lower(status)", "operator": "=", "value": "paid"}, false},
		{"a call as a token in the value", map[string]any{"field": "status", "operator": "=", "value": "{{upper(source.expected)}}"}, true},
		{"a bare call in the value is text", map[string]any{"field": "status", "operator": "=", "value": "upper(source.expected)"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EvaluateConditions(msg, []map[string]any{c.cond}); got != c.want {
				t.Errorf("%v: got %t, want %t", c.cond, got, c.want)
			}
		})
	}
}

// What a value that is not a call to any function turns into. The editor
// warns about each of these (ui/src/lib/functionCatalog.ts, notFunctions), and
// says which of the two things happens; this is the engine's side of that
// sentence. Neither is an error anywhere, which is why the editor says it.
func TestAValueThatCallsNoFunction(t *testing.T) {
	msg := &mockMessage{data: map[string]any{"name": "Ada"}}

	cases := []struct {
		name string
		expr string
		want any
	}{
		{"a name the engine does not know writes nothing", "nwo()", nil},
		{"so does text that happens to end in brackets", "Paris (France)", nil},
		{"and a call holding one", "upper(nwo(source.name))", ""},
		{"a dotted name is not a call at all: it is text", "time.now()", "time.now()"},
		{"brackets that do not end the value are text", "see (note) below", "see (note) below"},
		{"quoted, the brackets are text", "'Paris (France)'", "Paris (France)"},
		{"in a template, an unknown name writes nothing", "at {{nwo()}}", "at "},
		{"in a template, a dotted name is text", "at {{time.now()}}", "at time.now()"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NewEvaluator().EvaluateAdvancedExpression(msg, c.expr)
			if g, w := fixtureJSON(t, got), fixtureJSON(t, c.want); g != w {
				t.Errorf("%s\n got %s\nwant %s", c.expr, g, w)
			}
		})
	}
}
