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
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test connection: before a worker is saved, say in one word what is wrong
// with it, against the worker's real routes (ml/worker/hermod_ml/app.py):
// /v2/health/ready answers without a token, /v1/capabilities needs it.
// ---------------------------------------------------------------------------

// probeWorker answers like hermod-ml with the token s3cret, and algorithms on
// /v1/capabilities.
func probeWorker(t *testing.T, algorithms string) string {
	const token = "s3cret"
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/health/ready":
			_, _ = io.WriteString(w, `{"ready":true}`)
		case "/v1/capabilities":
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":"Missing or invalid bearer token."}`)
				return
			}
			_, _ = io.WriteString(w, `{"tasks":["auto"],"algorithms":`+algorithms+`,"unavailable":{}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"Not found."}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// closedAddress is an address nothing listens on.
func closedAddress(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "http://" + addr
}

func TestProbe(t *testing.T) {
	slim := `["auto","random_forest","linear"]`
	deep := `["auto","random_forest","pytorch_mlp","keras_mlp"]`
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<html>some other app</html>")
	}))
	t.Cleanup(other.Close)
	inference := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/health/ready" {
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(inference.Close)

	tests := []struct {
		name         string
		url, token   string
		want         ProbeResult
		deepLearning bool
		frameworks   string
	}{
		{name: "connected, slim image", url: probeWorker(t, slim), token: "s3cret", want: ProbeOK},
		{name: "connected, deep learning", url: probeWorker(t, deep), token: "s3cret", want: ProbeOK,
			deepLearning: true, frameworks: "PyTorch,TensorFlow"},
		{name: "wrong token", url: probeWorker(t, slim), token: "nope", want: ProbeUnauthorized},
		{name: "no token where one is needed", url: probeWorker(t, slim), want: ProbeUnauthorized},
		{name: "nothing listens there", url: closedAddress(t), token: "x", want: ProbeUnreachable},
		{name: "another web server", url: other.URL, token: "x", want: ProbeNotAWorker},
		{name: "an inference server, not hermod-ml", url: inference.URL, token: "x", want: ProbeNotAWorker},
		{name: "not a URL", url: "hermod-ml:8090", want: ProbeBadAddress},
		{name: "not http", url: "ftp://hermod-ml:8090", want: ProbeBadAddress},
		{name: "credentials in the URL", url: "http://user:pw@hermod-ml:8090", want: ProbeBadAddress},
		{name: "empty", url: "", want: ProbeBadAddress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Probe(t.Context(), tt.url, tt.token, nil)
			if got.Result != tt.want {
				t.Fatalf("Result = %q (%s), want %q", got.Result, got.Message, tt.want)
			}
			if got.Message == "" {
				t.Error("no message says what happened")
			}
			if got.DeepLearning != tt.deepLearning || strings.Join(got.Frameworks, ",") != tt.frameworks {
				t.Errorf("deep learning = %v %v, want %v %q", got.DeepLearning, got.Frameworks, tt.deepLearning, tt.frameworks)
			}
			if tt.token != "" && strings.Contains(got.Message, tt.token) {
				t.Errorf("the message repeats the token: %s", got.Message)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"http://hermod-ml:8090", "https://ml.example.com/", "http://10.0.0.5:8090/base"} {
		if _, err := ValidateURL(ok); err != nil {
			t.Errorf("ValidateURL(%q) = %v, want ok", ok, err)
		}
	}
	if got, _ := ValidateURL(" http://hermod-ml:8090/ "); got != "http://hermod-ml:8090" {
		t.Errorf("ValidateURL trims to %q", got)
	}
	for _, bad := range []string{"", "hermod-ml:8090", "localhost", "ftp://x", "http://", "http://u:p@x", "http://x/?q=1", "http://x/#f"} {
		if _, err := ValidateURL(bad); err == nil {
			t.Errorf("ValidateURL(%q) accepted it", bad)
		}
	}
}

func TestAWorkersRefusalOfTheTokenIsErrUnauthorized(t *testing.T) {
	f := &fakeWorker{status: http.StatusUnauthorized, reply: `{"error":"Missing or invalid bearer token."}`}
	_, err := New(f.server(t).URL, "bad", nil).Capabilities(t.Context())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}
