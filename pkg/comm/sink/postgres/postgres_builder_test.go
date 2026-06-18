package postgres

import (
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/infra/sqlutil"
)

func TestIsEmptyIdentity(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"nil", nil, true},
		{"empty string", "", true},
		{"int zero", 0, true},
		{"int64 zero", int64(0), true},
		{"float64 zero", float64(0), true},
		{"uint zero", uint(0), true},
		{"non-empty string", "abc", false},
		{"int64 positive", int64(5), false},
		{"bool false (not numeric)", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isEmptyIdentity(tc.val); got != tc.want {
				t.Errorf("isEmptyIdentity(%#v) = %v; want %v", tc.val, got, tc.want)
			}
		})
	}
}

func TestQuoteColumn(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"simple", "user_id", `"user_id"`, false},
		{"mixed case", "FullName", `"FullName"`, false},
		{"injection attempt", "id; DROP TABLE users", "", true},
		{"empty", "", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := quoteColumn(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("quoteColumn(%q) expected error, got %q", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("quoteColumn(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("quoteColumn(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateDataType(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"text", "TEXT", false},
		{"varchar", "VARCHAR(255)", false},
		{"numeric", "NUMERIC(10, 2)", false},
		{"timestamp tz", "TIMESTAMP WITH TIME ZONE", false},
		{"injection", "TEXT; DROP TABLE x", true},
		{"quote", "TEXT'", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDataType(tc.input)
			if tc.wantErr != (err != nil) {
				t.Errorf("validateDataType(%q) err=%v; wantErr=%v", tc.input, err, tc.wantErr)
			}
		})
	}
}

func TestResolveDataType(t *testing.T) {
	tests := []struct {
		name string
		m    sqlutil.ColumnMapping
		want string
	}{
		{"default text", sqlutil.ColumnMapping{}, "TEXT"},
		{"explicit", sqlutil.ColumnMapping{DataType: "INTEGER"}, "INTEGER"},
		{"identity int -> serial", sqlutil.ColumnMapping{DataType: "INTEGER", IsIdentity: true}, "SERIAL"},
		{"identity bigint -> bigserial", sqlutil.ColumnMapping{DataType: "BIGINT", IsIdentity: true}, "BIGSERIAL"},
		{"identity uuid stays", sqlutil.ColumnMapping{DataType: "UUID", IsIdentity: true}, "UUID"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveDataType(tc.m)
			if err != nil {
				t.Fatalf("resolveDataType: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolveDataType = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestBuildColumnDefinition(t *testing.T) {
	tests := []struct {
		name string
		m    sqlutil.ColumnMapping
		want string
	}{
		{"pk text", sqlutil.ColumnMapping{TargetColumn: "id", DataType: "TEXT", IsPrimaryKey: true}, `"id" TEXT PRIMARY KEY`},
		{"not null", sqlutil.ColumnMapping{TargetColumn: "name", DataType: "TEXT"}, `"name" TEXT NOT NULL`},
		{"nullable", sqlutil.ColumnMapping{TargetColumn: "bio", DataType: "TEXT", IsNullable: true}, `"bio" TEXT`},
		{"identity uuid", sqlutil.ColumnMapping{TargetColumn: "id", DataType: "UUID", IsIdentity: true, IsPrimaryKey: true}, `"id" UUID DEFAULT gen_random_uuid() PRIMARY KEY`},
		{"identity serial", sqlutil.ColumnMapping{TargetColumn: "id", DataType: "INTEGER", IsIdentity: true, IsPrimaryKey: true}, `"id" SERIAL PRIMARY KEY`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildColumnDefinition(tc.m)
			if err != nil {
				t.Fatalf("buildColumnDefinition: %v", err)
			}
			if got != tc.want {
				t.Errorf("buildColumnDefinition = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestBuildUpsertQuery(t *testing.T) {
	cols := []string{`"id"`, `"name"`}
	ph := []string{"$1", "$2"}

	tests := []struct {
		name    string
		pks     []string
		updates []string
		want    string
	}{
		{
			name: "no pk -> plain insert",
			want: `INSERT INTO "t" ("id", "name") VALUES ($1, $2)`,
		},
		{
			name:    "pk with updates",
			pks:     []string{`"id"`},
			updates: []string{`"name" = EXCLUDED."name"`},
			want:    `INSERT INTO "t" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name"`,
		},
		{
			name: "pk only -> do nothing",
			pks:  []string{`"id"`},
			want: `INSERT INTO "t" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO NOTHING`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildUpsertQuery(`"t"`, cols, ph, tc.pks, tc.updates)
			if got != tc.want {
				t.Errorf("buildUpsertQuery = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestSplitSchemaTable(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSchema string
		wantTable  string
	}{
		{"plain", "users", "", "users"},
		{"qualified", "public.users", "public", "users"},
		{"only first dot splits", "a.b.c", "a", "b.c"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			schema, table := splitSchemaTable(tc.input)
			if schema != tc.wantSchema || table != tc.wantTable {
				t.Errorf("splitSchemaTable(%q) = (%q, %q); want (%q, %q)", tc.input, schema, table, tc.wantSchema, tc.wantTable)
			}
		})
	}
}

func TestValidateTxID(t *testing.T) {
	if err := validateTxID("550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Errorf("valid uuid rejected: %v", err)
	}
	if err := validateTxID("'; ROLLBACK PREPARED 'x"); err == nil {
		t.Error("expected error for non-uuid txID")
	}
}

func TestResolveOperation(t *testing.T) {
	tests := []struct {
		name string
		mode string
		op   hermod.Operation
		want hermod.Operation
	}{
		{"auto keeps create", "auto", hermod.OpCreate, hermod.OpCreate},
		{"auto empty -> create", "auto", "", hermod.OpCreate},
		{"insert forces create", "insert", hermod.OpUpdate, hermod.OpCreate},
		{"upsert forces update", "upsert", hermod.OpCreate, hermod.OpUpdate},
		{"delete forces delete", "delete", hermod.OpCreate, hermod.OpDelete},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &PostgresSink{operationMode: tc.mode}
			msg := message.AcquireMessage()
			defer message.ReleaseMessage(msg)
			if tc.op != "" {
				msg.SetOperation(tc.op)
			}
			if got := s.resolveOperation(msg); got != tc.want {
				t.Errorf("resolveOperation = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestResolveTable(t *testing.T) {
	t.Run("configured table wins", func(t *testing.T) {
		s := &PostgresSink{tableName: "fixed"}
		msg := message.AcquireMessage()
		defer message.ReleaseMessage(msg)
		msg.SetTable("other")
		if got := s.resolveTable(msg); got != "fixed" {
			t.Errorf("resolveTable = %q; want fixed", got)
		}
	})

	t.Run("schema qualified from message", func(t *testing.T) {
		s := &PostgresSink{}
		msg := message.AcquireMessage()
		defer message.ReleaseMessage(msg)
		msg.SetTable("users")
		msg.SetSchema("public")
		if got := s.resolveTable(msg); got != "public.users" {
			t.Errorf("resolveTable = %q; want public.users", got)
		}
	})
}

func TestFilterNilMessages(t *testing.T) {
	a := message.AcquireMessage()
	b := message.AcquireMessage()
	defer message.ReleaseMessage(a)
	defer message.ReleaseMessage(b)

	got := filterNilMessages([]hermod.Message{a, nil, b, nil})
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Errorf("filterNilMessages preserved %d items in unexpected order", len(got))
	}
}
