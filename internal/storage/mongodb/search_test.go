package mongodb

import (
	"regexp"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// The behaviour these assert is proved end-to-end against a real server in
// search_integration_test.go. This file exists because that one only runs where
// MONGODB_URI is set, and the property is worth holding on every build: what
// arrives from a search box is text, and must reach the server as text.

func patternFor(t *testing.T, search string) string {
	t.Helper()
	or := searchAcross(search, "name")
	if len(or) != 1 {
		t.Fatalf("searchAcross returned %d clauses for one field", len(or))
	}
	clause, ok := or[0]["name"].(bson.M)
	if !ok {
		t.Fatalf("unexpected clause shape %T", or[0]["name"])
	}
	pat, ok := clause["$regex"].(string)
	if !ok {
		t.Fatalf("clause has no string $regex: %v", clause)
	}
	return pat
}

// Every metacharacter an operator can type must reach the server quoted. The
// full stop is the one that matters most: unescaped it matches any character,
// so a one-character search returned every row the filter covered.
func TestSearchMetacharactersReachTheServerAsText(t *testing.T) {
	for _, search := range []string{".", "[", "(a+)+$", ".*", "^admin", `a\b`, "a|b"} {
		pat := patternFor(t, search)
		if pat != regexp.QuoteMeta(search) {
			t.Errorf("searching for %q produced the pattern %q; want it quoted as %q\n"+
				"anything else is compiled by the server as a regular expression rather "+
				"than looked for as text", search, pat, regexp.QuoteMeta(search))
			continue
		}
		// The quoted pattern must match the search text itself and nothing that
		// merely satisfies it as an expression.
		re, err := regexp.Compile(pat)
		if err != nil {
			t.Errorf("the pattern for %q does not compile: %v", search, err)
			continue
		}
		if !re.MatchString("prefix " + search + " suffix") {
			t.Errorf("searching for %q no longer finds a name containing it", search)
		}
	}
}

// "." must not behave as a wildcard, stated as the consequence rather than as
// the escaping, so the test still means something if the implementation changes.
func TestAFullStopSearchDoesNotMatchEverything(t *testing.T) {
	re, err := regexp.Compile(patternFor(t, "."))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if re.MatchString("alpha") {
		t.Error(`searching for "." matches a name with no full stop in it, so the ` +
			"search box returns the whole collection")
	}
	if !re.MatchString("v1.2") {
		t.Error(`searching for "." no longer finds a name that does contain one`)
	}
}

// Each field gets its own clause, and they all carry the same pattern: a search
// that covered four fields before must still cover four.
func TestEveryNamedFieldIsSearched(t *testing.T) {
	fields := []string{"_id", "name", "type", "vhost"}
	or := searchAcross("alpha", fields...)
	if len(or) != len(fields) {
		t.Fatalf("searching %d fields produced %d clauses", len(fields), len(or))
	}
	for i, f := range fields {
		if _, ok := or[i][f]; !ok {
			t.Errorf("clause %d does not search %q: %v", i, f, or[i])
		}
	}
}
