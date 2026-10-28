// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/environment"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstore "github.com/gsoultan/hermod/internal/storage/sql"
	_ "modernc.org/sqlite"
)

// Two real instances, each with its own SQL store and its full route table,
// talking over HTTP the way a staging and a production install do.

type testStore struct {
	storage.Storage
}

func (testStore) CreateAuditLog(context.Context, storage.AuditLog) error { return nil }
func (testStore) CreateLog(context.Context, storage.Log) error           { return nil }

var users = map[string]*storage.User{
	"admin":  {ID: "u-admin", Username: "root", Role: storage.RoleAdministrator, VHosts: []string{"*"}},
	"editor": {ID: "u-ed", Username: "ada", Role: storage.RoleEditor, VHosts: []string{"default"}},
	"viewer": {ID: "u-vw", Username: "vic", Role: storage.RoleViewer, VHosts: []string{"default"}},
}

type instance struct {
	st  storage.Storage
	srv *httptest.Server
}

func newInstance(t *testing.T, name string, role environment.Role) *instance {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:envapi_%s_%s?mode=memory&cache=shared", t.Name(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := sqlstore.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A user exists, so the role guards are not in first-run bypass.
	if err := st.CreateUser(t.Context(), storage.User{ID: "u-admin", Username: "root", Password: "x", Role: storage.RoleAdministrator}); err != nil {
		t.Fatal(err)
	}
	store := testStore{st}
	h := &handlers.Handler{Storage: store, LogStorage: store}
	svc := NewService(h, environment.NewHTTPRemote())
	if _, err := svc.SetInstance(t.Context(), environment.Instance{Name: name, Role: role}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewHandler(h, svc).RegisterRoutes(mux)
	// Stands in for AuthMiddleware: the session's user, when there is one.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := users[r.Header.Get("X-Test-As")]; ok {
			r = r.WithContext(context.WithValue(r.Context(), handlers.UserContextKey, u))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return &instance{st: st, srv: srv}
}

func (in *instance) call(t *testing.T, as, method, path string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(t.Context(), method, in.srv.URL+path, rd)
	req.Header.Set("X-Test-As", as)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := in.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (in *instance) must(t *testing.T, want int, as, method, path string, body, into any) {
	t.Helper()
	code, out := in.call(t, as, method, path, body)
	if code != want {
		t.Fatalf("%s %s as %s = %d, want %d: %s", method, path, as, code, want, out)
	}
	if into != nil {
		if err := json.Unmarshal(out, into); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, out)
		}
	}
}

func configured(t *testing.T) {
	t.Helper()
	// A configured install, which with a user in it is past first run.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"),
		[]byte("type: sqlite\nconn: \"file::memory:\"\njwt_secret: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)
}

// linked is a staging linked to a production, each holding an orders
// connection pair under different ids, with the workflow on staging.
func linked(t *testing.T) (stg, prd *instance, link environment.Link) {
	t.Helper()
	configured(t)
	stg = newInstance(t, "Staging", environment.RoleStaging)
	prd = newInstance(t, "Production", environment.RoleProduction)
	ctx := t.Context()
	for _, c := range []struct {
		st       storage.Storage
		src, snk string
	}{{stg.st, "s-orders", "k-wh"}, {prd.st, "p-orders", "p-wh"}} {
		if err := c.st.CreateSource(ctx, storage.Source{ID: c.src, Name: "orders-db", Type: "postgres", VHost: "default"}); err != nil {
			t.Fatal(err)
		}
		if err := c.st.CreateSink(ctx, storage.Sink{ID: c.snk, Name: "warehouse", Type: "postgres", VHost: "default"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := stg.st.CreateWorkflow(ctx, storage.Workflow{
		ID: "wf-orders", Name: "orders-cdc", VHost: "default",
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "s-orders"},
			{ID: "n2", Type: "sink", RefID: "k-wh"},
		},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "n1", TargetID: "n2"}},
	}); err != nil {
		t.Fatal(err)
	}

	var code struct {
		Code string `json:"code"`
	}
	prd.must(t, http.StatusCreated, "admin", http.MethodPost, "/api/environments/pairing-codes", map[string]string{"label": "staging", "vhost": "default"}, &code)
	stg.must(t, http.StatusCreated, "admin", http.MethodPost, "/api/environments/links",
		map[string]string{"url": prd.srv.URL, "code": code.Code, "self_url": stg.srv.URL, "vhost": "default"}, &link)
	return stg, prd, link
}

