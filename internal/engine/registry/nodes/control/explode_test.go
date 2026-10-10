package control

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	msgpkg "github.com/gsoultan/hermod/pkg/comm/message"
)

func explodeMsg(t *testing.T, fields map[string]any) hermod.Message {
	t.Helper()
	m := msgpkg.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID("m1")
	for k, v := range fields {
		m.SetData(k, v)
	}
	return m
}

func runExplode(t *testing.T, msg hermod.Message, cfg map[string]any) ([]hermod.Message, error) {
	t.Helper()
	exec, ok := interfaces.GetNodeExecutor("explode")
	if !ok {
		t.Fatal("explode is not a registered node type")
	}
	node := &storage.WorkflowNode{ID: "x1", Type: "explode", Config: cfg}
	msgs, branch, err := exec.Execute(t.Context(), &stubCtx{}, "wf1", node, msg)
	if branch != "" {
		t.Errorf("branch = %q, want none", branch)
	}
	for _, m := range msgs {
		t.Cleanup(m.Release)
	}
	return msgs, err
}

func explodeData(msgs []hermod.Message) []map[string]any {
	out := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		out[i] = m.Data()
	}
	return out
}

func lines() []any {
	return []any{
		map[string]any{"sku": "x", "qty": 1},
		map[string]any{"sku": "y", "order": "overridden"},
	}
}

func TestExplode(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]any
		cfg    map[string]any
		want   []map[string]any
	}{
		{
			name:   "each element replaces the array by default",
			fields: map[string]any{"order": "o1", "lines": lines()},
			cfg:    map[string]any{"arrayPath": "lines"},
			want: []map[string]any{
				{"order": "o1", "lines": map[string]any{"sku": "x", "qty": 1}},
				{"order": "o1", "lines": map[string]any{"sku": "y", "order": "overridden"}},
			},
		},
		{
			name:   "element into a target field with its position",
			fields: map[string]any{"order": "o1", "tags": []any{"a", "b"}},
			cfg:    map[string]any{"arrayPath": "tags", "targetField": "tag", "indexField": "n"},
			want: []map[string]any{
				{"order": "o1", "tag": "a", "n": 0},
				{"order": "o1", "tag": "b", "n": 1},
			},
		},
		{
			name:   "elements merged into the record, the element winning",
			fields: map[string]any{"order": "o1", "lines": lines()},
			cfg:    map[string]any{"arrayPath": "lines", "mode": "merge"},
			want: []map[string]any{
				{"order": "o1", "sku": "x", "qty": 1},
				{"order": "overridden", "sku": "y"},
			},
		},
		{
			name:   "a nested array keeps its siblings",
			fields: map[string]any{"order": map[string]any{"id": "o1", "lines": []any{"a"}}},
			cfg:    map[string]any{"arrayPath": "order.lines", "targetField": "line"},
			want:   []map[string]any{{"order": map[string]any{"id": "o1"}, "line": "a"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msgs, err := runExplode(t, explodeMsg(t, tc.fields), tc.cfg)
			if err != nil {
				t.Fatalf("explode: %v", err)
			}
			if got := explodeData(msgs); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("explode =\n %#v\nwant\n %#v", got, tc.want)
			}
		})
	}
}

func TestExplodeMarksTheGroupForCollect(t *testing.T) {
	msgs, err := runExplode(t, explodeMsg(t, map[string]any{"tags": []any{"a", "b"}}), map[string]any{"arrayPath": "tags"})
	if err != nil {
		t.Fatalf("explode: %v", err)
	}
	for i, m := range msgs {
		md := m.Metadata()
		if md["_fanout_group"] != "m1" || md["_fanout_total"] != "2" || md["_fanout_index"] != []string{"0", "1"}[i] {
			t.Errorf("message %d metadata = %v, want the collect node's group markers", i, md)
		}
	}
}

func TestExplodeEmptyArray(t *testing.T) {
	msgs, err := runExplode(t, explodeMsg(t, map[string]any{"tags": []any{}}), map[string]any{"arrayPath": "tags"})
	if err != nil || len(msgs) != 0 {
		t.Fatalf("explode of an empty array = %d messages, %v; want none", len(msgs), err)
	}

	in := explodeMsg(t, map[string]any{"id": 1, "tags": []any{}})
	msgs, err = runExplode(t, in, map[string]any{"arrayPath": "tags", "keepEmpty": true})
	if err != nil || len(msgs) != 1 || msgs[0].Data()["id"] != 1 {
		t.Fatalf("keepEmpty: %d messages, %v; want the record passed on", len(msgs), err)
	}
	in.Retain() // runExplode releases what it returns, which is this message.
}

func TestExplodeRefuses(t *testing.T) {
	tests := []struct {
		name    string
		fields  map[string]any
		cfg     map[string]any
		wantErr string
	}{
		{"no array path", map[string]any{"a": []any{1}}, map[string]any{}, "arrayPath"},
		{"a field that is not an array", map[string]any{"a": "x"}, map[string]any{"arrayPath": "a"}, "not an array"},
		{"more elements than the cap", map[string]any{"a": []any{1, 2, 3}}, map[string]any{"arrayPath": "a", "maxItems": "2"}, "maxItems"},
		{"merging an element that is not an object", map[string]any{"a": []any{map[string]any{"k": 1}, 2}}, map[string]any{"arrayPath": "a", "mode": "merge"}, "element 1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msgs, err := runExplode(t, explodeMsg(t, tc.fields), tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
			if len(msgs) != 0 {
				t.Errorf("%d messages emitted alongside the error", len(msgs))
			}
		})
	}
}
