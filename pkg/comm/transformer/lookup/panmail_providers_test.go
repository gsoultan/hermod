// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/infra/sharedcall"
)

// panmailGateway answers ListEmailProviders with the given providers and
// records what it was asked.
type panmailGateway struct {
	srv    *httptest.Server
	calls  atomic.Int32
	apiKey atomic.Value
	body   atomic.Value
}

func newPanmailGateway(t *testing.T, status int, reply string) *panmailGateway {
	t.Helper()
	g := &panmailGateway{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.calls.Add(1)
		if r.URL.Path != "/panmail.v1.EmailProviderService/ListEmailProviders" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		g.apiKey.Store(r.Header.Get("X-API-Key"))
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.body.Store(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(g.srv.Close)
	return g
}

const twoProviders = `{"providers":[
  {"id":"p1","name":"Primary","type":"PROVIDER_TYPE_SMTP","allowedSenders":["*@example.com"],
   "smtp":{"host":"smtp.example.com","username":"u"}},
  {"id":"p2","name":"Bulk","type":"PROVIDER_TYPE_SES"}
]}`

func runPanmailProviders(t *testing.T, tr *PanmailProvidersTransformer, reg *cachingFakeRegistry,
	cfg map[string]any, fields map[string]any) (map[string]any, error) {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	out, err := tr.Transform(ctx, msg, cfg)
	if out == nil {
		return nil, err
	}
	return out.Data(), err
}

func newPanmailProvidersFixture() (*PanmailProvidersTransformer, *cachingFakeRegistry) {
	return &PanmailProvidersTransformer{calls: &sharedcall.Group{}}, &cachingFakeRegistry{cache: map[string]any{}}
}

func TestPanmailProvidersIsRegistered(t *testing.T) {
	if _, ok := transformer.Get("panmail_providers"); !ok {
		t.Fatal("panmail_providers is not registered")
	}
}

func TestPanmailProvidersWritesTheListToTheTargetField(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	got, err := runPanmailProviders(t, tr, reg, map[string]any{
		"baseUrl":     g.srv.URL,
		"apiKey":      "key-1",
		"targetField": "providers",
	}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	list, ok := got["providers"].([]map[string]any)
	if !ok || len(list) != 2 {
		t.Fatalf("providers = %#v", got["providers"])
	}
	first := list[0]
	if first["id"] != "p1" || first["name"] != "Primary" || first["type"] != "smtp" {
		t.Fatalf("first = %#v", first)
	}
	if a, _ := first["allowedSenders"].([]string); !slices.Equal(a, []string{"*@example.com"}) {
		t.Fatalf("allowedSenders = %#v", first["allowedSenders"])
	}
	// Workflows written against the old gateway field still read the domains.
	if d, _ := first["allowedDomains"].([]string); !slices.Equal(d, []string{"example.com"}) {
		t.Fatalf("allowedDomains = %#v", first["allowedDomains"])
	}
	if list[1]["type"] != "ses" {
		t.Fatalf("second type = %#v", list[1]["type"])
	}
	// Connection settings never reach the message.
	if _, ok := first["smtp"]; ok {
		t.Fatalf("connection settings leaked into the message: %#v", first)
	}
	if g.apiKey.Load() != "key-1" {
		t.Fatalf("X-API-Key = %v", g.apiKey.Load())
	}
}

// Each result says which domains it may send From: the whole-domain entries of
// its allowed senders, tidied. An exact address grants no domain.
func TestPanmailProvidersAddsTheFromDomains(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, `{"providers":[
  {"id":"p1","name":"Primary","type":"PROVIDER_TYPE_SMTP",
   "allowedSenders":[" *@Example.com","billing@other.example","*@mail.example.com","*@example.com",""]},
  {"id":"p2","name":"Bulk","type":"PROVIDER_TYPE_SES"}
]}`)
	tr, reg := newPanmailProvidersFixture()

	got, err := runPanmailProviders(t, tr, reg, map[string]any{"baseUrl": g.srv.URL, "apiKey": "k"}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	list := got["panmail_providers"].([]map[string]any)

	from, ok := list[0]["fromDomains"].([]string)
	if !ok || !slices.Equal(from, []string{"example.com", "mail.example.com"}) {
		t.Fatalf("fromDomains = %#v", list[0]["fromDomains"])
	}
	// No allowed senders means none to send From, written as an empty list.
	if d, ok := list[1]["fromDomains"].([]string); !ok || len(d) != 0 {
		t.Fatalf("fromDomains with no allowed domains = %#v", list[1]["fromDomains"])
	}
	// No address is invented.
	if _, ok := list[0]["from"]; ok {
		t.Fatalf("a from address was invented: %#v", list[0])
	}
}

// The from email is the node's From mailbox at each domain the provider may
// send From: the gateway returns no address, so the mailbox is the user's.
func TestPanmailProvidersAddsTheFromEmails(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, `{"providers":[
  {"id":"p1","name":"Primary","type":"PROVIDER_TYPE_SMTP","allowedSenders":["*@Example.com","*@mail.example.com"]},
  {"id":"p2","name":"Bulk","type":"PROVIDER_TYPE_SES"}
]}`)

	for name, tc := range map[string]struct {
		mailbox string
		want    string
	}{
		"defaults to noreply": {mailbox: "", want: "noreply"},
		"the mailbox set":     {mailbox: " Billing ", want: "Billing"},
	} {
		t.Run(name, func(t *testing.T) {
			tr, reg := newPanmailProvidersFixture()
			got, err := runPanmailProviders(t, tr, reg, map[string]any{
				"baseUrl": g.srv.URL, "apiKey": "k", "fromMailbox": tc.mailbox,
			}, nil)
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			list := got["panmail_providers"].([]map[string]any)

			wantAll := []string{tc.want + "@example.com", tc.want + "@mail.example.com"}
			if e, ok := list[0]["fromEmails"].([]string); !ok || !slices.Equal(e, wantAll) {
				t.Fatalf("fromEmails = %#v, want %v", list[0]["fromEmails"], wantAll)
			}
			if list[0]["fromEmail"] != wantAll[0] {
				t.Fatalf("fromEmail = %#v, want %q", list[0]["fromEmail"], wantAll[0])
			}
			// No allowed domain, no address to compose.
			if list[1]["fromEmail"] != "" {
				t.Fatalf("fromEmail with no allowed domains = %#v", list[1]["fromEmail"])
			}
			if e, ok := list[1]["fromEmails"].([]string); !ok || len(e) != 0 {
				t.Fatalf("fromEmails with no allowed domains = %#v", list[1]["fromEmails"])
			}
		})
	}
}

// An exact address is a From email as it stands, in the gateway's order with
// the mailbox composed at each whole domain; the mailbox is not applied to it.
func TestPanmailProvidersKeepsExactSendersAsFromEmails(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, `{"providers":[
  {"id":"p1","name":"Primary","type":"PROVIDER_TYPE_SMTP",
   "allowedSenders":["Billing@Example.com","*@example.com","billing@example.com"]}
]}`)
	tr, reg := newPanmailProvidersFixture()

	got, err := runPanmailProviders(t, tr, reg, map[string]any{
		"baseUrl": g.srv.URL, "apiKey": "k", "fromMailbox": "alerts",
	}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	p := got["panmail_providers"].([]map[string]any)[0]

	want := []string{"billing@example.com", "alerts@example.com"}
	if e, ok := p["fromEmails"].([]string); !ok || !slices.Equal(e, want) {
		t.Fatalf("fromEmails = %#v, want %v", p["fromEmails"], want)
	}
	if p["fromEmail"] != want[0] {
		t.Fatalf("fromEmail = %#v, want %q", p["fromEmail"], want[0])
	}
	if d, ok := p["fromDomains"].([]string); !ok || !slices.Equal(d, []string{"example.com"}) {
		t.Fatalf("fromDomains = %#v", p["fromDomains"])
	}
}

func TestPanmailProvidersRefusesAMailboxThatIsNotOne(t *testing.T) {
	for _, mailbox := range []string{"no reply", "a@b.com", "<x>", "a,b"} {
		tr, reg := newPanmailProvidersFixture()
		_, err := runPanmailProviders(t, tr, reg, map[string]any{
			"baseUrl": "https://mail.example.com", "apiKey": "k", "fromMailbox": mailbox,
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "mailbox") {
			t.Fatalf("fromMailbox %q: err = %v, want a mailbox error", mailbox, err)
		}
	}
}

// Two nodes on one tenant with different mailboxes must not share a list.
func TestPanmailProvidersCacheIsPerMailbox(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()
	cfg := func(m string) map[string]any {
		return map[string]any{"baseUrl": g.srv.URL, "apiKey": "k", "fromMailbox": m}
	}
	if _, err := runPanmailProviders(t, tr, reg, cfg("sales"), nil); err != nil {
		t.Fatal(err)
	}
	got, err := runPanmailProviders(t, tr, reg, cfg("billing"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if e := got["panmail_providers"].([]map[string]any)[0]["fromEmail"]; e != "billing@example.com" {
		t.Fatalf("fromEmail = %#v, want billing@example.com", e)
	}
}

func TestPanmailProvidersDefaultsTheTargetField(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	got, err := runPanmailProviders(t, tr, reg, map[string]any{"baseUrl": g.srv.URL, "apiKey": "k"}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if _, ok := got["panmail_providers"]; !ok {
		t.Fatalf("no panmail_providers field: %#v", got)
	}
}

func TestPanmailProvidersSendsTheFilters(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, `{"providers":[{"id":"p2","name":"Bulk","type":"PROVIDER_TYPE_SES"}]}`)
	tr, reg := newPanmailProvidersFixture()

	_, err := runPanmailProviders(t, tr, reg, map[string]any{
		"baseUrl":      g.srv.URL,
		"apiKey":       "k",
		"name":         "{{.region}}",
		"providerType": "ses",
	}, map[string]any{"region": "eu"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	body, _ := g.body.Load().(map[string]any)
	if body["name"] != "eu" || body["type"] != "PROVIDER_TYPE_SES" {
		t.Fatalf("request body = %#v", body)
	}
}

func TestPanmailProvidersRefusesAnUnknownProviderType(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	_, err := runPanmailProviders(t, tr, reg, map[string]any{
		"baseUrl": g.srv.URL, "apiKey": "k", "providerType": "pigeon",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "pigeon") {
		t.Fatalf("err = %v, want it to name the bad type", err)
	}
	if g.calls.Load() != 0 {
		t.Fatalf("the gateway was called %d times", g.calls.Load())
	}
}

func TestPanmailProvidersRequiresGatewayAndKey(t *testing.T) {
	tr, reg := newPanmailProvidersFixture()
	for name, cfg := range map[string]map[string]any{
		"no gateway": {"apiKey": "k"},
		"no key":     {"baseUrl": "https://mail.example.com"},
	} {
		if _, err := runPanmailProviders(t, tr, reg, cfg, nil); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// The gateway and key choose where a tenant credential is sent. Row data must
// not be able to pick either, so they resolve env and secret functions only.
func TestPanmailProvidersDoesNotLetRowDataPickTheGatewayOrKey(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	_, err := runPanmailProviders(t, tr, reg, map[string]any{
		"baseUrl": "{{.gateway}}",
		"apiKey":  "k",
	}, map[string]any{"gateway": g.srv.URL})
	if err == nil {
		t.Fatal("a gateway taken from row data was accepted")
	}
	if g.calls.Load() != 0 {
		t.Fatalf("the row-chosen gateway was called %d times", g.calls.Load())
	}
}

func TestPanmailProvidersReadsTheKeyFromASecret(t *testing.T) {
	t.Setenv("HERMOD_SECRET_PANMAIL_TEST_KEY", "from-secret")
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	_, err := runPanmailProviders(t, tr, reg, map[string]any{
		"baseUrl": g.srv.URL,
		"apiKey":  `{{secret("PANMAIL_TEST_KEY")}}`,
	}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if g.apiKey.Load() != "from-secret" {
		t.Fatalf("X-API-Key = %v, want from-secret", g.apiKey.Load())
	}
}

func TestPanmailProvidersCachesTheList(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()
	cfg := map[string]any{"baseUrl": g.srv.URL, "apiKey": "k"}

	for range 3 {
		if _, err := runPanmailProviders(t, tr, reg, cfg, nil); err != nil {
			t.Fatalf("Transform: %v", err)
		}
	}
	if g.calls.Load() != 1 {
		t.Fatalf("gateway calls = %d, want 1", g.calls.Load())
	}
}

func TestPanmailProvidersCacheIsPerKey(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	for _, key := range []string{"tenant-a", "tenant-b"} {
		if _, err := runPanmailProviders(t, tr, reg, map[string]any{"baseUrl": g.srv.URL, "apiKey": key}, nil); err != nil {
			t.Fatalf("Transform: %v", err)
		}
	}
	if g.calls.Load() != 2 {
		t.Fatalf("gateway calls = %d, want 2 (one per api key)", g.calls.Load())
	}
	for k := range reg.cache {
		if strings.Contains(k, "tenant-a") || strings.Contains(k, "tenant-b") {
			t.Fatalf("api key written into the cache key in the clear: %q", k)
		}
	}
}

func TestPanmailProvidersTTLZeroDisablesTheCache(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()
	cfg := map[string]any{"baseUrl": g.srv.URL, "apiKey": "k", "ttl": "0"}

	for range 2 {
		if _, err := runPanmailProviders(t, tr, reg, cfg, nil); err != nil {
			t.Fatalf("Transform: %v", err)
		}
	}
	if g.calls.Load() != 2 {
		t.Fatalf("gateway calls = %d, want 2", g.calls.Load())
	}
}

func TestPanmailProvidersReportsARefusal(t *testing.T) {
	g := newPanmailGateway(t, http.StatusForbidden, `{"code":"permission_denied","message":"api key lacks providers:read"}`)
	tr, reg := newPanmailProvidersFixture()

	_, err := runPanmailProviders(t, tr, reg, map[string]any{"baseUrl": g.srv.URL, "apiKey": "k"}, nil)
	if err == nil || !strings.Contains(err.Error(), "providers:read") {
		t.Fatalf("err = %v, want the gateway's reason", err)
	}
	if strings.Contains(err.Error(), `"k"`) {
		t.Fatalf("the api key reached the error: %v", err)
	}
}
