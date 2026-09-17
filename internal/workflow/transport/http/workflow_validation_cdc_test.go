package http

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// The engine refuses to run a db_lookup against a CDC source, and refuses to
// build a batch_sql source on a CDC delegate. Until now the only way to learn
// that was to start the workflow: the editor's pickers apply the rule, but a
// workflow created through the API or restored from a bundle never went near
// them. Validation is where a workflow is told what is wrong with it before it
// runs, so the rule belongs here too.
//
// Warning, not error: validateWorkflow turns errors into a 400 on save, and a
// workflow whose lookup source somebody else just switched to CDC has to stay
// editable so it can be fixed.
type cdcValidationStorage struct {
	storage.Storage
	sources map[string]storage.Source
}

func (m *cdcValidationStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	src, ok := m.sources[id]
	if !ok {
		return storage.Source{}, storage.ErrNotFound
	}
	return src, nil
}

func cdcValidationHandler(sources map[string]storage.Source) *WorkflowHandler {
	return &WorkflowHandler{Handler: &handlers.Handler{
		Storage: &cdcValidationStorage{sources: sources},
	}}
}

// lookupWorkflow is source -> transformation -> sink, wired so the only issues
// it can raise are the ones a case is about.
func lookupWorkflow(transType, sourceKey, sourceID string) storage.Workflow {
	return storage.Workflow{
		Name: "enrich",
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "stream"},
			{ID: "n2", Type: "transformation", Config: map[string]any{
				"transType": transType,
				sourceKey:   sourceID,
			}},
			{ID: "n3", Type: "sink", RefID: "sink-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "n1", TargetID: "n2"},
			{ID: "e2", SourceID: "n2", TargetID: "n3"},
		},
	}
}

func cdcIssues(issues []ValidationIssue) []ValidationIssue {
	var out []ValidationIssue
	for _, i := range issues {
		if strings.Contains(i.Message, "CDC") {
			out = append(out, i)
		}
	}
	return out
}

func TestValidateWorkflowFlagsACDCLookupSource(t *testing.T) {
	cases := []struct {
		name   string
		source storage.Source
		warn   bool
	}{
		{
			name:   "CDC switched off is what a lookup source should be",
			source: storage.Source{ID: "lookup", Name: "customers", Type: "postgres", Config: hermod.StringMap{"use_cdc": "false"}},
			warn:   false,
		},
		{
			name:   "CDC switched on is flagged",
			source: storage.Source{ID: "lookup", Name: "orders", Type: "postgres", Config: hermod.StringMap{"use_cdc": "true"}},
			warn:   true,
		},
		{
			name:   "no use_cdc key means CDC is on",
			source: storage.Source{ID: "lookup", Name: "orders", Type: "postgres"},
			warn:   true,
		},
		{
			name:   "SQL Server is the documented exception",
			source: storage.Source{ID: "lookup", Name: "erp", Type: "mssql", Config: hermod.StringMap{"use_cdc": "true"}},
			warn:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := cdcValidationHandler(map[string]storage.Source{"lookup": tc.source})
			wf := lookupWorkflow("db_lookup", "sourceId", "lookup")

			found := cdcIssues(h.ValidateWorkflow(context.Background(), wf))
			switch {
			case tc.warn && len(found) == 0:
				t.Fatal("a db_lookup on a CDC source passed validation")
			case !tc.warn && len(found) > 0:
				t.Fatalf("a valid lookup source was flagged: %s", found[0].Message)
			}

			if !tc.warn {
				return
			}
			if found[0].Severity != "warning" {
				t.Errorf("severity = %q, want warning: an error would block saving the fix", found[0].Severity)
			}
			if !strings.Contains(found[0].Message, tc.source.Name) {
				t.Errorf("the issue does not name the source: %s", found[0].Message)
			}
			if found[0].NodeID != "n2" {
				t.Errorf("NodeID = %q, want n2 so the editor can point at the node", found[0].NodeID)
			}
			if err := h.validateWorkflow(context.Background(), wf); err != nil {
				t.Errorf("a warning blocked the save: %v", err)
			}
		})
	}
}

// A batch_sql source is reached through a source node's RefID, and the CDC
// source is one more hop away in its source_id.
func TestValidateWorkflowFlagsACDCBatchSQLDelegate(t *testing.T) {
	h := cdcValidationHandler(map[string]storage.Source{
		"batch":  {ID: "batch", Name: "nightly", Type: "batch_sql", Config: hermod.StringMap{"source_id": "orders"}},
		"orders": {ID: "orders", Name: "orders", Type: "postgres", Config: hermod.StringMap{"use_cdc": "true"}},
		"sink-1": {ID: "sink-1"},
	})

	wf := storage.Workflow{
		Name: "nightly sync",
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "batch"},
			{ID: "n2", Type: "sink", RefID: "sink-1"},
		},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "n1", TargetID: "n2"}},
	}

	found := cdcIssues(h.ValidateWorkflow(context.Background(), wf))
	if len(found) == 0 {
		t.Fatal("a batch_sql source on a CDC delegate passed validation")
	}
	if !strings.Contains(found[0].Message, "orders") {
		t.Errorf("the issue does not name the delegate: %s", found[0].Message)
	}
	if found[0].NodeID != "n1" {
		t.Errorf("NodeID = %q, want n1", found[0].NodeID)
	}
}

// execute_sql writes rather than reads, so aiming it at a CDC database is a
// different question and deliberately not answered here.
func TestValidateWorkflowLeavesExecuteSQLAlone(t *testing.T) {
	h := cdcValidationHandler(map[string]storage.Source{
		"lookup": {ID: "lookup", Name: "orders", Type: "postgres", Config: hermod.StringMap{"use_cdc": "true"}},
	})

	found := cdcIssues(h.ValidateWorkflow(context.Background(), lookupWorkflow("execute_sql", "sourceId", "lookup")))
	if len(found) > 0 {
		t.Fatalf("execute_sql was flagged: %s", found[0].Message)
	}
}

// The validation handler is constructed without storage in more than one test,
// and a source that cannot be read is not evidence of anything.
func TestValidateWorkflowWithoutStorageRaisesNoCDCIssue(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}

	found := cdcIssues(h.ValidateWorkflow(context.Background(), lookupWorkflow("db_lookup", "sourceId", "lookup")))
	if len(found) > 0 {
		t.Fatalf("a source that could not be read was reported as CDC: %s", found[0].Message)
	}
}
