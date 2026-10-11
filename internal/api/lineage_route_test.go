package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
)

// The reachability test for lineage: workflows, sources and sinks are stored
// the way the editor stores them, in a real SQLite metadata database, and the
// lineage is read back through the server's own routes and middleware — the
// session check, the vhost scope and the derivation — as the UI reads it.
func TestLineageIsServedFromStoredWorkflows(t *testing.T) {
	checkLineage(t, newLineageServer(t))
}

type lineageCall func(path, role string, vhosts []string) (int, string)

// newLineageServer stores two tenants' workflows in SQLite and serves them
// through Server.Routes.
func newLineageServer(t *testing.T) lineageCall {
	t.Helper()
	const secret = "lineage-route-secret"
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hermod.db")
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"),
		[]byte("type: sqlite\nconn: \""+dbPath+"\"\njwt_secret: "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)
	t.Setenv("HERMOD_JWT_SECRET", secret)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := sqlstorage.NewSQLStorage(db, "sqlite")
	ctx := t.Context()
	if err := st.Init(ctx); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// A user exists, so this is not a first run and every guard is on.
	must(st.CreateUser(ctx, storage.User{ID: "u1", Username: "alice", Password: "x", Role: storage.RoleViewer, VHosts: []string{"tenant-a"}}))

	// tenant-a: CDC from Postgres into Kafka, and Kafka into a search index.
	must(st.CreateSource(ctx, storage.Source{ID: "pg", Name: "orders db", Type: "postgres", VHost: "tenant-a",
		Config: hermod.StringMap{"host": "db", "port": "5432", "dbname": "shop", "user": "app", "password": "S3cr3t!", "tables": "public.orders"}}))
	must(st.CreateSink(ctx, storage.Sink{ID: "kout", Name: "orders topic", Type: "kafka", VHost: "tenant-a",
		Config: hermod.StringMap{"brokers": "kafka:9092", "topic": "orders"}}))
	must(st.CreateSource(ctx, storage.Source{ID: "kin", Name: "orders feed", Type: "kafka", VHost: "tenant-a",
		Config: hermod.StringMap{"brokers": "kafka:9092", "topic": "orders"}}))
	must(st.CreateSink(ctx, storage.Sink{ID: "es", Name: "search", Type: "elasticsearch", VHost: "tenant-a",
		Config: hermod.StringMap{"addresses": "http://es:9200", "index": "orders"}}))
	must(st.CreateWorkflow(ctx, storage.Workflow{ID: "capture", Name: "capture orders", VHost: "tenant-a",
		Nodes: []storage.WorkflowNode{{ID: "s", Type: "source", RefID: "pg"}, {ID: "t", Type: "sink", RefID: "kout"}}}))
	must(st.CreateWorkflow(ctx, storage.Workflow{ID: "index", Name: "index orders", VHost: "tenant-a",
		Nodes: []storage.WorkflowNode{
			{ID: "s", Type: "source", RefID: "kin"},
			{ID: "p", Type: "transformation", Config: map[string]any{"transType": "ml_predict", "model": "fraud"}},
			{ID: "t", Type: "sink", RefID: "es"},
		}}))
	// tenant-b reads the same table: invisible to tenant-a's users, and
	// tenant-a's lineage is invisible to tenant-b's.
	must(st.CreateSource(ctx, storage.Source{ID: "bpg", Name: "b orders", Type: "postgres", VHost: "tenant-b",
		Config: hermod.StringMap{"host": "db", "port": "5432", "dbname": "shop", "tables": "public.orders"}}))
	must(st.CreateWorkflow(ctx, storage.Workflow{ID: "bwf", Name: "tenant b copy", VHost: "tenant-b",
		Nodes: []storage.WorkflowNode{{ID: "s", Type: "source", RefID: "bpg"}}}))

	s := &Server{Handler: &handlers.Handler{Storage: st, LogStorage: st}}
	srv := httptest.NewServer(s.Routes())
	t.Cleanup(srv.Close)

	call := func(path, role string, vhosts []string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if role != "" {
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
				"id": "u1", "username": "alice", "role": role, "vhosts": vhosts, "exp": time.Now().Add(time.Hour).Unix(),
			}).SignedString([]byte(secret))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	return call
}

