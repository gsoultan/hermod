package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod/internal/aibudget"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstore "github.com/gsoultan/hermod/internal/storage/sql"
)

// ---------------------------------------------------------------------------
// AI budgets over the API: an Editor of a vhost sets its budget, caps and kill
// switch; anyone with the vhost may read the budget and the usage; nobody else
// may do either. A worker, and only a worker, asks the control plane to check
// and count its calls.
// ---------------------------------------------------------------------------

type fullStore interface {
	storage.Storage
	storage.AIBudgetStore
}

// apiStore is a real SQL store that also keeps the audit entries written.
type apiStore struct {
	fullStore
	audits []storage.AuditLog
}

func (s *apiStore) CreateAuditLog(_ context.Context, l storage.AuditLog) error {
	s.audits = append(s.audits, l)
	return nil
}
func (s *apiStore) CreateLog(context.Context, storage.Log) error { return nil }

func newAPI(t *testing.T) (*Handler, *apiStore) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:aibudgetapi_%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := sqlstore.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := &apiStore{fullStore: st.(fullStore)}
	return NewHandler(&handlers.Handler{Storage: store, LogStorage: store}), store
}

var (
	admin   = &storage.User{ID: "u-admin", Username: "root", Role: storage.RoleAdministrator, VHosts: []string{"*"}}
	editorA = &storage.User{ID: "u-ed-a", Username: "ada", Role: storage.RoleEditor, VHosts: []string{"tenant-a"}}
	editorB = &storage.User{ID: "u-ed-b", Username: "bob", Role: storage.RoleEditor, VHosts: []string{"tenant-b"}}
	viewerA = &storage.User{ID: "u-vw-a", Username: "vic", Role: storage.RoleViewer, VHosts: []string{"tenant-a"}}
	worker  = &storage.User{ID: "worker:w-1", Username: "worker", Role: storage.RoleViewer}
)

func call(h http.HandlerFunc, user *storage.User, method, vhost, body string) *httptest.ResponseRecorder {
	ctx := context.Background()
	if user != nil {
		ctx = context.WithValue(ctx, handlers.UserContextKey, user)
	}
	r := httptest.NewRequestWithContext(ctx, method, "/api/vhosts/"+vhost+"/ai/budget", bytes.NewBufferString(body))
	r.SetPathValue("vhost", vhost)
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

const budgetBody = `{"monthly_tokens":1000,"monthly_cost":20,"currency":"USD",
	"prices":[{"model":"*","input_per_million":3,"output_per_million":15}],
	"workflows":[{"workflow_id":"wf-1","monthly_tokens":100}]}`

func TestEditorSetsTheBudgetAndItIsAudited(t *testing.T) {
	h, store := newAPI(t)
	w := call(h.PutBudget, editorA, http.MethodPut, "tenant-a", budgetBody)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", w.Code, w.Body)
	}
	var rep aibudget.Report
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Budget.MonthlyTokens != 1000 || rep.Budget.UpdatedBy != "ada" || len(rep.Budget.Workflows) != 1 {
		t.Fatalf("response budget = %+v", rep.Budget)
	}
	saved, err := store.GetAIBudget(t.Context(), "tenant-a")
	if err != nil || saved.MonthlyCost != 20 {
		t.Fatalf("stored = %+v, %v", saved, err)
	}
	if len(store.audits) != 1 || store.audits[0].EntityID != "tenant-a" || store.audits[0].EntityType != "vhost" ||
		!strings.Contains(store.audits[0].Payload, `"monthly_tokens":1000`) {
		t.Fatalf("audits = %+v", store.audits)
	}
}

func TestBudgetAccess(t *testing.T) {
	h, _ := newAPI(t)
	cases := []struct {
		name   string
		fn     http.HandlerFunc
		user   *storage.User
		method string
		vhost  string
		body   string
		want   int
	}{
		{"admin writes any vhost", h.PutBudget, admin, http.MethodPut, "tenant-b", budgetBody, http.StatusOK},
		{"viewer reads its vhost", h.GetBudget, viewerA, http.MethodGet, "tenant-a", "", http.StatusOK},
		{"viewer may not write", h.PutBudget, viewerA, http.MethodPut, "tenant-a", budgetBody, http.StatusForbidden},
		{"viewer may not switch AI off", h.PutKillSwitch, viewerA, http.MethodPut, "tenant-a", `{"disabled":true}`, http.StatusForbidden},
		{"editor of another vhost may not read", h.GetBudget, editorB, http.MethodGet, "tenant-a", "", http.StatusForbidden},
		{"editor of another vhost may not write", h.PutBudget, editorB, http.MethodPut, "tenant-a", budgetBody, http.StatusForbidden},
		{"anonymous", h.GetBudget, nil, http.MethodGet, "tenant-a", "", http.StatusForbidden},
		{"all is not a vhost", h.GetBudget, admin, http.MethodGet, "all", "", http.StatusBadRequest},
		{"invalid budget", h.PutBudget, editorA, http.MethodPut, "tenant-a", `{"monthly_cost":5}`, http.StatusBadRequest},
		{"not JSON", h.PutBudget, editorA, http.MethodPut, "tenant-a", `nope`, http.StatusBadRequest},
		{"a person cannot use the worker check", h.WorkerCheck, admin, http.MethodPost, "", `{"vhost":"tenant-a"}`, http.StatusForbidden},
		{"a person cannot report worker usage", h.WorkerUsage, admin, http.MethodPost, "", `{"vhost":"tenant-a"}`, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if w := call(tc.fn, tc.user, tc.method, tc.vhost, tc.body); w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body)
			}
		})
	}
}

