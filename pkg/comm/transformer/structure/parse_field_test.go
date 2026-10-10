package structure

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseField(t *testing.T) {
	tests := []struct {
		name  string
		value any
		cfg   map[string]any
		want  any
	}{
		{
			name:  "json object",
			value: `{"a":1,"b":[true,"x"]}`,
			cfg:   map[string]any{"format": "json"},
			want:  map[string]any{"a": float64(1), "b": []any{true, "x"}},
		},
		{
			name:  "json when no format is set, as the editor shows it",
			value: `{"a":"b"}`,
			cfg:   map[string]any{},
			want:  map[string]any{"a": "b"},
		},
		{
			name:  "json from bytes",
			value: []byte(`[1,2]`),
			cfg:   map[string]any{"format": "json"},
			want:  []any{float64(1), float64(2)},
		},
		{
			name:  "csv with a header line",
			value: "id,name\n1,Ada\n2,Grace\n",
			cfg:   map[string]any{"format": "csv", "hasHeader": true},
			want: []any{
				map[string]any{"id": "1", "name": "Ada"},
				map[string]any{"id": "2", "name": "Grace"},
			},
		},
		{
			name:  "csv with configured headers and delimiter",
			value: "1;Ada",
			cfg:   map[string]any{"format": "csv", "headers": "id, name", "delimiter": ";"},
			want:  []any{map[string]any{"id": "1", "name": "Ada"}},
		},
		{
			name:  "csv without headers gives rows of values",
			value: "1\tAda",
			cfg:   map[string]any{"format": "csv", "delimiter": "tab"},
			want:  []any{[]any{"1", "Ada"}},
		},
		{
			name:  "key=value with quotes",
			value: `level=info msg="disk full" code=7`,
			cfg:   map[string]any{"format": "kv"},
			want:  map[string]any{"level": "info", "msg": "disk full", "code": "7"},
		},
		{
			name:  "key=value with custom delimiters",
			value: "a:1&b:two",
			cfg:   map[string]any{"format": "kv", "pairDelimiter": "&", "kvSeparator": ":"},
			want:  map[string]any{"a": "1", "b": "two"},
		},
		{
			name:  "xml elements, attributes and repeats",
			value: `<order id="o1"><line sku="x">2</line><line sku="y">3</line><note>rush</note></order>`,
			cfg:   map[string]any{"format": "xml"},
			want: map[string]any{"order": map[string]any{
				"@id": "o1",
				"line": []any{
					map[string]any{"@sku": "x", "#text": "2"},
					map[string]any{"@sku": "y", "#text": "3"},
				},
				"note": "rush",
			}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{"field": "raw", "targetField": "parsed"}
			for k, v := range tc.cfg {
				cfg[k] = v
			}
			out, err := run(t, "parse_field", newMsg(t, map[string]any{"raw": tc.value}), cfg)
			if err != nil {
				t.Fatalf("parse_field: %v", err)
			}
			if got := out.Data()["parsed"]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parsed =\n %#v\nwant\n %#v", got, tc.want)
			}
		})
	}
}

func TestParseFieldReplacesTheFieldByDefault(t *testing.T) {
	out, err := run(t, "parse_field", newMsg(t, map[string]any{"raw": `{"a":"b"}`}),
		map[string]any{"field": "raw", "format": "json"})
	if err != nil {
		t.Fatalf("parse_field: %v", err)
	}
	if got := out.Data()["raw"]; !reflect.DeepEqual(got, map[string]any{"a": "b"}) {
		t.Errorf("raw = %#v, want the parsed object in its place", got)
	}
}

func TestParseFieldRefuses(t *testing.T) {
	billionLaughs := `<?xml version="1.0"?><!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;">]><lolz>&lol2;</lolz>`
	tests := []struct {
		name    string
		value   any
		cfg     map[string]any
		wantErr string
	}{
		{"an unknown format", "x", map[string]any{"format": "yaml"}, "format"},
		{"a missing field", nil, map[string]any{"format": "json"}, "no field"},
		{"a value that is not text", 42, map[string]any{"format": "json"}, "not text"},
		{"malformed json", `{"a":`, map[string]any{"format": "json"}, "json"},
		{"text over the size limit", strings.Repeat("a", 2048), map[string]any{"format": "kv", "maxBytes": 1024}, "1024"},
		{"an xml document type, which is where entity expansion lives", billionLaughs, map[string]any{"format": "xml"}, "DOCTYPE"},
		{"an undeclared xml entity", `<a>&lol;</a>`, map[string]any{"format": "xml"}, "xml"},
		{"xml nested past the depth limit", strings.Repeat("<a>", 100) + strings.Repeat("</a>", 100), map[string]any{"format": "xml"}, "deep"},
		{"csv rows that disagree with the headers", "id,name\n1", map[string]any{"format": "csv", "hasHeader": true}, "csv"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string]any{}
			if tc.value != nil {
				fields["raw"] = tc.value
			}
			cfg := map[string]any{"field": "raw"}
			for k, v := range tc.cfg {
				cfg[k] = v
			}
			_, err := run(t, "parse_field", newMsg(t, fields), cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}
