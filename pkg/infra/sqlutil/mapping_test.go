package sqlutil

import "testing"

func TestParseColumnMappings(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []ColumnMapping
		wantErr bool
	}{
		{
			name:  "Empty string yields no mappings",
			input: "",
			want:  nil,
		},
		// A blank source_field falls back to target_column rather than staying
		// empty. An empty path resolves to nil at write time, which binds the
		// column as NULL and breaks NOT NULL / PRIMARY KEY constraints; UIs also
		// commonly leave the field blank to mean "same name as the target".
		// See the rationale on ParseColumnMappings.
		{
			name:  "Blank source_field falls back to target_column",
			input: `[{"source_field":"","target_column":"id","data_type":"uuid","is_primary_key":true}]`,
			want: []ColumnMapping{
				{SourceField: "id", TargetColumn: "id", DataType: "uuid", IsPrimaryKey: true},
			},
		},
		{
			name:  "Whitespace-only source_field is trimmed then falls back",
			input: `[{"source_field":"   ","target_column":"code"}]`,
			want: []ColumnMapping{
				{SourceField: "code", TargetColumn: "code"},
			},
		},
		{
			name:  "Explicit source_field is preserved",
			input: `[{"source_field":"user.name","target_column":"name"}]`,
			want: []ColumnMapping{
				{SourceField: "user.name", TargetColumn: "name"},
			},
		},
		{
			name:    "Invalid JSON returns error",
			input:   `{not-json`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseColumnMappings(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("%s: expected error, got nil", tc.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.name, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("%s: got %d mappings, want %d", tc.name, len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("%s: mapping[%d] = %+v; want %+v", tc.name, i, got[i], tc.want[i])
				}
			}
		})
	}
}