func TestAWorkflowGoesFromStagingToProductionOverHTTP(t *testing.T) {
	stg, prd, link := linked(t)
	if link.Name != "Production" || link.Role != environment.RoleProduction {
		t.Fatalf("link = %+v", link)
	}

	var rows []environment.MatchRow
	stg.must(t, http.StatusOK, "admin", http.MethodGet, "/api/environments/links/"+link.ID+"/matching", nil, &rows)
	if len(rows) != 2 {
		t.Fatalf("matching = %+v", rows)
	}
	for _, r := range rows {
		if r.Status != "matched" {
			t.Fatalf("row not matched: %+v", r)
		}
	}

	var sent environment.Promotion
	stg.must(t, http.StatusCreated, "editor", http.MethodPost, "/api/environments/links/"+link.ID+"/promotions",
		map[string]any{"workflow_id": "wf-orders", "note": "first release"}, &sent)
	if sent.Status != environment.StatusPending {
		t.Fatalf("sent = %+v", sent)
	}

	var pending []environment.Promotion
	prd.must(t, http.StatusOK, "admin", http.MethodGet, "/api/environments/promotions", nil, &pending)
	if len(pending) != 1 || pending[0].Sender != "ada" || pending[0].Note != "first release" {
		t.Fatalf("pending = %+v", pending)
	}
	if _, err := prd.st.GetWorkflow(t.Context(), "wf-orders"); err == nil {
		t.Fatal("the workflow landed before anyone approved it")
	}

	var done environment.Promotion
	prd.must(t, http.StatusOK, "admin", http.MethodPost, "/api/environments/promotions/"+pending[0].ID+"/approve", nil, &done)
	if done.Status != environment.StatusDeployed || done.DecidedBy != "root" {
		t.Fatalf("approved = %+v", done)
	}
	wf, err := prd.st.GetWorkflow(t.Context(), "wf-orders")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Nodes[0].RefID != "p-orders" || wf.Nodes[1].RefID != "p-wh" {
		t.Fatalf("production workflow points at %q and %q, not its own connections", wf.Nodes[0].RefID, wf.Nodes[1].RefID)
	}
}

