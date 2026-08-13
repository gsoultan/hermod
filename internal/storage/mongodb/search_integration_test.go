//go:build integration
// +build integration

package mongodb

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/storage"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// The search box on every list screen reaches this backend as filter.Search.
// The SQL backend binds it as a parameter to LIKE, where it is text. This
// backend put it in a $regex, where it is a program.
//
// These run against a real MongoDB because the question is what the server does
// with the pattern, and a fake would only re-state the assumption being tested.

func mongoStore(t *testing.T) (*mongoStorage, context.Context) {
	t.Helper()
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("integration: set MONGODB_URI to run")
	}

	ctx := t.Context()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		// MONGODB_URI names a server. Failing to reach it is a failure, not a
		// reason to report that these clauses passed.
		t.Fatalf("MONGODB_URI names a server that could not be reached (%s): %v", uri, err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	dbName := os.Getenv("MONGODB_DB")
	if dbName == "" {
		dbName = "hermod_test"
	}

	s := NewMongoStorage(client, dbName).(*mongoStorage)
	if err := s.Init(ctx); err != nil {
		t.Fatalf("MONGODB_URI names a server that could not be initialised (%s): %v", uri, err)
	}
	t.Cleanup(func() { _ = client.Database(dbName).Collection("sources").Drop(context.Background()) })
	return s, ctx
}

// Searching for "." must find the sources whose text contains a full stop, not
// every source there is. A regex metacharacter arriving from a query string is
// text that the operator typed, not syntax.
func TestSearchTreatsMetacharactersAsText(t *testing.T) {
	s, ctx := mongoStore(t)

	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if err := s.CreateSource(ctx, storage.Source{
			ID: uuid.New().String(), Name: name, Type: "postgres",
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	got, total, err := s.ListSources(ctx, storage.CommonFilter{Search: "."})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 0 || len(got) != 0 {
		names := make([]string, 0, len(got))
		for _, src := range got {
			names = append(names, src.Name)
		}
		t.Errorf("searching for %q returned %d of 3 sources (%v), none of which contain a full stop\n"+
			"filter.Search goes into $regex unescaped, so the search box is a regular "+
			"expression engine: \".\" matches any character and returns the whole table",
			".", total, names)
	}
}

// A search string that is not a valid regular expression must return no
// matches, not an error. An operator typing an open bracket into a search box
// has made no mistake.
func TestAnUnbalancedBracketIsNotAServerError(t *testing.T) {
	s, ctx := mongoStore(t)

	if err := s.CreateSource(ctx, storage.Source{
		ID: uuid.New().String(), Name: "alpha", Type: "postgres",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, _, err := s.ListSources(ctx, storage.CommonFilter{Search: "["})
	if err != nil {
		t.Errorf("searching for %q failed with %v\n"+
			"an unparseable pattern reaches the server as syntax, so a character "+
			"typed into a search box becomes a failed request", "[", err)
	}
	if len(got) != 0 {
		t.Errorf("searching for %q matched %d sources that do not contain it", "[", len(got))
	}
}

// The other half of the same fix, and the half that shows it is not merely
// defensive: a name that genuinely contains regex syntax becomes findable. The
// pattern here is a nested quantifier, which is also the shape that costs the
// server real time when it is run rather than matched.
func TestASearchForRegexSyntaxFindsItLiterally(t *testing.T) {
	s, ctx := mongoStore(t)

	const literal = "(a+)+$"
	for _, name := range []string{"queue " + literal + " backlog", "unrelated"} {
		if err := s.CreateSource(ctx, storage.Source{
			ID: uuid.New().String(), Name: name, Type: "postgres",
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	got, _, err := s.ListSources(ctx, storage.CommonFilter{Search: literal})
	if err != nil {
		t.Fatalf("searching for %q failed with %v", literal, err)
	}
	if len(got) != 1 || got[0].Name != "queue "+literal+" backlog" {
		names := make([]string, 0, len(got))
		for _, src := range got {
			names = append(names, src.Name)
		}
		t.Errorf("searching for %q found %v, want just the source whose name contains it\n"+
			"the pattern is run as a regular expression against every document instead "+
			"of being looked for as text, so the one real match is the one thing missed",
			literal, names)
	}
}
