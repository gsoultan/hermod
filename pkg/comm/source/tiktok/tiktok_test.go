package tiktok_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/source/tiktok"
)

// TestTikTokSourceSendsItsTokenOnlyInTheAuthorizationHeader: the source sent
// its access token twice, as a Bearer header and again as ?access_token= in the
// URL. The header is what TikTok's v2 API authenticates with; the copy in the
// URL only added places for the token to be recorded — the error text of every
// failed request, and the access log of every proxy on the way.
func TestTikTokSourceSendsItsTokenOnlyInTheAuthorizationHeader(t *testing.T) {
	const token = "S3CR3Tsentinel"

	type seen struct{ query, auth string }
	var (
		mu       sync.Mutex
		requests []seen
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, seen{query: r.URL.RawQuery, auth: r.Header.Get("Authorization")})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"videos":[{"id":"v1"}],"cursor":1}}`))
	}))
	defer srv.Close()

	for _, mode := range []string{"video", "comments", "statistics"} {
		t.Run(mode, func(t *testing.T) {
			mu.Lock()
			requests = nil
			mu.Unlock()

			src := tiktok.NewTikTokSource(token, time.Millisecond, mode)
			src.SetBaseURL(srv.URL)
			defer src.Close()

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if _, err := src.Read(ctx); err != nil {
				t.Fatalf("Read: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(requests) == 0 {
				t.Fatal("the source made no request")
			}
			for _, r := range requests {
				if strings.Contains(r.query, token) {
					t.Errorf("token sent in the URL query: %q", r.query)
				}
				if r.auth != "Bearer "+token {
					t.Errorf("Authorization = %q, want the Bearer token", r.auth)
				}
			}
		})
	}
}