func TestEnvironmentRoutesAreGuarded(t *testing.T) {
	stg, prd, link := linked(t)
	wfBody := map[string]any{"workflow_id": "wf-orders"}
	if err := stg.st.CreateWorkflow(t.Context(), storage.Workflow{ID: "wf-billing", Name: "billing", VHost: "tenant-b"}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		in   *instance
		as   string
		path string
		meth string
		body any
		want int
	}{
		{"a viewer cannot link", stg, "viewer", "/api/environments/links", http.MethodPost, map[string]string{"url": prd.srv.URL}, http.StatusForbidden},
		{"an editor cannot link", stg, "editor", "/api/environments/links", http.MethodPost, map[string]string{"url": prd.srv.URL}, http.StatusForbidden},
		{"an editor cannot issue pairing codes", prd, "editor", "/api/environments/pairing-codes", http.MethodPost, nil, http.StatusForbidden},
		{"a viewer cannot promote", stg, "viewer", "/api/environments/links/" + link.ID + "/promotions", http.MethodPost, wfBody, http.StatusForbidden},
		{"an editor cannot promote another vhost's workflow", stg, "editor", "/api/environments/links/" + link.ID + "/promotions", http.MethodPost, map[string]any{"workflow_id": "wf-billing"}, http.StatusForbidden},
		{"nor preview it", stg, "editor", "/api/environments/links/" + link.ID + "/preview", http.MethodPost, map[string]any{"workflow_id": "wf-billing"}, http.StatusForbidden},
		{"an editor cannot change the role", stg, "editor", "/api/environments/self", http.MethodPut, map[string]string{"name": "x", "role": "production"}, http.StatusForbidden},
		{"a peer route needs a token", prd, "", "/api/environments/peer/info", http.MethodGet, nil, http.StatusUnauthorized},
		{"a session is not a peer token", prd, "admin", "/api/environments/peer/connections", http.MethodGet, nil, http.StatusUnauthorized},
		{"a wrong pairing code is refused", prd, "", "/api/environments/pair", http.MethodPost, map[string]string{"code": "AAAA-AAAA", "name": "x", "url": "http://x"}, http.StatusBadRequest},
		{"a pairing code needs a vhost", prd, "admin", "/api/environments/pairing-codes", http.MethodPost, map[string]string{"label": "x"}, http.StatusBadRequest},
		{"a viewer cannot send a release", stg, "viewer", "/api/environments/releases", http.MethodPost, map[string]any{"items": []any{wfBody}}, http.StatusForbidden},
		{"an editor cannot release another vhost's workflow", stg, "editor", "/api/environments/releases", http.MethodPost, map[string]any{"items": []any{map[string]any{"workflow_id": "wf-billing"}}}, http.StatusForbidden},
		{"nor preview that release", stg, "editor", "/api/environments/releases/preview", http.MethodPost, map[string]any{"items": []any{map[string]any{"workflow_id": "wf-billing"}}}, http.StatusForbidden},
		{"a release from a peer needs its token", prd, "admin", "/api/environments/peer/releases", http.MethodPost, map[string]any{"release_id": "r1"}, http.StatusUnauthorized},
		{"what a peer sent needs its token", prd, "admin", "/api/environments/peer/promotions", http.MethodGet, nil, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, out := c.in.call(t, c.as, c.meth, c.path, c.body); got != c.want {
				t.Fatalf("= %d, want %d: %s", got, c.want, out)
			}
		})
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, prd.srv.URL+"/api/environments/peer/info", nil)
	req.Header.Set("Authorization", "Bearer hmdp_"+strings.Repeat("0", 64))
	res, err := prd.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an invented token got %d", res.StatusCode)
	}
}

func TestARevokedLinkCannotPromote(t *testing.T) {
	stg, prd, link := linked(t)
	var peers []map[string]any
	prd.must(t, http.StatusOK, "admin", http.MethodGet, "/api/environments/peers", nil, &peers)
	if len(peers) != 1 {
		t.Fatalf("peers = %+v", peers)
	}
	if _, leaked := peers[0]["token_hash"]; leaked {
		t.Fatalf("the peer list shows the token hash: %+v", peers[0])
	}
	prd.must(t, http.StatusNoContent, "admin", http.MethodDelete, fmt.Sprintf("/api/environments/peers/%v", peers[0]["id"]), nil, nil)

	code, out := stg.call(t, "editor", http.MethodPost, "/api/environments/links/"+link.ID+"/promotions", map[string]any{"workflow_id": "wf-orders"})
	if code == http.StatusCreated {
		t.Fatalf("a revoked link still promoted: %s", out)
	}
	var none []environment.Promotion
	prd.must(t, http.StatusOK, "admin", http.MethodGet, "/api/environments/promotions", nil, &none)
	if len(none) != 0 {
		t.Fatalf("production holds %+v", none)
	}
}

