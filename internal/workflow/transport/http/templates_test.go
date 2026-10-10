package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// templatesDir is the sample library GET /api/templates serves.
const templatesDir = "../../../../examples/templates"

// templateFile is one entry of the library. Data is what the editor imports:
// a bundle ({workflow, sources, sinks}) or a bare workflow.
type templateFile struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Icon        string          `json:"icon"`
	Color       string          `json:"color"`
	Data        json.RawMessage `json:"data"`
}

func loadTemplate(t *testing.T, path string) (templateFile, storage.WorkflowExportBundle, bool) {
	t.Helper()
	tf, bundle, isBundle, err := readTemplate(path)
	if err != nil {
		t.Fatal(err)
	}
	return tf, bundle, isBundle
}

func readTemplate(path string) (templateFile, storage.WorkflowExportBundle, bool, error) {
	var bundle storage.WorkflowExportBundle
	raw, err := os.ReadFile(path)
	if err != nil {
		return templateFile{}, bundle, false, err
	}
	var tf templateFile
	if err := json.Unmarshal(raw, &tf); err != nil {
		return tf, bundle, false, fmt.Errorf("not a template: %w", err)
	}
	if tf.Name == "" || tf.Description == "" || tf.Icon == "" || tf.Color == "" || len(tf.Data) == 0 {
		return tf, bundle, false, errors.New("name, description, icon, color and data are all required")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(tf.Data, &probe); err != nil {
		return tf, bundle, false, fmt.Errorf("data: %w", err)
	}
	_, isBundle := probe["workflow"]
	if isBundle {
		err = json.Unmarshal(tf.Data, &bundle)
	} else {
		err = json.Unmarshal(tf.Data, &bundle.Workflow)
	}
	if err != nil {
		return tf, bundle, false, fmt.Errorf("data: %w", err)
	}
	return tf, bundle, isBundle, nil
}

func templatePaths(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(templatesDir, "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no templates found in %s: %v", templatesDir, err)
	}
	return paths
}

// legacyTemplates predate this check and do not pass it: their source and
// sink nodes are unconfigured placeholders, and some lack the template
// wrapper, so the library serves them with no data. Each is expected to fail;
// one that starts passing must be taken off this list.
var legacyTemplates = map[string]bool{
	"api_aggregator.json":                 true,
	"cdc_to_elasticsearch.json":           true,
	"cdc_to_snowflake.json":               true,
	"global_fulfillment.json":             true,
	"mysql_to_kafka_with_enrichment.json": true,
	"reliability_recovery_dlq.json":       true,
	"sap_to_s3.json":                      true,
	"world_case_gdpr.json":                true,
}

// templateProblems is every reason a template would be refused: it does not
// load, or the workflow validator reports an error.
func templateProblems(t *testing.T, h *WorkflowHandler, path string) []string {
	t.Helper()
	_, bundle, _, err := readTemplate(path)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	for _, issue := range h.ValidateWorkflow(t.Context(), bundle.Workflow) {
		if issue.Severity == "error" {
			problems = append(problems, fmt.Sprintf("node %q: %s", issue.NodeID, issue.Message))
		}
	}
	return problems
}

// Every template in the library loads and passes the workflow validator with
// no errors: a template that fails validation is refused when it is saved.
func TestEveryTemplatePassesTheValidator(t *testing.T) {
	h := NewWorkflowHandler(&handlers.Handler{})
	seen := map[string]bool{}
	for _, path := range templatePaths(t) {
		name := filepath.Base(path)
		seen[name] = true
		t.Run(name, func(t *testing.T) {
			problems := templateProblems(t, h, path)
			switch {
			case legacyTemplates[name] && len(problems) == 0:
				t.Errorf("%s passes now: remove it from legacyTemplates", name)
			case legacyTemplates[name]:
				t.Logf("legacy template, known to fail: %s", strings.Join(problems, "; "))
			default:
				for _, p := range problems {
					t.Error(p)
				}
			}
		})
	}
	for name := range legacyTemplates {
		if !seen[name] {
			t.Errorf("legacyTemplates names %s, which is not in the library", name)
		}
	}
}

