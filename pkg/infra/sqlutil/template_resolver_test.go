package sqlutil

import (
	"reflect"
	"testing"
)

// The resolver seam exists because a SQL template used to be strictly weaker
// than every other template in Hermod. A {{ }} token here resolved through
// GetFromMapPath -- a literal walk of the data map -- while a condition, a sink
// mapping or a db_lookup keyField resolved through evaluator.GetMsgValByPath,
// which also answers the CDC envelope (after., before.), the virtual fields
// (operation, table, schema) and meta.. So `{{.after.payload}}` returned rows in
// the editor's SQL builder, whose sample still carries the envelope, and bound
// NULL in the node that ran the same text.
//
// sqlutil cannot import the evaluator -- it is deliberately below the
// transformer packages so sources and sinks can use it -- so the caller supplies
// the resolution rule instead.

func TestParameterizeTemplateWithUsesTheSuppliedResolver(t *testing.T) {
	payload := map[string]any{"registrantId": "r-1"}
	resolve := func(path string) any {
		// A path no map walk over the engine's data map could answer: the data
		// map *is* the after-image, so there is no "after" key in it.
		if path == "after.payload" {
			return payload
		}
		return nil
	}

	b := ParameterizeTemplateWith("pgx", "SELECT {{.after.payload}}::JSON AS payload", resolve)

	if b.Err != nil {
		t.Fatalf("Err = %v, want nil", b.Err)
	}
	if want := "SELECT $1::JSON AS payload"; b.SQL != want {
		t.Errorf("SQL = %q, want %q", b.SQL, want)
	}
	if len(b.Args) != 1 || !reflect.DeepEqual(b.Args[0], any(payload)) {
		t.Errorf("Args = %#v, want [%#v]", b.Args, payload)
	}
	if len(b.Unresolved) != 0 {
		t.Errorf("Unresolved = %v, want none", b.Unresolved)
	}
}

func TestParameterizeTemplateWithReportsWhatTheResolverCouldNotAnswer(t *testing.T) {
	resolve := func(string) any { return nil }

	b := ParameterizeTemplateWith("pgx", "SELECT {{.nope}}", resolve)

	if len(b.Args) != 1 || b.Args[0] != nil {
		t.Errorf("Args = %#v, want [nil] -- an unresolved token is still bound, as NULL", b.Args)
	}
	if want := []string{"nope"}; !reflect.DeepEqual(b.Unresolved, want) {
		t.Errorf("Unresolved = %v, want %v", b.Unresolved, want)
	}
}

// The cache key digest walks the template with TemplateArgs while the statement
// is built with ParameterizeTemplateEx. If the two can be given different
// resolution rules, a key can describe a different query from the one that runs
// -- which is the defect lookup_cache_fast_path.md was written about. Pin that
// they take the same seam.
func TestTemplateArgsWithBindsWhatParameterizeTemplateWithBinds(t *testing.T) {
	resolve := func(path string) any {
		return map[string]any{"after.id": "u1", "operation": "insert"}[path]
	}
	const tpl = `SELECT * FROM t WHERE id = {{.after.id}} AND op = {{.operation}}`

	args := TemplateArgsWith(tpl, resolve)
	want := ParameterizeTemplateWith("pgx", tpl, resolve).Args

	if !reflect.DeepEqual(args, want) {
		t.Errorf("TemplateArgsWith = %#v, ParameterizeTemplateWith = %#v", args, want)
	}
	if len(args) != 2 || args[0] != "u1" || args[1] != "insert" {
		t.Errorf("args = %#v, want [u1 insert]", args)
	}
}

// MapResolver is what the existing map-taking entry points delegate to, so the
// callers that genuinely have no message -- batch_sql, whose variables come from
// a parameters object on the source config -- keep exactly the behaviour they
// had.
func TestMapResolverIsTheOldBehaviour(t *testing.T) {
	data := map[string]any{"a": map[string]any{"b": 7}}
	resolve := MapResolver(data)

	if got := resolve("a.b"); got != 7 {
		t.Errorf("resolve(a.b) = %v, want 7", got)
	}
	if got := resolve("after.b"); got != nil {
		t.Errorf("resolve(after.b) = %v, want nil -- a map walk knows no virtual fields", got)
	}

	withResolver := ParameterizeTemplateWith("pgx", "SELECT {{.a.b}}", resolve)
	withMap := ParameterizeTemplateEx("pgx", "SELECT {{.a.b}}", data)
	if !reflect.DeepEqual(withResolver, withMap) {
		t.Errorf("ParameterizeTemplateWith(MapResolver(d)) = %#v, ParameterizeTemplateEx(d) = %#v",
			withResolver, withMap)
	}
}

// List expansion is decided by where the token sits, not by how it resolved, so
// it has to keep working through the seam.
func TestResolverSeamStillExpandsInLists(t *testing.T) {
	resolve := func(path string) any {
		if path == "after.ids" {
			return []any{"a", "b", "c"}
		}
		return nil
	}

	b := ParameterizeTemplateWith("pgx", "SELECT * FROM t WHERE id IN ({{.after.ids}})", resolve)

	if want := "SELECT * FROM t WHERE id IN ($1, $2, $3)"; b.SQL != want {
		t.Errorf("SQL = %q, want %q", b.SQL, want)
	}
	if len(b.Args) != 3 {
		t.Errorf("Args = %#v, want 3 elements", b.Args)
	}
}
