package http

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// checkActiveWorkflows protects a source that an active workflow holds in a
// source node's RefID. It walks nodes for `type == "source"`, which is only one
// of the ways a workflow names a source: a db_lookup holds one in its config
// under sourceId, and a batch_sql source holds one in source_id. A source
// reached only those ways was editable out from under a running workflow.
//
// That matters more now that the engine refuses to query a CDC source. Turning
// CDC on for a source a running lookup depends on breaks that workflow on its
// next message, and nothing said so.
type cdcFlipStorage struct {
	storage.Storage
	sources   map[string]storage.Source
	workflows []storage.Workflow
	listErr   error
}

func (m *cdcFlipStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	src, ok := m.sources[id]
	if !ok {
		return storage.Source{}, storage.ErrNotFound
	}
	return src, nil
}

func (m *cdcFlipStorage) ListWorkflows(ctx context.Context, f storage.CommonFilter) ([]storage.Workflow, int, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	return m.workflows, len(m.workflows), nil
}

func lookupWorkflow(active bool, lookupSourceID string) storage.Workflow {
	return storage.Workflow{
		Name: "enrich orders", Active: active,
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "stream"},
			{ID: "n2", Type: "transformation", Config: map[string]any{
				"transType": "db_lookup",
				"sourceId":  lookupSourceID,
			}},
		},
	}
}

func cdcFlipHandler(st *cdcFlipStorage) *SourceHandler {
	return &SourceHandler{Handler: &handlers.Handler{Storage: st}}
}

func cfg(pairs ...string) hermod.StringMap {
	m := hermod.StringMap{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func TestUpdateSourceRefusesTurningCDCOnForALookupTarget(t *testing.T) {
	cases := []struct {
		name    string
		old     storage.Source
		updated storage.Source
		active  bool
		refuse  bool
	}{
		{
			name:    "switching CDC on breaks the lookup that queries it",
			old:     storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg("use_cdc", "false")},
			updated: storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg("use_cdc", "true")},
			active:  true,
			refuse:  true,
		},
		{
			name:    "dropping the key switches CDC on just as surely",
			old:     storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg("use_cdc", "false")},
			updated: storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg()},
			active:  true,
			refuse:  true,
		},
		{
			name:    "switching CDC off is always fine",
			old:     storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg("use_cdc", "true")},
			updated: storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg("use_cdc", "false")},
			active:  true,
			refuse:  false,
		},
		{
			name:    "a source that was already CDC is not a new break",
			old:     storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg("use_cdc", "true")},
			updated: storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg("use_cdc", "true", "host", "new")},
			active:  true,
			refuse:  false,
		},
		{
			name:    "SQL Server is the documented exception",
			old:     storage.Source{ID: "customers", Name: "erp", Type: "mssql", Config: cfg("use_cdc", "false")},
			updated: storage.Source{ID: "customers", Name: "erp", Type: "mssql", Config: cfg("use_cdc", "true")},
			active:  true,
			refuse:  false,
		},
		{
			name:    "nothing is running, so nothing breaks yet",
			old:     storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg("use_cdc", "false")},
			updated: storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg("use_cdc", "true")},
			active:  false,
			refuse:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := cdcFlipHandler(&cdcFlipStorage{
				sources:   map[string]storage.Source{"customers": tc.old},
				workflows: []storage.Workflow{lookupWorkflow(tc.active, "customers")},
			})

			err := h.checkCDCFlipForQueryTargets(t.Context(), tc.old, tc.updated)
			switch {
			case tc.refuse && err == nil:
				t.Fatal("a source a running lookup queries was switched to CDC")
			case !tc.refuse && err != nil:
				t.Fatalf("a legitimate update was refused: %v", err)
			}
			if tc.refuse && !strings.Contains(err.Error(), "enrich orders") {
				t.Errorf("the error does not name the workflow it breaks: %v", err)
			}
		})
	}
}

// A batch_sql source names its database one hop further on, so the source being
// switched is not the one any node holds.
func TestUpdateSourceRefusesTurningCDCOnForABatchSQLDelegate(t *testing.T) {
	h := cdcFlipHandler(&cdcFlipStorage{
		sources: map[string]storage.Source{
			"batch": {ID: "batch", Name: "nightly", Type: "batch_sql", Config: cfg("source_id", "reporting")},
		},
		workflows: []storage.Workflow{{
			Name: "nightly sync", Active: true,
			Nodes: []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: "batch"}},
		}},
	})

	old := storage.Source{ID: "reporting", Name: "reporting", Type: "postgres", Config: cfg("use_cdc", "false")}
	updated := storage.Source{ID: "reporting", Name: "reporting", Type: "postgres", Config: cfg("use_cdc", "true")}

	err := h.checkCDCFlipForQueryTargets(t.Context(), old, updated)
	if err == nil {
		t.Fatal("a batch_sql delegate was switched to CDC under a running workflow")
	}
	if !strings.Contains(err.Error(), "nightly sync") {
		t.Errorf("the error does not name the workflow it breaks: %v", err)
	}
}

// A source nothing queries is nobody's business but its own.
func TestUpdateSourceAllowsCDCOnAnUnreferencedSource(t *testing.T) {
	h := cdcFlipHandler(&cdcFlipStorage{
		sources:   map[string]storage.Source{},
		workflows: []storage.Workflow{lookupWorkflow(true, "somebody-else")},
	})

	old := storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg("use_cdc", "false")}
	updated := storage.Source{ID: "customers", Name: "customers", Type: "postgres", Config: cfg("use_cdc", "true")}

	if err := h.checkCDCFlipForQueryTargets(t.Context(), old, updated); err != nil {
		t.Fatalf("an unreferenced source was refused: %v", err)
	}
}
