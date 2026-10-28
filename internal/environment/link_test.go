// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package environment

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// memStore is an in-memory Store.
type memStore struct {
	mu        sync.Mutex
	settings  map[string]string
	workflows map[string]storage.Workflow
	versions  map[string][]storage.WorkflowVersion
	sources   map[string]storage.Source
	sinks     map[string]storage.Sink
	vhosts    []storage.VHost
}

func newMemStore() *memStore {
	return &memStore{settings: map[string]string{}, workflows: map[string]storage.Workflow{},
		versions: map[string][]storage.WorkflowVersion{}, sources: map[string]storage.Source{}, sinks: map[string]storage.Sink{}}
}

func (m *memStore) ListVHosts(_ context.Context, _ storage.CommonFilter) ([]storage.VHost, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.vhosts), len(m.vhosts), nil
}

func (m *memStore) CreateVHost(_ context.Context, v storage.VHost) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vhosts = append(m.vhosts, v)
	return nil
}

func (m *memStore) hasVHost(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.ContainsFunc(m.vhosts, func(v storage.VHost) bool { return v.Name == name })
}

func (m *memStore) ListWorkflows(_ context.Context, f storage.CommonFilter) ([]storage.Workflow, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.Workflow
	for _, wf := range m.workflows {
		if f.VHost == "" || wf.VHost == f.VHost {
			out = append(out, wf)
		}
	}
	slices.SortFunc(out, func(a, b storage.Workflow) int { return strings.Compare(a.ID, b.ID) })
	return out, len(out), nil
}

func (m *memStore) GetSetting(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.settings[key]
	if !ok {
		return "", storage.ErrNotFound
	}
	return v, nil
}

func (m *memStore) SaveSetting(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[key] = value
	return nil
}

func (m *memStore) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wf, ok := m.workflows[id]
	if !ok {
		return storage.Workflow{}, storage.ErrNotFound
	}
	return wf, nil
}

func (m *memStore) GetWorkflowVersion(_ context.Context, id string, version int) (storage.WorkflowVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.versions[id] {
		if v.Version == version {
			return v, nil
		}
	}
	return storage.WorkflowVersion{}, storage.ErrNotFound
}

// ListWorkflowVersions is newest first, as the real backends are.
func (m *memStore) ListWorkflowVersions(_ context.Context, id string) ([]storage.WorkflowVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Clone(m.versions[id])
	slices.Reverse(out)
	return out, nil
}

func (m *memStore) ListSources(_ context.Context, f storage.CommonFilter) ([]storage.Source, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.Source
	for _, s := range m.sources {
		if f.VHost == "" || s.VHost == f.VHost {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b storage.Source) int { return strings.Compare(a.ID, b.ID) })
	return out, len(out), nil
}

func (m *memStore) ListSinks(_ context.Context, f storage.CommonFilter) ([]storage.Sink, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.Sink
	for _, s := range m.sinks {
		if f.VHost == "" || s.VHost == f.VHost {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b storage.Sink) int { return strings.Compare(a.ID, b.ID) })
	return out, len(out), nil
}

func (m *memStore) GetSource(_ context.Context, id string) (storage.Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sources[id]
	if !ok {
		return storage.Source{}, storage.ErrNotFound
	}
	return s, nil
}

func (m *memStore) GetSink(_ context.Context, id string) (storage.Sink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sinks[id]
	if !ok {
		return storage.Sink{}, storage.ErrNotFound
	}
	return s, nil
}

// saveVersion stores wf and records it as the next version.
func (m *memStore) saveVersion(wf storage.Workflow, by, msg string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workflows[wf.ID] = wf
	cfg := wf
	cfg.Nodes, cfg.Edges = nil, nil
	raw, _ := json.Marshal(cfg)
	n := len(m.versions[wf.ID]) + 1
	m.versions[wf.ID] = append(m.versions[wf.ID], storage.WorkflowVersion{
		WorkflowID: wf.ID, Version: n, Nodes: wf.Nodes, Edges: wf.Edges, Config: string(raw),
		CreatedAt: time.Now().Add(-48 * time.Hour), CreatedBy: by, Message: msg,
	})
	return n
}

// memApplier is the workflow update path over a memStore.
type memApplier struct{ store *memStore }

func (a memApplier) Validate(_ context.Context, wf storage.Workflow) error {
	if wf.Name == "invalid" {
		return errors.New("the workflow is invalid")
	}
	return nil
}

