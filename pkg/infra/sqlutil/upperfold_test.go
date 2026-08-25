package sqlutil

import "testing"

// Identifier case folding, which is not the same in every dialect.
//
// Quoting a name makes it case-sensitive everywhere. What differs is what the
// *unquoted* form would have meant, and that is what a user's configuration is
// implicitly written against:
//
//   - PostgreSQL, YugabyteDB, Cassandra fold unquoted identifiers to lower
//     case, so quoting "id" names the same column ordinary DDL created.
//   - Oracle and Snowflake fold to UPPER case, so quoting "id" names a
//     different, lower-case column — one that conventional DDL never created.
//
// Treating the second group like the first is why the Oracle sink could not
// write to an ordinary Oracle table at all: a mapping to `id` produced
// "id", and every statement failed with ORA-00904 naming a column that does
// not exist. Verified against a real Oracle server, which is the only place
// this was ever going to show up.
func TestQuotingFollowsTheDialectsOwnCaseFolding(t *testing.T) {
	upper := []string{"oracle", "snowflake"}
	lower := []string{"postgres", "pgx", "yugabyte", "cassandra", "clickhouse"}

	for _, d := range upper {
		got, err := QuoteIdent(d, "id")
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		if got != `"ID"` {
			t.Errorf("QuoteIdent(%q, \"id\") = %s, want \"ID\": %s folds unquoted "+
				"identifiers to upper case, so quoting the name as typed misses the "+
				"column ordinary DDL created", d, got, d)
		}
	}

	for _, d := range lower {
		got, err := QuoteIdent(d, "id")
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		if got != `"id"` {
			t.Errorf("QuoteIdent(%q, \"id\") = %s, want \"id\"", d, got)
		}
	}
}

// Folding applies per segment, so a schema-qualified name stays qualified.
func TestUpperFoldingAppliesToEverySegment(t *testing.T) {
	got, err := QuoteIdent("oracle", "hr.orders")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"HR"."ORDERS"` {
		t.Errorf("QuoteIdent(oracle, hr.orders) = %s, want \"HR\".\"ORDERS\"", got)
	}
}

// A name already spelled the way Oracle stores it is unchanged, so existing
// configurations that used upper case keep working.
func TestAlreadyUpperCaseNamesAreUnchanged(t *testing.T) {
	got, err := QuoteIdent("oracle", "ID")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"ID"` {
		t.Errorf("QuoteIdent(oracle, ID) = %s, want \"ID\"", got)
	}
}