var secretRef = regexp.MustCompile(`^\{\{\s*secret\("[A-Za-z0-9_.-]+"\)\s*\}\}$`)

// The AI templates import as they are: the bundle names its workflow, every
// source and sink node points at a connection the bundle carries, and no key
// or password is written into the template, only secret references.
func TestAITemplatesAreImportReadyAndKeepKeysInSecrets(t *testing.T) {
	ai := 0
	for _, path := range templatePaths(t) {
		if !strings.HasPrefix(filepath.Base(path), "ai_") {
			continue
		}
		ai++
		t.Run(filepath.Base(path), func(t *testing.T) {
			_, bundle, isBundle := loadTemplate(t, path)
			if !isBundle || bundle.Workflow.ID == "" {
				t.Fatal("an AI template is an import bundle with a workflow id")
			}
			refs := map[string]bool{}
			for _, s := range bundle.Sources {
				refs["source:"+s.ID] = true
				checkConnectionSecrets(t, s.ID, s.Config)
			}
			for _, s := range bundle.Sinks {
				refs["sink:"+s.ID] = true
				checkConnectionSecrets(t, s.ID, s.Config)
			}
			aiNodes := 0
			for _, n := range bundle.Workflow.Nodes {
				if (n.Type == "source" || n.Type == "sink") && !refs[n.Type+":"+n.RefID] {
					t.Errorf("%s node %q points at %q, which the bundle does not carry", n.Type, n.ID, n.RefID)
				}
				if _, isAI := aiNodeTypes[nodeTransformationType(n)]; !isAI {
					continue
				}
				aiNodes++
				if key, _ := n.Config["apiKey"].(string); !secretRef.MatchString(key) {
					t.Errorf("AI node %q: apiKey must be {{secret(\"NAME\")}}, got %q", n.ID, key)
				}
			}
			if aiNodes == 0 {
				t.Error("an AI template has no AI node")
			}
		})
	}
	if ai < 3 {
		t.Fatalf("found %d AI templates, want at least 3", ai)
	}
}

// checkConnectionSecrets fails a connection that writes a credential in clear.
func checkConnectionSecrets(t *testing.T, id string, config map[string]string) {
	t.Helper()
	for k, v := range config {
		lk := strings.ToLower(k)
		credential := strings.Contains(lk, "password") || strings.Contains(lk, "secret") ||
			strings.Contains(lk, "api_key") || strings.Contains(lk, "token")
		if v == "" || !credential {
			continue
		}
		if !strings.HasPrefix(v, "secret:") {
			t.Errorf("connection %q: %s must be a secret:NAME reference", id, k)
		}
	}
}

// The support triage template is the plan's flow: a support email is
// classified, its details extracted, a reply drafted, a person approves it,
// and only then is it sent.
func TestSupportTriageTemplateFlow(t *testing.T) {
	_, bundle, _ := loadTemplate(t, filepath.Join(templatesDir, "ai_support_triage.json"))
	wf := bundle.Workflow
	byID := map[string]storage.WorkflowNode{}
	for _, n := range wf.Nodes {
		byID[n.ID] = n
	}
	// Follow the edges from the source, taking the branch a happy path takes.
	path := []string{}
	cur := ""
	for _, n := range wf.Nodes {
		if n.Type == "source" {
			cur = n.ID
		}
	}
	for steps := 0; cur != "" && steps < len(wf.Nodes); steps++ {
		path = append(path, nodeTransformationType(byID[cur]))
		next := ""
		for _, e := range wf.Edges {
			label := edgeLabelOf(e)
			if e.SourceID == cur && (label == "" || label == "support" || label == "approved") {
				next = e.TargetID
				break
			}
		}
		cur = next
	}
	want := "source>ai_classify>ai_extract>ai_prompt>approval>sink"
	if got := strings.Join(path, ">"); got != want {
		t.Fatalf("happy path = %s, want %s", got, want)
	}
}

func edgeLabelOf(e storage.WorkflowEdge) string {
	if l, ok := e.Config["label"].(string); ok && l != "" {
		return l
	}
	return e.SourceHandle
}