func (a memApplier) Portable(wf storage.Workflow) storage.Workflow {
	wf.Status, wf.WorkerID, wf.OwnerID, wf.LeaseUntil = "", "", "", nil
	wf.TotalProcessed, wf.TotalErrors, wf.TotalLag = 0, 0, 0
	return wf
}

func (a memApplier) ApplyWorkflowChange(_ context.Context, before, after storage.Workflow, by, msg string) (int, int, error) {
	prev := len(a.store.versions[after.ID])
	if prev == 0 {
		prev = a.store.saveVersion(before, by, "before")
	}
	return prev, a.store.saveVersion(after, by, msg), nil
}

func (a memApplier) CreateWorkflowVersioned(_ context.Context, wf storage.Workflow, by, msg string) (int, error) {
	if _, ok := a.store.workflows[wf.ID]; ok {
		return 0, errors.New("exists")
	}
	return a.store.saveVersion(wf, by, msg), nil
}

func (a memApplier) RollbackWorkflowTo(ctx context.Context, id string, version int, by string) (int, error) {
	v, err := a.store.GetWorkflowVersion(ctx, id, version)
	if err != nil {
		return 0, err
	}
	var wf storage.Workflow
	_ = json.Unmarshal([]byte(v.Config), &wf)
	wf.ID, wf.Nodes, wf.Edges = id, v.Nodes, v.Edges
	return a.store.saveVersion(wf, by, "rollback"), nil
}

type memSecrets struct {
	secrets map[string]bool
	envs    map[string]bool
}

func (m memSecrets) HasSecret(_ context.Context, vhost, name string) bool {
	return m.secrets[vhost+"/"+name]
}
func (m memSecrets) HasEnv(name string) bool { return m.envs[name] }

// loopback is a Remote that calls another Service in process, as the HTTP
// handlers would, including the peer token check.
type loopback struct {
	target *Service
	down   bool
	// old makes it answer as an instance from before releases and vhosts.
	old bool
}

var errDown = errors.New("connection refused")

func (l *loopback) peer(ctx context.Context, token string) (Peer, error) {
	if l.down {
		return Peer{}, errDown
	}
	return l.target.AuthenticatePeer(ctx, token)
}

func (l *loopback) Pair(ctx context.Context, _ string, req PairRequest) (PairResult, error) {
	if l.down {
		return PairResult{}, errDown
	}
	return l.target.Pair(ctx, req)
}

func (l *loopback) Info(ctx context.Context, _, token string) (PeerInfo, error) {
	p, err := l.peer(ctx, token)
	if err != nil {
		return PeerInfo{}, err
	}
	info, err := l.target.PeerInfo(ctx, p)
	if l.old {
		info.Features, info.VHost = nil, ""
	}
	return info, err
}

func (l *loopback) Connections(ctx context.Context, _, token string) (Connections, error) {
	p, err := l.peer(ctx, token)
	if err != nil {
		return Connections{}, err
	}
	return l.target.PeerConnections(ctx, p)
}

func (l *loopback) Check(ctx context.Context, _, token string, req CheckRequest) (CheckResult, error) {
	p, err := l.peer(ctx, token)
	if err != nil {
		return CheckResult{}, err
	}
	return l.target.PeerCheck(ctx, p, req)
}

func (l *loopback) SubmitRelease(ctx context.Context, _, token string, rs ReleaseSubmission) ([]Promotion, error) {
	p, err := l.peer(ctx, token)
	if err != nil {
		return nil, err
	}
	if l.old {
		return nil, &RemoteError{Status: 404, Message: "404 page not found"}
	}
	return l.target.ReceiveRelease(ctx, p, rs)
}

func (l *loopback) Promotions(ctx context.Context, _, token string) ([]Promotion, error) {
	p, err := l.peer(ctx, token)
	if err != nil {
		return nil, err
	}
	if l.old {
		return nil, &RemoteError{Status: 405, Message: "method not allowed"}
	}
	return l.target.PeerPromotions(ctx, p)
}

func (l *loopback) Submit(ctx context.Context, _, token string, sub Submission) (Promotion, error) {
	p, err := l.peer(ctx, token)
	if err != nil {
		return Promotion{}, err
	}
	return l.target.Receive(ctx, p, sub)
}

func (l *loopback) Unpair(ctx context.Context, _, token string) error {
	p, err := l.peer(ctx, token)
	if err != nil {
		return err
	}
	return l.target.RevokePeer(ctx, p.ID)
}

