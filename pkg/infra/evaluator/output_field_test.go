package evaluator

import (
	"strings"
	"testing"
)

// Five nodes read their `field` with EvaluateField, so it may be a call, and
// then wrote the result to a field named after it: Mapping to `field`, Fuzzy
// Lookup to field+"_fuzzy", and so on. With lower(source.name) that is the
// path "lower(source.name)", which SetData splits at its dot -- the value
// landed under {"lower(source": {"name)": ...}} and nothing said so.
func TestOutputField(t *testing.T) {
	cases := []struct {
		name, field, target, suffix string
		want                        string
		wantErr                     bool
	}{
		{name: "a plain field is written back to", field: "status", want: "status"},
		{name: "with the node's suffix", field: "name", suffix: "_fuzzy", want: "name_fuzzy"},
		{name: "a nested path", field: "after.user.name", suffix: "_terms", want: "after.user.name_terms"},
		{name: "a target field wins", field: "status", target: "status_label", suffix: "_x", want: "status_label"},
		// source.status and status are one field to EvaluateField, so they are
		// one field to write to. It used to be the path "source.status".
		{name: "source.x is the field x", field: "source.status", want: "status"},
		{name: "source.x with a suffix", field: "source.after.name", suffix: "_fuzzy", want: "after.name_fuzzy"},
		{name: "a call with a target field", field: "lower(source.name)", target: "name_lower", want: "name_lower"},
		{name: "a call with nowhere to write", field: "lower(source.name)", wantErr: true},
		{name: "a call with nowhere to write, whatever the suffix", field: "toint(source.amount)", suffix: "_sum", wantErr: true},
		{name: "a call that takes nothing", field: "now()", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := OutputField(c.field, c.target, c.suffix)
			if c.wantErr {
				if err == nil {
					t.Fatalf("OutputField(%q, %q, %q) = %q, want an error", c.field, c.target, c.suffix, got)
				}
				// The message is what an operator reads in the node's error.
				if !strings.Contains(err.Error(), c.field) || !strings.Contains(err.Error(), "target field") {
					t.Errorf("error %q should name the expression and the target field to set", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("OutputField(%q, %q, %q): %v", c.field, c.target, c.suffix, err)
			}
			if got != c.want {
				t.Errorf("OutputField(%q, %q, %q) = %q, want %q", c.field, c.target, c.suffix, got, c.want)
			}
		})
	}
}

// What OutputField refuses has to be exactly what EvaluateField evaluates as a
// call. If the two disagreed, a field would be read one way and written another.
func TestOutputFieldRefusesWhatEvaluateFieldCalls(t *testing.T) {
	msg := &mockMessage{data: map[string]any{"name": "Ada", "lower(source": map[string]any{"name)": "a field named like a call"}}}
	if got := EvaluateField(msg, "lower(source.name)"); got != "ada" {
		t.Fatalf("EvaluateField read a call as %v, want it evaluated to \"ada\"", got)
	}
	if _, err := OutputField("lower(source.name)", "", ""); err == nil {
		t.Error("OutputField names a field for something EvaluateField evaluates as a call")
	}
}