// The kill switch is its own route, so turning AI off in an incident does not
// need the whole budget restated, and keeps the limits that are set.
func TestKillSwitchKeepsTheLimits(t *testing.T) {
	h, store := newAPI(t)
	if w := call(h.PutBudget, editorA, http.MethodPut, "tenant-a", budgetBody); w.Code != http.StatusOK {
		t.Fatalf("PUT budget = %d %s", w.Code, w.Body)
	}
	if w := call(h.PutKillSwitch, editorA, http.MethodPut, "tenant-a", `{"disabled":true}`); w.Code != http.StatusOK {
		t.Fatalf("PUT kill switch = %d %s", w.Code, w.Body)
	}
	b, _ := store.GetAIBudget(t.Context(), "tenant-a")
	if !b.Disabled || b.MonthlyTokens != 1000 || len(b.Prices) != 1 {
		t.Fatalf("budget = %+v, want AI off and the limits kept", b)
	}
	last := store.audits[len(store.audits)-1]
	if !strings.Contains(last.Payload, `"disabled":true`) {
		t.Fatalf("the kill switch was not audited: %+v", last)
	}
	// A vhost with no budget yet can still be switched off.
	if w := call(h.PutKillSwitch, admin, http.MethodPut, "tenant-c", `{"disabled":true}`); w.Code != http.StatusOK {
		t.Fatalf("kill switch on a vhost with no budget = %d %s", w.Code, w.Body)
	}
}

func TestGetBudgetReportsUsage(t *testing.T) {
	h, store := newAPI(t)
	svc := h.service()
	if _, err := store.AddAIUsage(t.Context(), "tenant-a", svc.Period(), "", storage.AIUsageDelta{InputTokens: 7, OutputTokens: 3}); err != nil {
		t.Fatal(err)
	}
	w := call(h.GetBudget, viewerA, http.MethodGet, "tenant-a", "")
	var rep aibudget.Report
	_ = json.Unmarshal(w.Body.Bytes(), &rep)
	if w.Code != http.StatusOK || rep.Usage.Tokens() != 10 || rep.VHost != "tenant-a" {
		t.Fatalf("GET = %d %+v", w.Code, rep)
	}
}

func TestWorkerCheckAndUsage(t *testing.T) {
	h, store := newAPI(t)
	if w := call(h.PutKillSwitch, admin, http.MethodPut, "tenant-a", `{"disabled":true}`); w.Code != http.StatusOK {
		t.Fatal(w.Body)
	}
	w := call(h.WorkerCheck, worker, http.MethodPost, "", `{"vhost":"tenant-a","workflow_id":"wf-1"}`)
	var got checkResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || got.Allowed || got.Limit != "ai_disabled" {
		t.Fatalf("check = %d %s", w.Code, w.Body)
	}
	w = call(h.WorkerCheck, worker, http.MethodPost, "", `{"vhost":"tenant-b"}`)
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || !got.Allowed {
		t.Fatalf("check of a vhost with no budget = %d %s", w.Code, w.Body)
	}

	w = call(h.WorkerUsage, worker, http.MethodPost, "",
		`{"vhost":"tenant-b","workflow_id":"wf-9","provider":"openai","model":"m","input_tokens":4,"output_tokens":6}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("usage = %d %s", w.Code, w.Body)
	}
	u, _ := store.GetAIUsage(t.Context(), "tenant-b", h.service().Period(), "wf-9")
	if u.Tokens() != 10 {
		t.Fatalf("worker usage was not counted: %+v", u)
	}
	if w := call(h.WorkerUsage, worker, http.MethodPost, "", `{"vhost":"tenant-b","input_tokens":-5}`); w.Code != http.StatusBadRequest {
		t.Fatalf("negative usage = %d, want 400", w.Code)
	}
}