// pair is a staging and a production instance, not yet linked.
type pair struct {
	stg, prd           *Service
	stgStore, prdStore *memStore
	remote             *loopback
	secrets            *memSecrets
}

func newPair(t *testing.T) *pair {
	t.Helper()
	ctx := context.Background()
	p := &pair{stgStore: newMemStore(), prdStore: newMemStore(),
		secrets: &memSecrets{secrets: map[string]bool{}, envs: map[string]bool{}}}
	p.prd = NewService(p.prdStore, memApplier{p.prdStore}, nil, p.secrets, "1.20.0")
	p.remote = &loopback{target: p.prd}
	p.stg = NewService(p.stgStore, memApplier{p.stgStore}, p.remote, nil, "1.20.0")
	if _, err := p.stg.SetInstance(ctx, Instance{Name: "Staging", Role: RoleStaging}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.prd.SetInstance(ctx, Instance{Name: "Production", Role: RoleProduction}); err != nil {
		t.Fatal(err)
	}
	return p
}

// link links staging's default vhost to production's with a fresh code.
func (p *pair) link(t *testing.T) Link {
	t.Helper()
	ctx := context.Background()
	code, _, err := p.prd.NewPairingCode(ctx, "", "default", "rina")
	if err != nil {
		t.Fatal(err)
	}
	l, err := p.stg.LinkWith(ctx, LinkRequest{URL: "https://prod.example.com/", Code: code, SelfURL: "https://stg.example.com", VHost: "default"}, "gembit")
	if err != nil {
		t.Fatalf("LinkWith: %v", err)
	}
	return l
}

func TestPairingLinksInstancesAndCodeWorksOnce(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	code, expires, err := p.prd.NewPairingCode(ctx, "Staging", "default", "rina")
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(expires) > PairingTTL || time.Until(expires) < PairingTTL-time.Minute {
		t.Fatalf("expires %v, want about %v from now", expires, PairingTTL)
	}

	// Typed by a person: lower case, no dash.
	l, err := p.stg.LinkWith(ctx, LinkRequest{URL: "https://prod.example.com/", Code: strings.ToLower(strings.ReplaceAll(code, "-", "")), SelfURL: "https://stg.example.com", VHost: "default"}, "gembit")
	if err != nil {
		t.Fatalf("LinkWith: %v", err)
	}
	if l.Name != "Production" || l.Role != RoleProduction || l.URL != "https://prod.example.com" || !l.Reachable {
		t.Fatalf("link = %+v", l)
	}
	peers, _ := p.prd.Peers(ctx)
	if len(peers) != 1 || peers[0].Name != "Staging" || peers[0].URL != "https://stg.example.com" || peers[0].CreatedBy != "rina" {
		t.Fatalf("peers = %+v", peers)
	}

	// The token is stored encrypted, never in the clear.
	token, err := loadLinkToken(ctx, p.stgStore, l.ID)
	if err != nil || !strings.HasPrefix(token, tokenPrefix) {
		t.Fatalf("token = %q, %v", token, err)
	}
	raw := p.stgStore.settings[storage.EnvironmentLinkTokensKey]
	if strings.Contains(raw, token) || !strings.Contains(raw, "enc:") {
		t.Fatalf("the stored token is not encrypted: %s", raw)
	}
	if strings.Contains(p.stgStore.settings[keyLinks], token) {
		t.Fatal("the token leaked into the links setting")
	}
	if peers[0].TokenHash == token || strings.Contains(p.prdStore.settings[keyPeers], token) {
		t.Fatal("production stores the token itself rather than its hash")
	}

	if _, err := p.stg.LinkWith(ctx, LinkRequest{URL: "https://other.example.com", Code: code, VHost: "default"}, "gembit"); !errors.Is(err, ErrBadPairingCode) {
		t.Fatalf("second use of a code: err = %v, want ErrBadPairingCode", err)
	}
}

func TestPairingCodeExpires(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	code, _, err := p.prd.NewPairingCode(ctx, "", "default", "rina")
	if err != nil {
		t.Fatal(err)
	}
	p.prd.now = func() time.Time { return time.Now().Add(PairingTTL + time.Second) }
	if _, err := p.prd.Pair(ctx, PairRequest{Code: code}); !errors.Is(err, ErrBadPairingCode) {
		t.Fatalf("expired code: err = %v, want ErrBadPairingCode", err)
	}
}

func TestPairingGuessesClearOutstandingCodes(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	code, _, err := p.prd.NewPairingCode(ctx, "", "default", "rina")
	if err != nil {
		t.Fatal(err)
	}
	for range maxPairFailures {
		if _, err := p.prd.Pair(ctx, PairRequest{Code: "AAAA-AAAA"}); !errors.Is(err, ErrBadPairingCode) {
			t.Fatalf("wrong code: err = %v", err)
		}
	}
	if _, err := p.prd.Pair(ctx, PairRequest{Code: code}); !errors.Is(err, ErrBadPairingCode) {
		t.Fatalf("after %d guesses the real code still works: err = %v", maxPairFailures, err)
	}
}

func TestAuthenticatePeer(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	l := p.link(t)
	token, _ := loadLinkToken(ctx, p.stgStore, l.ID)

	peer, err := p.prd.AuthenticatePeer(ctx, token)
	if err != nil || peer.Name != "Staging" {
		t.Fatalf("good token: %+v, %v", peer, err)
	}
	for _, bad := range []string{"", "hmdp_", tokenPrefix + strings.Repeat("0", 64), "Bearer " + token, token + "x"} {
		if _, err := p.prd.AuthenticatePeer(ctx, bad); !errors.Is(err, ErrForbidden) {
			t.Errorf("token %q: err = %v, want ErrForbidden", bad, err)
		}
	}
	if err := p.prd.RevokePeer(ctx, peer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.prd.AuthenticatePeer(ctx, token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked token: err = %v, want ErrForbidden", err)
	}
}

func TestUnlinkRevokesTheTokenOnTheOtherSide(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	l := p.link(t)
	token, _ := loadLinkToken(ctx, p.stgStore, l.ID)

	note, err := p.stg.Unlink(ctx, l.ID)
	if err != nil || note != "" {
		t.Fatalf("Unlink: %q, %v", note, err)
	}
	if links, _ := p.stg.Links(ctx); len(links) != 0 {
		t.Fatalf("links = %+v", links)
	}
	if left, _ := loadLinkToken(ctx, p.stgStore, l.ID); left != "" {
		t.Fatal("the token was kept after unlinking")
	}
	if _, err := p.prd.AuthenticatePeer(ctx, token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("production still accepts the token: %v", err)
	}
}

func TestUnlinkWhileTheOtherSideIsDownSaysSo(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	l := p.link(t)
	p.remote.down = true
	note, err := p.stg.Unlink(ctx, l.ID)
	if err != nil || !strings.Contains(note, "revoke") {
		t.Fatalf("Unlink: note %q, err %v", note, err)
	}
	if links, _ := p.stg.Links(ctx); len(links) != 0 {
		t.Fatalf("links = %+v", links)
	}
}

func TestTestLinkRecordsReachability(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	l := p.link(t)
	if _, err := p.prd.SetRules(ctx, Rules{Approval: ApproveAnyAdmin, MinSoakMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	got, info, err := p.stg.TestLink(ctx, l.ID)
	if err != nil || !got.Reachable || info.Rules.MinSoakMinutes != 60 || info.Version != "1.20.0" {
		t.Fatalf("up: %+v %+v %v", got, info, err)
	}
	p.remote.down = true
	got, _, err = p.stg.TestLink(ctx, l.ID)
	if err == nil || got.Reachable || got.CheckNote == "" {
		t.Fatalf("down: %+v %v", got, err)
	}
}

func TestValidateURL(t *testing.T) {
	padded := " http://10.0.0.5:4000 " // pasted with spaces around it
	for raw, ok := range map[string]bool{
		"https://hermod.example.com/": true,
		padded:                        true,
		"ftp://x":                     false,
		"hermod.example.com":          false,
		"https://u:p@x.example.com":   false,
		"https://x.example.com/?a=1":  false,
		"https://x.example.com/#f":    false,
		"":                            false,
	} {
		_, err := ValidateURL(raw)
		if (err == nil) != ok {
			t.Errorf("ValidateURL(%q) err = %v, want ok=%v", raw, err, ok)
		}
	}
}

func TestResolve(t *testing.T) {
	targets := []Connection{
		{ID: "p-orders", Name: "orders-db", Type: "postgres", VHost: "default"},
		{ID: "p-orders-eu", Name: "orders-db", Type: "postgres", VHost: "eu"},
		{ID: "p-kafka", Name: "Kafka Events", Type: "kafka", VHost: "default"},
		{ID: "p-wh1", Name: "warehouse", Type: "snowflake", VHost: "a"},
		{ID: "p-wh2", Name: "warehouse", Type: "snowflake", VHost: "b"},
	}
	cases := []struct {
		name    string
		c       Connection
		mapping map[string]string
		want    string
		status  string
	}{
		{"same name and vhost", Connection{ID: "s1", Name: "orders-db", Type: "postgres", VHost: "eu"}, nil, "p-orders-eu", MatchMatched},
		{"name is case-insensitive", Connection{ID: "s2", Name: "kafka events", Type: "kafka", VHost: "x"}, nil, "p-kafka", MatchMatched},
		{"type must agree", Connection{ID: "s3", Name: "orders-db", Type: "mysql", VHost: "default"}, nil, "", MatchMissing},
		{"environment suffix suggests", Connection{ID: "s4", Name: "Kafka Events-stg", Type: "kafka", VHost: "default"}, nil, "p-kafka", MatchSuggested},
		{"ambiguous across vhosts", Connection{ID: "s5", Name: "warehouse", Type: "snowflake", VHost: "c"}, nil, "", MatchMissing},
		{"hand-made match wins", Connection{ID: "s6", Name: "anything", Type: "postgres"}, map[string]string{"s6": "p-orders"}, "p-orders", MatchMatched},
		{"hand-made match to a deleted connection", Connection{ID: "s7", Name: "orders-db", Type: "postgres", VHost: "default"}, map[string]string{"s7": "gone"}, "", MatchMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, status, _ := resolve(tc.c, tc.mapping, targets)
			if status != tc.status || got.ID != tc.want {
				t.Fatalf("resolve = %q %s, want %q %s", got.ID, status, tc.want, tc.status)
			}
		})
	}
}

func TestMatchingListsWhatNeedsAPersonFirst(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	p.stgStore.sources["s-orders"] = storage.Source{ID: "s-orders", Name: "orders-db", Type: "postgres", VHost: "default"}
	p.stgStore.sources["s-crm"] = storage.Source{ID: "s-crm", Name: "crm-stg", Type: "mysql", VHost: "default"}
	p.stgStore.sinks["k-slack"] = storage.Sink{ID: "k-slack", Name: "fraud-alerts", Type: "slack", VHost: "default"}
	p.prdStore.sources["p-orders"] = storage.Source{ID: "p-orders", Name: "orders-db", Type: "postgres", VHost: "default"}
	p.prdStore.sources["p-crm"] = storage.Source{ID: "p-crm", Name: "crm", Type: "mysql", VHost: "default"}
	l := p.link(t)

	rows, err := p.stg.Matching(ctx, l.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.ID+":"+r.Status+":"+r.TargetID)
	}
	want := []string{"k-slack:missing:", "s-crm:suggested:p-crm", "s-orders:matched:p-orders"}
	if !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}

	// Confirming the suggestion makes it a match; forgetting it brings the
	// suggestion back.
	if _, err := p.stg.SetMapping(ctx, l.ID, map[string]string{"s-crm": "p-crm"}); err != nil {
		t.Fatal(err)
	}
	rows, _ = p.stg.Matching(ctx, l.ID, "")
	if i := slices.IndexFunc(rows, func(r MatchRow) bool { return r.ID == "s-crm" }); rows[i].Status != MatchMatched || !rows[i].Manual {
		t.Fatalf("after confirming: %+v", rows[i])
	}
	if _, err := p.stg.SetMapping(ctx, l.ID, map[string]string{"s-crm": ""}); err != nil {
		t.Fatal(err)
	}
	rows, _ = p.stg.Matching(ctx, l.ID, "")
	if i := slices.IndexFunc(rows, func(r MatchRow) bool { return r.ID == "s-crm" }); rows[i].Status != MatchSuggested {
		t.Fatalf("after forgetting: %+v", rows[i])
	}
}

func TestRulesAndInstanceAreValidated(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	if _, err := p.prd.SetRules(ctx, Rules{Approval: "whoever"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad approval: %v", err)
	}
	if _, err := p.prd.SetInstance(ctx, Instance{Role: "qa"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad role: %v", err)
	}
	r, err := p.stg.Rules(ctx)
	if err != nil || !reflect.DeepEqual(r, DefaultRules()) {
		t.Fatalf("default rules = %+v, %v", r, err)
	}
}