func checkLineage(t *testing.T, call lineageCall) {
	t.Helper()
	if code, _ := call("/api/vhosts/tenant-a/lineage", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d, want 401", code)
	}
	if code, _ := call("/api/vhosts/tenant-a/lineage", "Viewer", []string{"tenant-b"}); code != http.StatusForbidden {
		t.Fatalf("tenant-b viewer on tenant-a: %d, want 403", code)
	}

	code, body := call("/api/vhosts/tenant-a/lineage", "Viewer", []string{"tenant-a"})
	if code != http.StatusOK {
		t.Fatalf("graph: %d %s", code, body)
	}
	if strings.Contains(body, "S3cr3t!") {
		t.Fatal("the source password reached the lineage response")
	}
	if strings.Contains(body, "bwf") || strings.Contains(body, "tenant b") {
		t.Fatal("tenant-b's workflow reached tenant-a's lineage")
	}
	var graph struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(body), &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 6 { // 2 workflows, table, topic, index, model
		t.Fatalf("nodes = %+v", graph.Nodes)
	}

	asset := url.QueryEscape("table:postgres://db:5432/shop/public.orders")
	code, body = call("/api/vhosts/tenant-a/lineage/impact?node="+asset, "Viewer", []string{"tenant-a"})
	if code != http.StatusOK {
		t.Fatalf("impact: %d %s", code, body)
	}
	var imp struct {
		Impacted []struct {
			Node struct {
				ID string `json:"id"`
			} `json:"node"`
			Path []string `json:"path"`
		} `json:"impacted"`
	}
	if err := json.Unmarshal([]byte(body), &imp); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range imp.Impacted {
		got = append(got, it.Node.ID)
	}
	want := "workflow:capture topic:kafka://kafka:9092/orders workflow:index index:elasticsearch://es:9200/orders"
	if strings.Join(got, " ") != want {
		t.Fatalf("impacted = %v, want %s", got, want)
	}
	if len(imp.Impacted[3].Path) != 5 {
		t.Errorf("path to the index = %v", imp.Impacted[3].Path)
	}

	// The same table through "all" for tenant-b's user is tenant-b's copy
	// only: tenant-a's workflows are not downstream of it for them.
	code, body = call("/api/vhosts/all/lineage/impact?node="+asset, "Viewer", []string{"tenant-b"})
	if code != http.StatusOK || strings.Contains(body, "capture") || !strings.Contains(body, "workflow:bwf") {
		t.Fatalf("tenant-b impact through all: %d %s", code, body)
	}
}

// GET /api/infra/lineage (the dashboard's source -> workflow -> sink list)
// predates vhost scoping and answered every signed-in user with every
// vhost's sources, sinks and workflows. It now answers each user with the
// vhosts they may see, as the workflow list does.
func TestLegacyInfraLineageIsScopedToTheCallersVHosts(t *testing.T) {
	call := newLineageServer(t)

	if code, _ := call("/api/infra/lineage", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d, want 401", code)
	}
	code, body := call("/api/infra/lineage", "Viewer", []string{"tenant-b"})
	if code != http.StatusOK {
		t.Fatalf("tenant-b viewer: %d %s", code, body)
	}
	if strings.Contains(body, "capture") || strings.Contains(body, "orders db") {
		t.Fatalf("tenant-a's lineage reached a tenant-b viewer: %s", body)
	}
	code, body = call("/api/infra/lineage", "Administrator", nil)
	if code != http.StatusOK || !strings.Contains(body, "capture") {
		t.Fatalf("administrator: %d %s", code, body)
	}
}
