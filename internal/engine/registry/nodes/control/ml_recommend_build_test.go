package control

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
)

// fakeBuilder answers MLRecommendBuild with rows, recording the call.
type fakeBuilder struct {
	vhost, model string
	config       map[string]any
	rows         []map[string]any
	err          error
}

func (f *fakeBuilder) MLRecommendBuild(_ context.Context, vhost, model string, config map[string]any) ([]map[string]any, int, error) {
	f.vhost, f.model, f.config = vhost, model, config
	return f.rows, len(f.rows), f.err
}

func runBuild(t *testing.T, reg any, cfg map[string]any) ([]hermod.Message, error) {
	t.Helper()
	exec, ok := interfaces.GetNodeExecutor("ml_recommend_build")
	if !ok {
		t.Fatal("ml_recommend_build is not a registered node type")
	}
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	node := &storage.WorkflowNode{ID: "b1", Type: "ml_recommend_build", Config: cfg}
	msgs, _, err := exec.Execute(ctx, &stubCtx{}, "wf1", node, explodeMsg(t, map[string]any{"trigger": "nightly"}))
	for _, m := range msgs {
		t.Cleanup(m.Release)
	}
	return msgs, err
}

func TestRecommendBuildEmitsOneRecordPerRow(t *testing.T) {
	reg := &fakeBuilder{rows: []map[string]any{
		{"model": "plan", "for_id": "R1", "item_id": "C1", "rank": 1, "plan_date": "2026-10-13"},
		{"model": "plan", "for_id": "R1", "item_id": "C2", "rank": 2, "plan_date": "2026-10-13", "due_date": "2026-10-20",
			"segment": "Retail", "size": "large"},
	}}
	msgs, err := runBuild(t, reg, map[string]any{"model": "plan", "for": "all_salesmen", "periodDays": "5", "withinDays": "14", "k": 20.0,
		"targetSourceId": "pg", "targetTable": "visit_plan", "targetMode": "replace"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"for": "all_salesmen", "periodDays": 5, "withinDays": 14, "k": 20, "maxRows": defaultMaxBuildMessages,
		"targetSourceId": "pg", "targetTable": "visit_plan", "targetMode": "replace"}
	if reg.model != "plan" || reg.vhost != "default" || !reflect.DeepEqual(reg.config, want) {
		t.Fatalf("called %s/%s with %v", reg.vhost, reg.model, reg.config)
	}
	if len(msgs) != 2 || msgs[1].Data()["item_id"] != "C2" || msgs[1].Data()["trigger"] != "nightly" ||
		msgs[0].Metadata()["_fanout_total"] != "2" || msgs[1].Data()["due_date"] != "2026-10-20" ||
		msgs[1].Data()["segment"] != "Retail" || msgs[1].Data()["size"] != "large" {
		t.Fatalf("messages %v", explodeData(msgs))
	}
}

func TestRecommendBuildRefusals(t *testing.T) {
	cases := []struct {
		reg  any
		cfg  map[string]any
		want string
	}{
		{&fakeBuilder{}, map[string]any{"for": "all_salesmen"}, "model"},
		{&fakeBuilder{}, map[string]any{"model": "m", "for": "all_salesmen", "k": "many"}, "k"},
		{&fakeBuilder{}, map[string]any{"model": "m", "for": "all_customers", "withinDays": "soon"}, "withinDays"},
		{&fakeBuilder{}, map[string]any{"model": "m", "for": "all_salesmen", "targetTable": "t"}, "target"},
		{&fakeBuilder{err: errors.New("boom")}, map[string]any{"model": "m", "for": "all_salesmen"}, "boom"},
		{struct{}{}, map[string]any{"model": "m", "for": "all_salesmen"}, "cannot build"},
	}
	for _, tc := range cases {
		if _, err := runBuild(t, tc.reg, tc.cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v, want an error mentioning %q", tc.cfg, err, tc.want)
		}
	}
}
