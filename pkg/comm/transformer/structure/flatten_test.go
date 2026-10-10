package structure

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

// newMsg builds a message holding fields, released when the test ends.
func newMsg(t *testing.T, fields map[string]any) *message.DefaultMessage {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}
	return msg
}

// run looks the transformer up by name, the way the engine does, and runs it.
func run(t *testing.T, name string, msg hermod.Message, cfg map[string]any) (hermod.Message, error) {
	t.Helper()
	tr, ok := transformer.Get(name)
	if !ok {
		t.Fatalf("%s is not registered", name)
	}
	return tr.Transform(t.Context(), msg, cfg)
}

func TestFlatten(t *testing.T) {
	nested := func() map[string]any {
		return map[string]any{
			"id": 7,
			"customer": map[string]any{
				"name":    "Ada",
				"address": map[string]any{"city": "Leeds", "zip": "LS1"},
			},
			"tags": []any{"a", map[string]any{"k": "v"}},
		}
	}
	tests := []struct {
		name string
		cfg  map[string]any
		want map[string]any
	}{
		{
			name: "default separator, arrays as index keys",
			cfg:  map[string]any{},
			want: map[string]any{
				"id": 7, "customer_name": "Ada", "customer_address_city": "Leeds",
				"customer_address_zip": "LS1", "tags_0": "a", "tags_1_k": "v",
			},
		},
		{
			name: "custom separator and arrays kept",
			cfg:  map[string]any{"separator": ".", "arrays": "keep"},
			want: map[string]any{
				"id": 7, "customer.name": "Ada", "customer.address.city": "Leeds",
				"customer.address.zip": "LS1", "tags": []any{"a", map[string]any{"k": "v"}},
			},
		},
		{
			name: "max depth keeps deeper objects whole",
			cfg:  map[string]any{"maxDepth": "1", "arrays": "keep"},
			want: map[string]any{
				"id": 7, "customer_name": "Ada",
				"customer_address": map[string]any{"city": "Leeds", "zip": "LS1"},
				"tags":             []any{"a", map[string]any{"k": "v"}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, "flatten", newMsg(t, nested()), tc.cfg)
			if err != nil {
				t.Fatalf("flatten: %v", err)
			}
			if got := out.Data(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("flatten =\n %#v\nwant\n %#v", got, tc.want)
			}
		})
	}
}

func TestFlattenOneField(t *testing.T) {
	msg := newMsg(t, map[string]any{
		"id":      1,
		"payload": map[string]any{"a": map[string]any{"b": 2}},
	})
	out, err := run(t, "flatten", msg, map[string]any{"field": "payload", "targetField": "flat"})
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}
	data := out.Data()
	// Reading a field by path hands back its JSON-normalised value, so the
	// number is a float64 by then.
	if !reflect.DeepEqual(data["flat"], map[string]any{"a_b": float64(2)}) {
		t.Errorf("flat = %#v, want {a_b: 2}", data["flat"])
	}
	if data["payload"] == nil || data["id"] != 1 {
		t.Errorf("the other fields were touched: %#v", data)
	}
}

func TestFlattenRefusesAKeyCollision(t *testing.T) {
	// "a_b" already exists, so flattening {"a":{"b":…}} would overwrite it.
	msg := newMsg(t, map[string]any{"a_b": "kept", "a": map[string]any{"b": "lost"}})
	_, err := run(t, "flatten", msg, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "a_b") {
		t.Fatalf("err = %v, want a collision naming a_b", err)
	}
}

func TestUnflatten(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		cfg  map[string]any
		want map[string]any
	}{
		{
			name: "default separator rebuilds objects and index arrays",
			in:   map[string]any{"id": 7, "customer_name": "Ada", "customer_city": "Leeds", "tags_0": "a", "tags_1": "b"},
			cfg:  map[string]any{},
			want: map[string]any{
				"id":       7,
				"customer": map[string]any{"name": "Ada", "city": "Leeds"},
				"tags":     []any{"a", "b"},
			},
		},
		{
			name: "arrays kept as objects with index keys",
			in:   map[string]any{"tags.0": "a", "tags.1": "b"},
			cfg:  map[string]any{"separator": ".", "arrays": "keep"},
			want: map[string]any{"tags": map[string]any{"0": "a", "1": "b"}},
		},
		{
			name: "a gap in the indexes stays an object",
			in:   map[string]any{"t_0": "a", "t_2": "c"},
			cfg:  map[string]any{},
			want: map[string]any{"t": map[string]any{"0": "a", "2": "c"}},
		},
		{
			name: "max depth leaves the rest of the key joined",
			in:   map[string]any{"a_b_c": 1},
			cfg:  map[string]any{"maxDepth": 1},
			want: map[string]any{"a": map[string]any{"b_c": 1}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, "unflatten", newMsg(t, tc.in), tc.cfg)
			if err != nil {
				t.Fatalf("unflatten: %v", err)
			}
			if got := out.Data(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("unflatten =\n %#v\nwant\n %#v", got, tc.want)
			}
		})
	}
}

func TestUnflattenRefusesAValueAndAnObjectAtOneKey(t *testing.T) {
	msg := newMsg(t, map[string]any{"a": 1, "a_b": 2})
	if _, err := run(t, "unflatten", msg, map[string]any{}); err == nil {
		t.Fatal("unflatten accepted a key that is both a value and an object")
	}
}

func TestFlattenThenUnflattenRoundTrips(t *testing.T) {
	in := map[string]any{
		"order": map[string]any{"id": "o1", "lines": []any{map[string]any{"sku": "x"}, map[string]any{"sku": "y"}}},
	}
	cfg := map[string]any{"separator": "__"}
	flat, err := run(t, "flatten", newMsg(t, in), cfg)
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}
	back, err := run(t, "unflatten", flat, cfg)
	if err != nil {
		t.Fatalf("unflatten: %v", err)
	}
	if got := back.Data(); !reflect.DeepEqual(got, in) {
		t.Errorf("round trip =\n %#v\nwant\n %#v", got, in)
	}
}

func TestFlattenKeepsTheCDCEnvelope(t *testing.T) {
	msg := newMsg(t, nil)
	msg.SetOperation(hermod.OpUpdate)
	msg.SetBefore([]byte(`{"id":1}`))
	msg.SetData("addr", map[string]any{"city": "Leeds"})

	out, err := run(t, "flatten", msg, map[string]any{})
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}
	if string(out.Before()) != `{"id":1}` || out.Operation() != hermod.OpUpdate {
		t.Errorf("before = %s, op = %s: the envelope was lost", out.Before(), out.Operation())
	}
	if !strings.Contains(string(out.Payload()), `"addr_city":"Leeds"`) {
		t.Errorf("payload = %s, want the flattened after-image", out.Payload())
	}
}
