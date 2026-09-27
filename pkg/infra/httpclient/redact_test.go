package httpclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/infra/httpclient"
)

// secret stands in for a token: distinctive, so a match can only mean the
// credential itself reached the error text.
const secret = "S3CR3Tsentinel"

// The URL shapes the connectors actually build, each carrying its credential
// somewhere net/http does not strip.
func TestRedactURLErrorRemovesCredentialsFromTheURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		host string
	}{
		{"telegram bot path", "https://api.telegram.org/bot123456:" + secret + "/sendMessage", "api.telegram.org"},
		{"graph api query", "https://graph.facebook.com/v17.0/1/feed?access_token=" + secret + "&message=hi", "graph.facebook.com"},
		{"slack webhook", "https://hooks.slack.com/services/T000/B000/" + secret, "hooks.slack.com"},
		{"discord webhook", "https://discord.com/api/webhooks/1/" + secret, "discord.com"},
		{"userinfo", "https://" + secret + ":" + secret + "@example.com/hook", "example.com"},
		{"fragment", "https://example.com#" + secret, "example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := httpclient.RedactURLError(&url.Error{Op: "Post", URL: tc.url, Err: errors.New("connection refused")})
			msg := err.Error()
			if strings.Contains(msg, secret) {
				t.Fatalf("credential survived: %s", msg)
			}
			// A transport failure is diagnosed by which server could not be
			// reached. Redaction that took the host too would leave an operator
			// guessing.
			if !strings.Contains(msg, tc.host) {
				t.Errorf("host %q was removed along with the credential: %s", tc.host, msg)
			}
		})
	}
}

// Hand-built errors prove the function; these prove it against what net/http
// really returns, which is what the connectors get.
func TestRedactURLErrorOnRealNetHTTPErrors(t *testing.T) {
	t.Run("transport failure", func(t *testing.T) {
		// Port 1 needs root to bind, so nothing is listening there.
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
			"http://127.0.0.1:1/bot123456:"+secret+"/getMe?access_token="+secret, nil)
		if err != nil {
			t.Fatalf("NewRequestWithContext: %v", err)
		}
		resp, err := (&http.Client{}).Do(req)
		if err == nil {
			_ = resp.Body.Close()
			t.Fatal("expected a connection error from a port nothing listens on")
		}
		if !strings.Contains(err.Error(), secret) {
			t.Fatalf("precondition: net/http is expected to quote the URL in its error, got %v", err)
		}
		if got := httpclient.RedactURLError(err).Error(); strings.Contains(got, secret) {
			t.Errorf("credential survived: %s", got)
		}
	})

	t.Run("pasted token fails parsing", func(t *testing.T) {
		// A token copied from a web page often brings its newline along. The
		// URL then fails to parse, and the parse error quotes it too.
		_, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.telegram.org/bot123456:"+secret+"\n/getMe", nil)
		if err == nil {
			t.Fatal("expected a parse error for a URL containing a newline")
		}
		if !strings.Contains(err.Error(), secret) {
			t.Fatalf("precondition: the parse error is expected to quote the URL, got %v", err)
		}
		if got := httpclient.RedactURLError(err).Error(); strings.Contains(got, secret) {
			t.Errorf("credential survived: %s", got)
		}
	})
}

// Redaction changes the text and nothing else. The engine tells a poll timeout
// from a failure with errors.Is(err, context.DeadlineExceeded), and callers
// classify transient failures with Timeout().
func TestRedactURLErrorKeepsTheErrorChain(t *testing.T) {
	err := httpclient.RedactURLError(&url.Error{
		Op:  "Get",
		URL: "https://graph.facebook.com/v17.0/1?access_token=" + secret,
		Err: context.DeadlineExceeded,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) = false after redaction: %v", err)
	}
	var timeout interface{ Timeout() bool }
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Errorf("Timeout() lost after redaction: %v", err)
	}
	var ue *url.Error
	if !errors.As(err, &ue) || ue.Op != "Get" {
		t.Errorf("want a *url.Error with Op %q, got %#v", "Get", err)
	}
}

func TestRedactURLErrorLeavesEverythingElseAlone(t *testing.T) {
	if got := httpclient.RedactURLError(nil); got != nil {
		t.Errorf("RedactURLError(nil) = %v, want nil", got)
	}

	plain := errors.New("telegram api returned status: 401")
	if got := httpclient.RedactURLError(plain); !errors.Is(got, plain) || got.Error() != plain.Error() {
		t.Errorf("an error that is not a *url.Error was changed: %v", got)
	}

	// Nothing after the host means nothing to hide, and a marker there would
	// only suggest something had been removed.
	bare := &url.Error{Op: "Get", URL: "http://127.0.0.1:1", Err: errors.New("connection refused")}
	if got := httpclient.RedactURLError(bare).Error(); got != bare.Error() {
		t.Errorf("a URL with nothing after its host was changed: %s", got)
	}

	// Without a scheme there is no telling where the host ends, so none of it
	// is kept.
	noScheme := &url.Error{Op: "parse", URL: "api.telegram.org/bot123456:" + secret, Err: errors.New("invalid")}
	if got := httpclient.RedactURLError(noScheme).Error(); strings.Contains(got, secret) {
		t.Errorf("credential survived in a URL without a scheme: %s", got)
	}
}
