package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

// The shape of the Registrant Applied workflow: a foreach over a result list,
// a set of `column.after = source.after._item` to make each element the row,
// then two db_lookups. The set left a literal "after" key as the after-image,
// so the first lookup's hit was written beside it and never serialised: the
// second lookup, the sink and the editor's Available fields saw no osiris.
func TestDBLookupAfterForeachAndSetReachesTheNextNode(t *testing.T) {
	reg := newSimRegistry(t)
	dbPath := filepath.Join(t.TempDir(), "lookup.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE orgs (name TEXT, code TEXT)`,
		`INSERT INTO orgs VALUES ('acme', 'IMPC')`,
	} {
		if _, err := db.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	creator := reg.storage.(interface {
		CreateSource(context.Context, storage.Source) error
	})
	if err := creator.CreateSource(t.Context(), storage.Source{ID: "lk", Name: "lk", Type: "sqlite",
		Config: map[string]string{"path": dbPath, "use_cdc": "false"}}); err != nil {
		t.Fatal(err)
	}

	lookup := func(id, target, token string) storage.WorkflowNode {
		return storage.WorkflowNode{ID: id, Type: "transformation", Config: map[string]any{
			"transType": "db_lookup", "mode": "query", "sourceId": "lk", "targetField": target,
			"queryTemplate": `SELECT code AS "OrganizationCode" FROM orgs WHERE name = {{` + token + `}}`,
		}}
	}
	wf := storage.Workflow{ID: "wf", Name: "wf",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "each", Type: "foreach", Config: map[string]any{"arrayPath": "after.items"}},
			{ID: "item", Type: "transformation", Config: map[string]any{"transType": "set", "column.after": "source.after._item"}},
			lookup("osiris", "osiris", "source.after.org_name"),
			lookup("academic", "academic", ".after.org_name"),
			{ID: "snk", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "each"},
			{ID: "e2", SourceID: "each", TargetID: "item"},
			{ID: "e3", SourceID: "item", TargetID: "osiris"},
			{ID: "e4", SourceID: "osiris", TargetID: "academic"},
			{ID: "e5", SourceID: "academic", TargetID: "snk"},
		},
	}
	msg := message.AcquireMessage()
	msg.SetOperation("snapshot")
	msg.SetAfter([]byte(`{"items":[{"org_name":"acme"}]}`))

	steps, err := reg.TestWorkflow(t.Context(), wf, msg)
	if err != nil {
		t.Fatal(err)
	}
	after := map[string]map[string]any{}
	for _, s := range steps {
		if s.Error != "" {
			t.Fatalf("%s: %s", s.NodeID, s.Error)
		}
		raw, _ := s.Payload["after"].(json.RawMessage)
		var image map[string]any
		_ = json.Unmarshal(raw, &image)
		after[s.NodeID] = image
	}
	for _, node := range []string{"osiris", "academic", "snk"} {
		got, _ := after[node]["osiris"].(map[string]any)
		if got["OrganizationCode"] != "IMPC" {
			t.Errorf("%s: after-image has no osiris from the first lookup: %v", node, after[node])
		}
	}
	for _, node := range []string{"academic", "snk"} {
		got, _ := after[node]["academic"].(map[string]any)
		if got["OrganizationCode"] != "IMPC" {
			t.Errorf("%s: after-image has no academic from the second lookup: %v", node, after[node])
		}
	}
}
