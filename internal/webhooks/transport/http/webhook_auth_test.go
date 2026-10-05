package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/source/graphql"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
)

// The API key on a webhook source.
//
// The source form has an "API Key (Optional)" field, with the line "If
// provided, requests must include 'X-API-Key' header with this value", and it
// saves the key as `api_key`. The endpoint never read it. It checked one
// credential, an HMAC `secret`, which no field in the form writes — so an
// operator who generated a key and saved the source had an endpoint that took
// any request, and a form telling them it did not.

// unreadable is a store whose sources cannot be listed.
type unreadable struct{ storage.Storage }

func (unreadable) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	return nil, 0, errors.New("the sources table is unavailable")
}

func (unreadable) CreateWebhookRequest(context.Context, storage.WebhookRequest) error { return nil }

const webhookBody = `{"order_id":7}`

// deliver posts one webhook to a source with the given config and headers. The
// source's workflow is running: its path is held, so a request that gets past
// the credentials is accepted.
func deliver(t *testing.T, path string, config map[string]string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	fullPath := "/api/webhooks/" + path
	cfg := map[string]string{"path": fullPath}
	for k, v := range config {
		cfg[k] = v
	}
	return deliverTo(t, fullPath, sourcesOnly{sources: []storage.Source{{Type: "webhook", Config: cfg}}}, header)
}

func deliverTo(t *testing.T, fullPath string, store storage.Storage, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewWebhookHandler(&handlers.Handler{Storage: store}).RegisterWebhookRoutes(mux)

	src := webhook.NewWebhookSource(fullPath)
	t.Cleanup(func() { _ = src.Close() })

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, fullPath, strings.NewReader(webhookBody))
	for k, v := range header {
		req.Header[k] = v
	}
	mux.ServeHTTP(rec, req)
	return rec
}

func signature(secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(webhookBody))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestAKeyedWebhookRefusesARequestWithoutItsKey(t *testing.T) {
	config := map[string]string{"api_key": "sesame"}

	if rec := deliver(t, "keyed/none", config, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a request with no key was answered %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if rec := deliver(t, "keyed/wrong", config, http.Header{"X-Api-Key": {"guess"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("a request with the wrong key was answered %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if rec := deliver(t, "keyed/right", config, http.Header{"X-Api-Key": {"sesame"}}); rec.Code != http.StatusAccepted {
		t.Errorf("a request with the right key was answered %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

// A source with no credentials takes any request, as it always has.
func TestAWebhookWithNoCredentialsIsOpen(t *testing.T) {
	if rec := deliver(t, "open", nil, nil); rec.Code != http.StatusAccepted {
		t.Errorf("an unkeyed webhook answered %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

// The signing secret keeps working exactly as it did.
func TestASignedWebhookIsStillVerified(t *testing.T) {
	config := map[string]string{"secret": "s3cret"}

	if rec := deliver(t, "signed/none", config, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("an unsigned request was answered %d, want 401", rec.Code)
	}
	if rec := deliver(t, "signed/bad", config, http.Header{"X-Hub-Signature-256": {signature("other")}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("a request signed with another secret was answered %d, want 401", rec.Code)
	}
	if rec := deliver(t, "signed/good", config, http.Header{"X-Hub-Signature-256": {signature("s3cret")}}); rec.Code != http.StatusAccepted {
		t.Errorf("a correctly signed request was answered %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

// A source with both a key and a signing secret asks for both. Either alone
// would make the second one decoration.
func TestAWebhookWithAKeyAndASecretAsksForBoth(t *testing.T) {
	config := map[string]string{"api_key": "sesame", "secret": "s3cret"}

	if rec := deliver(t, "both/key-only", config, http.Header{"X-Api-Key": {"sesame"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("the key alone was answered %d, want 401", rec.Code)
	}
	if rec := deliver(t, "both/sig-only", config, http.Header{"X-Hub-Signature-256": {signature("s3cret")}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("the signature alone was answered %d, want 401", rec.Code)
	}
	both := http.Header{"X-Api-Key": {"sesame"}, "X-Hub-Signature-256": {signature("s3cret")}}
	if rec := deliver(t, "both/both", config, both); rec.Code != http.StatusAccepted {
		t.Errorf("the key and the signature together were answered %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

// When the store holding the credentials cannot be read, the endpoint cannot
// know whether this path has any. It refuses the request. It used to accept
// it: an error listing the sources read as "no credentials configured", so a
// storage hiccup opened every keyed and signed webhook for as long as it lasted.
func TestAWebhookIsRefusedWhenItsCredentialsCannotBeRead(t *testing.T) {
	rec := deliverTo(t, "/api/webhooks/unreadable", unreadable{}, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a webhook was answered %d while its credentials were unreadable, want 503: %s", rec.Code, rec.Body.String())
	}
}

// The same hole, on the GraphQL source's endpoint.
func TestAGraphQLRequestIsRefusedWhenItsKeyCannotBeRead(t *testing.T) {
	const fullPath = "/api/graphql/unreadable"
	h := NewWebhookHandler(&handlers.Handler{Storage: unreadable{}})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/graphql/{path...}", h.HandleGraphQL)

	src := graphql.NewGraphQLSource(fullPath)
	t.Cleanup(func() { _ = src.Close() })

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, fullPath, strings.NewReader(`{"query":"{ ok }"}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a GraphQL request was answered %d while its key was unreadable, want 503: %s", rec.Code, rec.Body.String())
	}
}