func TestAReleaseCreatesTheVHostAndGoesLiveWholeOverHTTP(t *testing.T) {
	configured(t)
	stg := newInstance(t, "Staging", environment.RoleStaging)
	prd := newInstance(t, "Production HR", environment.RoleProduction)
	ctx := t.Context()
	if err := stg.st.CreateSource(ctx, storage.Source{ID: "s-people", Name: "people-db", Type: "postgres", VHost: "HR"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"wf-onb", "wf-leave"} {
		if err := stg.st.CreateWorkflow(ctx, storage.Workflow{ID: id, Name: id, VHost: "HR",
			Nodes: []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: "s-people"}}}); err != nil {
			t.Fatal(err)
		}
	}

	var code struct {
		Code string `json:"code"`
	}
	prd.must(t, http.StatusCreated, "admin", http.MethodPost, "/api/environments/pairing-codes", map[string]string{"vhost": "hr"}, &code)
	var link environment.Link
	stg.must(t, http.StatusCreated, "admin", http.MethodPost, "/api/environments/links",
		map[string]string{"url": prd.srv.URL, "code": code.Code, "self_url": stg.srv.URL, "vhost": "HR"}, &link)
	if link.VHost != "HR" || link.TargetVHost != "hr" || !link.VHostCreated {
		t.Fatalf("link = %+v", link)
	}
	vhosts, _, err := prd.st.ListVHosts(ctx, storage.CommonFilter{})
	if err != nil || len(vhosts) != 1 || vhosts[0].Name != "hr" {
		t.Fatalf("production vhosts = %+v, %v; want hr created", vhosts, err)
	}
	var peers []map[string]any
	prd.must(t, http.StatusOK, "admin", http.MethodGet, "/api/environments/peers", nil, &peers)
	if len(peers) != 1 || peers[0]["vhost"] != "hr" || peers[0]["from_vhost"] != "HR" {
		t.Fatalf("peers = %+v", peers)
	}
	if err := prd.st.CreateSource(ctx, storage.Source{ID: "p-people", Name: "people-db", Type: "postgres", VHost: "hr"}); err != nil {
		t.Fatal(err)
	}

	var lanes []environment.ReadyLane
	stg.must(t, http.StatusOK, "admin", http.MethodGet, "/api/environments/ready", nil, &lanes)
	if len(lanes) != 1 || lanes[0].VHost != "HR" || len(lanes[0].Workflows) != 2 {
		t.Fatalf("lanes = %+v", lanes)
	}
	var hidden []environment.ReadyLane
	stg.must(t, http.StatusOK, "editor", http.MethodGet, "/api/environments/ready", nil, &hidden)
	if len(hidden) != 0 {
		t.Fatalf("an editor without HR sees %+v", hidden)
	}

	body := map[string]any{"items": []any{map[string]any{"workflow_id": "wf-onb"}, map[string]any{"workflow_id": "wf-leave"}}, "start": true}
	var pv environment.ReleasePreview
	stg.must(t, http.StatusOK, "admin", http.MethodPost, "/api/environments/releases/preview", body, &pv)
	if !pv.Ready || len(pv.Groups) != 1 || len(pv.Groups[0].Previews) != 2 {
		t.Fatalf("preview = %+v", pv)
	}
	var sent []environment.SentRelease
	stg.must(t, http.StatusCreated, "admin", http.MethodPost, "/api/environments/releases", body, &sent)
	if len(sent) != 1 || len(sent[0].Promotions) != 2 {
		t.Fatalf("sent = %+v", sent)
	}

	var pending []environment.Promotion
	prd.must(t, http.StatusOK, "admin", http.MethodGet, "/api/environments/promotions", nil, &pending)
	if len(pending) != 2 || pending[0].ReleaseID != sent[0].ReleaseID || pending[0].VHost != "hr" {
		t.Fatalf("pending = %+v", pending)
	}
	// The same admin sends and approves here, which the default rule forbids.
	prd.must(t, http.StatusOK, "admin", http.MethodPut, "/api/environments/rules", map[string]any{"approval": "any_admin", "auto_rollback": true}, nil)
	prd.must(t, http.StatusOK, "admin", http.MethodPost, "/api/environments/promotions/"+pending[0].ID+"/approve", nil, nil)
	for _, id := range []string{"wf-onb", "wf-leave"} {
		wf, err := prd.st.GetWorkflow(ctx, id)
		if err != nil || wf.VHost != "hr" || !wf.Active {
			t.Fatalf("%s in production = %+v, %v", id, wf, err)
		}
	}
	var log []environment.Promotion
	stg.must(t, http.StatusOK, "admin", http.MethodGet, "/api/environments/sent", nil, &log)
	if len(log) != 2 || log[0].Status != environment.StatusDeployed || log[1].Status != environment.StatusDeployed {
		t.Fatalf("staging's sent log = %+v", log)
	}
}
