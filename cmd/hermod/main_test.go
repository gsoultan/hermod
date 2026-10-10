package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// Startup must point the AI nodes at the metrics observer: a model call made
// by a node in this binary, after setupRegistry, shows up in
// hermod_ai_calls_total and hermod_ai_tokens_total.
func TestStartupWiresAIMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"wired-model","choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`))
	}))
	defer srv.Close()
	t.Cleanup(func() { genai.SetObserver(nil) })

	setupRegistry(nil, nil, nil, &Options{configPath: filepath.Join(t.TempDir(), "absent.yaml")})

	calls := telemetry.AICalls.WithLabelValues("openai_compatible", "wired-model", "ok")
	in := telemetry.AITokens.WithLabelValues("openai_compatible", "wired-model", "input")
	beforeCalls, beforeIn := promtestutil.ToFloat64(calls), promtestutil.ToFloat64(in)

	tf, ok := transformer.Get("ai_prompt")
	if !ok {
		t.Fatal("ai_prompt is not linked into the binary")
	}
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	if _, err := tf.Transform(t.Context(), msg, map[string]any{
		"provider": "openai_compatible", "baseUrl": srv.URL, "model": "m", "prompt": "hello",
	}); err != nil {
		t.Fatal(err)
	}

	if d := promtestutil.ToFloat64(calls) - beforeCalls; d != 1 {
		t.Errorf("hermod_ai_calls_total delta = %v, want 1", d)
	}
	if d := promtestutil.ToFloat64(in) - beforeIn; d != 7 {
		t.Errorf("hermod_ai_tokens_total{direction=input} delta = %v, want 7", d)
	}
}

type fakeUserLister struct {
	users []storage.User
	err   error
}

func (f *fakeUserLister) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	// ignore filter for simplicity
	return f.users, len(f.users), nil
}

func TestComputeSetupStatus(t *testing.T) {
	ctx := t.Context()

	cases := []struct {
		name       string
		configured bool
		store      userLister
		wantCfg    bool
		wantUsers  bool
	}{
		{name: "not configured, nil store", configured: false, store: nil, wantCfg: false, wantUsers: false},
		{name: "configured, nil store", configured: true, store: nil, wantCfg: true, wantUsers: true},
		{name: "configured, no users", configured: true, store: &fakeUserLister{users: nil}, wantCfg: true, wantUsers: false},
		{name: "configured, with users", configured: true, store: &fakeUserLister{users: []storage.User{{ID: "u1", Username: "admin"}}}, wantCfg: true, wantUsers: true},
		{name: "configured, list error", configured: true, store: &fakeUserLister{err: context.DeadlineExceeded}, wantCfg: true, wantUsers: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCfg, gotUsers := computeSetupStatus(ctx, tc.store, tc.configured)
			if gotCfg != tc.wantCfg || gotUsers != tc.wantUsers {
				t.Fatalf("computeSetupStatus() = (%v,%v), want (%v,%v)", gotCfg, gotUsers, tc.wantCfg, tc.wantUsers)
			}
		})
	}
}
