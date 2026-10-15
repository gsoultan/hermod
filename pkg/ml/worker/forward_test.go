// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package worker

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForwardSendsTheRequestToTheWorkerWithItsToken(t *testing.T) {
	var got struct{ method, path, query, auth, ctype, body string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.method, got.path, got.query = r.Method, r.URL.Path, r.URL.RawQuery
		got.auth, got.ctype, got.body = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"error":"short and stout"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "s3cret", srv.Client())
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, LocalURL+"/v1/datasets/v/d/file?format=csv", strings.NewReader("a,b\n1,2\n"))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer not-for-the-worker")
	rec := httptest.NewRecorder()
	c.Forward(rec, req)

	if got.method != http.MethodPut || got.path != "/v1/datasets/v/d/file" || got.query != "format=csv" {
		t.Errorf("worker saw %s %s?%s", got.method, got.path, got.query)
	}
	if got.auth != "Bearer s3cret" || got.ctype != "application/octet-stream" || got.body != "a,b\n1,2\n" {
		t.Errorf("worker saw auth %q, type %q, body %q", got.auth, got.ctype, got.body)
	}
	if rec.Code != http.StatusTeapot || rec.Body.String() != `{"error":"short and stout"}` ||
		rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("forwarded reply = %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestForwardToAWorkerThatDoesNotAnswerIsABadGateway(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c := New(url, "", nil)
	rec := httptest.NewRecorder()
	c.Forward(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, LocalURL+"/v1/datasets/v", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "the ML worker") {
		t.Errorf("reply = %d %s", rec.Code, rec.Body.String())
	}
}

// routedLocal is an in-process engine that routes to a remote worker.
type routedLocal struct {
	fakeLocal
	remote *Client
}

func (r *routedLocal) Remote() *Client { return r.remote }

func TestRemoteIsTheWorkerOverHTTP(t *testing.T) {
	remote := New("http://hermod-ml:8090", "", nil)
	if remote.Remote() != remote {
		t.Error("a worker reached over HTTP is its own remote")
	}
	if NewLocal(&fakeLocal{}).Remote() != nil {
		t.Error("an in-process engine alone has no remote worker")
	}
	if NewLocal(&routedLocal{remote: remote}).Remote() != remote {
		t.Error("an engine that routes to a worker reports that worker")
	}
	if NewLocal(&routedLocal{}).Remote() != nil {
		t.Error("an engine that routes to no worker reports none")
	}
}

func TestProviderCombinesTheBuiltinEngineWithTheWorker(t *testing.T) {
	saved := Configure("http://hermod-ml:8090", "t")
	var routedFor []*Client
	route := func(remote *Client) *Client {
		routedFor = append(routedFor, remote)
		return NewLocal(&routedLocal{remote: remote})
	}
	p := NewProvider(func() *Client { return nil })
	p.SetEngine(route)

	c, src := p.Current()
	if src != SourceBuiltin || c.Remote() != nil {
		t.Fatalf("with no worker, current = %v from %s; want the built-in engine alone", c, src)
	}
	p.SetSaved(saved)
	c, src = p.Current()
	if src != SourceSaved || c.Remote() != saved || c.Local() == nil {
		t.Fatalf("with a saved worker, current = %+v from %s; want the built-in engine routing to it", c, src)
	}
	if again, _ := p.Current(); again != c {
		t.Error("the combined client is built anew on every call")
	}
	p.SetSaved(nil)
	if c, src = p.Current(); src != SourceBuiltin || c.Remote() != nil {
		t.Errorf("after disconnecting, current = %+v from %s; want the built-in engine alone", c, src)
	}
	if p.Builtin() == nil || p.Builtin().Remote() != nil {
		t.Error("Builtin() is not the built-in engine alone")
	}
	if len(routedFor) != 2 {
		t.Errorf("route called %d times, want twice (no worker, then the saved one)", len(routedFor))
	}

	env := NewProvider(func() *Client { return saved })
	env.SetEngine(route)
	if c, src := env.Current(); src != SourceEnvironment || c.Remote() != saved {
		t.Errorf("with the environment's worker, current = %+v from %s", c, src)
	}
}
