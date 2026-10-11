package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/lineage/endpoint"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
)

var (
	admin   = &storage.User{ID: "u-admin", Username: "root", Role: storage.RoleAdministrator}
	viewerA = &storage.User{ID: "u-a", Username: "ada", Role: storage.RoleViewer, VHosts: []string{"tenant-a"}}
	editorB = &storage.User{ID: "u-b", Username: "bob", Role: storage.RoleEditor, VHosts: []string{"tenant-b"}}
)

// newMux is the lineage routes over a real SQLite store holding two tenants:
// tenant-a captures orders from Postgres into Kafka and indexes them;
// tenant-b has a workflow of its own.
func newMux(t *testing.T) *http.ServeMux {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/lineage.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := sqlstorage.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.CreateSource(ctx, storage.Source{ID: "pg", Name: "orders db", Type: "postgres", VHost: "tenant-a",
		Config: hermod.StringMap{"host": "db", "port": "5432", "dbname": "shop", "user": "u", "password": "Hunter2!", "tables": "public.orders"}}))
	must(st.CreateSink(ctx, storage.Sink{ID: "k", Name: "orders topic", Type: "kafka", VHost: "tenant-a",
		Config: hermod.StringMap{"brokers": "b:9092", "topic": "orders"}}))
	must(st.CreateSource(ctx, storage.Source{ID: "kin", Name: "orders in", Type: "kafka", VHost: "tenant-a",
		Config: hermod.StringMap{"brokers": "b:9092", "topic": "orders"}}))
	must(st.CreateSink(ctx, storage.Sink{ID: "es", Name: "search", Type: "elasticsearch", VHost: "tenant-a",
		Config: hermod.StringMap{"addresses": "http://es:9200", "index": "orders"}}))
	must(st.CreateWorkflow(ctx, storage.Workflow{ID: "capture", Name: "capture orders", VHost: "tenant-a",
		Nodes: []storage.WorkflowNode{{ID: "s", Type: "source", RefID: "pg"}, {ID: "t", Type: "sink", RefID: "k"}}}))
	must(st.CreateWorkflow(ctx, storage.Workflow{ID: "index", Name: "index orders", VHost: "tenant-a",
		Nodes: []storage.WorkflowNode{{ID: "s", Type: "source", RefID: "kin"}, {ID: "t", Type: "sink", RefID: "es"}}}))
	must(st.CreateSource(ctx, storage.Source{ID: "bsrc", Name: "b source", Type: "kafka", VHost: "tenant-b",
		Config: hermod.StringMap{"brokers": "b:9092", "topic": "b-events"}}))
	must(st.CreateWorkflow(ctx, storage.Workflow{ID: "bwf", Name: "b workflow", VHost: "tenant-b",
		Nodes: []storage.WorkflowNode{{ID: "s", Type: "source", RefID: "bsrc"}}}))

	mux := http.NewServeMux()
	NewHandler(&handlers.Handler{Storage: st, LogStorage: st}).RegisterRoutes(mux)
	return mux
}

func get(t *testing.T, mux http.Handler, user *storage.User, path string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := context.Background()
	if user != nil {
		ctx = context.WithValue(ctx, handlers.UserContextKey, user)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
	return w
}

func decodeGraph(t *testing.T, w *httptest.ResponseRecorder) endpoint.GraphResponse {
	t.Helper()
	var g endpoint.GraphResponse
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatalf("%v: %s", err, w.Body)
	}
	return g
}

func nodeIDs(g endpoint.GraphResponse) map[string]bool {
	out := map[string]bool{}
	for _, n := range g.Nodes {
		out[n.ID] = true
	}
	return out
}

func TestGraphOfAVHost(t *testing.T) {
	mux := newMux(t)
	w := get(t, mux, viewerA, "/api/vhosts/tenant-a/lineage")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", w.Code, w.Body)
	}
	g := decodeGraph(t, w)
	for _, id := range []string{"workflow:capture", "workflow:index", "table:postgres://db:5432/shop/public.orders",
		"topic:kafka://b:9092/orders", "index:elasticsearch://es:9200/orders"} {
		if !nodeIDs(g)[id] {
			t.Errorf("missing %s in %+v", id, g.Nodes)
		}
	}
	if nodeIDs(g)["workflow:bwf"] {
		t.Errorf("tenant-b's workflow is in tenant-a's graph")
	}
	if g.VHost != "tenant-a" || g.Summary.Workflows != 2 || g.Summary.Assets != 3 {
		t.Errorf("summary = %+v vhost=%q", g.Summary, g.VHost)
	}
	if strings.Contains(w.Body.String(), "Hunter2!") {
		t.Errorf("a source password reached the response")
	}
}

