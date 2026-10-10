package structure

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

func TestTemplateRender(t *testing.T) {
	fields := map[string]any{
		"name":   "Ada",
		"amount": 12.5,
		"lines":  []any{map[string]any{"sku": "x"}, map[string]any{"sku": "y"}},
	}
	tests := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{
			name: "fields and printf",
			cfg:  map[string]any{"template": `Hello {{.name}}, you owe {{printf "%.2f" .amount}}`},
			want: "Hello Ada, you owe 12.50",
		},
		{
			name: "range over a list",
			cfg:  map[string]any{"template": `{{range $i, $l := .lines}}{{if $i}},{{end}}{{$l.sku}}{{end}}`},
			want: "x,y",
		},
		{
			name: "a missing field renders empty when not strict",
			cfg:  map[string]any{"template": `[{{.nope}}]`},
			want: "[]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{"targetField": "out"}
			for k, v := range tc.cfg {
				cfg[k] = v
			}
			out, err := run(t, "template_render", newMsg(t, fields), cfg)
			if err != nil {
				t.Fatalf("template_render: %v", err)
			}
			if got := out.Data()["out"]; got != tc.want {
				t.Errorf("out = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTemplateRenderDefaultsItsTarget(t *testing.T) {
	out, err := run(t, "template_render", newMsg(t, map[string]any{"a": "b"}), map[string]any{"template": "{{.a}}"})
	if err != nil {
		t.Fatalf("template_render: %v", err)
	}
	if got := out.Data()["rendered"]; got != "b" {
		t.Errorf("rendered = %v, want b", got)
	}
}

func TestTemplateRenderRefuses(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]any
		wantErr string
	}{
		{"no template", map[string]any{}, "template"},
		{"a template that does not parse", map[string]any{"template": "{{.a"}, "template"},
		{"a missing field in strict mode", map[string]any{"template": "{{.nope}}", "strict": true}, "nope"},
		{"output over the limit", map[string]any{"template": `{{range .lines}}{{$.name}}{{end}}`, "maxBytes": 5}, "5 bytes"},
		{"call, the one builtin that runs code", map[string]any{"template": "{{call .name}}"}, "call"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := newMsg(t, map[string]any{"name": "Ada", "lines": []any{1, 2, 3}})
			_, err := run(t, "template_render", msg, tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestTemplateRenderPreparedTemplateFollowsTheConfig(t *testing.T) {
	tr, _ := transformer.Get("template_render")
	pt, ok := tr.(transformer.PreparedTransformer)
	if !ok {
		t.Fatal("template_render does not prepare its template")
	}
	cfg, err := pt.Prepare(map[string]any{"template": "one {{.a}}"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// Edited after it was prepared: the new text is what renders.
	cfg["template"] = "two {{.a}}"
	out, err := tr.Transform(t.Context(), newMsg(t, map[string]any{"a": "x"}), cfg)
	if err != nil {
		t.Fatalf("template_render: %v", err)
	}
	if got := out.Data()["rendered"]; got != "two x" {
		t.Errorf("rendered = %v, want the edited template's output", got)
	}
}