// Abuse cases: a caller with no session, or without the vhost, gets nothing
// from it — not the graph, not an impact, not a 404 that confirms an asset.
func TestLineageAccess(t *testing.T) {
	mux := newMux(t)
	asset := url.QueryEscape("table:postgres://db:5432/shop/public.orders")
	cases := []struct {
		name string
		user *storage.User
		path string
		want int
	}{
		{"no session", nil, "/api/vhosts/tenant-a/lineage", http.StatusUnauthorized},
		{"no session, impact", nil, "/api/vhosts/tenant-a/lineage/impact?node=" + asset, http.StatusUnauthorized},
		{"another tenant's graph", editorB, "/api/vhosts/tenant-a/lineage", http.StatusForbidden},
		{"another tenant's impact", editorB, "/api/vhosts/tenant-a/lineage/impact?node=" + asset, http.StatusForbidden},
		{"another tenant's asset through all", editorB, "/api/vhosts/all/lineage/impact?node=" + asset, http.StatusNotFound},
		{"own vhost", editorB, "/api/vhosts/tenant-b/lineage", http.StatusOK},
		{"admin any vhost", admin, "/api/vhosts/tenant-b/lineage", http.StatusOK},
		{"impact needs a node", viewerA, "/api/vhosts/tenant-a/lineage/impact", http.StatusBadRequest},
		{"impact of an unknown node", viewerA, "/api/vhosts/tenant-a/lineage/impact?node=table:nope", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := get(t, mux, tc.user, tc.path)
			if w.Code != tc.want {
				t.Fatalf("GET %s = %d %s, want %d", tc.path, w.Code, w.Body, tc.want)
			}
			if tc.want != http.StatusOK && strings.Contains(w.Body.String(), "orders") {
				t.Errorf("a refusal names tenant-a's assets: %s", w.Body)
			}
		})
	}
}

func TestAllVHostsShowsOnlyTheCallersOwn(t *testing.T) {
	mux := newMux(t)
	g := decodeGraph(t, get(t, mux, editorB, "/api/vhosts/all/lineage"))
	ids := nodeIDs(g)
	if !ids["workflow:bwf"] || ids["workflow:capture"] || ids["topic:kafka://b:9092/orders"] {
		t.Fatalf("editor of tenant-b sees %v", ids)
	}
	all := nodeIDs(decodeGraph(t, get(t, mux, admin, "/api/vhosts/all/lineage")))
	if !all["workflow:bwf"] || !all["workflow:capture"] {
		t.Fatalf("admin sees %v", all)
	}
}

func TestImpactOfAnAsset(t *testing.T) {
	mux := newMux(t)
	w := get(t, mux, viewerA, "/api/vhosts/tenant-a/lineage/impact?node="+url.QueryEscape("table:postgres://db:5432/shop/public.orders"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", w.Code, w.Body)
	}
	var imp endpoint.ImpactResponse
	if err := json.Unmarshal(w.Body.Bytes(), &imp); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range imp.Impacted {
		got = append(got, it.Node.ID)
	}
	want := []string{"workflow:capture", "topic:kafka://b:9092/orders", "workflow:index", "index:elasticsearch://es:9200/orders"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("impacted = %v, want %v", got, want)
	}
	last := imp.Impacted[len(imp.Impacted)-1]
	if len(last.Path) != 5 || last.Path[0] != "table:postgres://db:5432/shop/public.orders" {
		t.Errorf("path = %v", last.Path)
	}
	if imp.Workflows != 2 || imp.Assets != 2 || imp.VHost != "tenant-a" {
		t.Errorf("counts = %+v", imp)
	}
}

func TestLineageBeforeSetupIsUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(&handlers.Handler{}).RegisterRoutes(mux)
	if w := get(t, mux, admin, "/api/vhosts/all/lineage"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET = %d, want 503", w.Code)
	}
}
