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
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod/internal/storage"
)

const (
	// codeAlphabet leaves out 0, O, 1 and I, which are read aloud wrongly.
	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	codeLength   = 8
	// maxPairFailures wrong codes clear every outstanding code, so guessing
	// one costs a fresh code from an admin every few tries.
	maxPairFailures = 10
	maxPendingCodes = 20
	tokenPrefix     = "hmdp_"
	maxNameLen      = 64
	// lastUsedEvery is how stale a peer's last-used time may get before a
	// request rewrites it.
	lastUsedEvery = time.Minute
)

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomCode() (string, error) {
	b := make([]byte, codeLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 0, codeLength+1)
	for i, v := range b {
		if i == codeLength/2 {
			out = append(out, '-')
		}
		out = append(out, codeAlphabet[int(v)%len(codeAlphabet)])
	}
	return string(out), nil
}

// normalizeCode accepts a code as a person types it: any case, with or
// without the dash and spaces.
func normalizeCode(code string) string {
	code = strings.ToUpper(code)
	code = strings.NewReplacer("-", "", " ", "").Replace(code)
	if len(code) != codeLength {
		return code
	}
	return code[:codeLength/2] + "-" + code[codeLength/2:]
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return tokenPrefix + hex.EncodeToString(b), nil
}

func cleanName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fallback
	}
	if len(name) > maxNameLen {
		name = name[:maxNameLen]
	}
	return name
}

// NewPairingCode issues a one-time code another instance redeems to link one
// of its vhosts to vhost here. label says who it is for; it is shown, never
// trusted. vhost need not exist yet: it is created when the code is used.
func (s *Service) NewPairingCode(ctx context.Context, label, vhost, username string) (string, time.Time, error) {
	if err := ValidateVHostName(vhost); err != nil {
		return "", time.Time{}, err
	}
	if vhost == "all" {
		return "", time.Time{}, fmt.Errorf("%w: \"all\" stands for every vhost; pick one", ErrInvalid)
	}
	code, err := randomCode()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now().UTC()
	expires := now.Add(PairingTTL)

	s.mu.Lock()
	defer s.mu.Unlock()
	codes, err := loadPairing(ctx, s.store)
	if err != nil {
		return "", time.Time{}, err
	}
	codes = slices.DeleteFunc(codes, func(c PairingCode) bool { return !now.Before(c.ExpiresAt) })
	if len(codes) >= maxPendingCodes {
		codes = codes[len(codes)-maxPendingCodes+1:]
	}
	codes = append(codes, PairingCode{
		Hash: hashSecret(code), Label: cleanName(label, ""), VHost: vhost, CreatedBy: username, ExpiresAt: expires,
	})
	if err := putJSON(ctx, s.store, keyPairing, codes); err != nil {
		return "", time.Time{}, err
	}
	return code, expires, nil
}

// spend finds code among codes and removes it.
func spend(codes []PairingCode, code string) (PairingCode, []PairingCode, bool) {
	want := hashSecret(code)
	i := slices.IndexFunc(codes, func(c PairingCode) bool {
		return subtle.ConstantTimeCompare([]byte(c.Hash), []byte(want)) == 1
	})
	if i < 0 {
		return PairingCode{}, codes, false
	}
	used := codes[i]
	return used, slices.Delete(codes, i, i+1), true
}

// pairFailed counts a wrong code, clearing every outstanding code after too
// many in a row, and saves what is left. Called with s.mu held.
func (s *Service) pairFailed(ctx context.Context, codes []PairingCode) error {
	s.pairFailures++
	if s.pairFailures >= maxPairFailures {
		codes = nil
		s.pairFailures = 0
	}
	if err := putJSON(ctx, s.store, keyPairing, codes); err != nil {
		return err
	}
	return ErrBadPairingCode
}

// Pair redeems a pairing code: the instance presenting it becomes a peer
// allowed to send promotions here, and gets the token it does that with.
func (s *Service) Pair(ctx context.Context, req PairRequest) (PairResult, error) {
	code := normalizeCode(req.Code)
	now := s.now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	codes, err := loadPairing(ctx, s.store)
	if err != nil {
		return PairResult{}, err
	}
	codes = slices.DeleteFunc(codes, func(c PairingCode) bool { return !now.Before(c.ExpiresAt) })
	used, codes, ok := spend(codes, code)
	if !ok {
		return PairResult{}, s.pairFailed(ctx, codes)
	}

	token, err := randomToken()
	if err != nil {
		return PairResult{}, err
	}
	peerURL, err := ValidateURL(req.URL)
	if err != nil {
		peerURL = ""
	}
	peers, err := loadPeers(ctx, s.store)
	if err != nil {
		return PairResult{}, err
	}
	peer := Peer{
		ID: uuid.NewString(), Name: cleanName(req.Name, cleanName(used.Label, "Linked instance")), URL: peerURL,
		TokenHash: hashSecret(token), CreatedBy: used.CreatedBy, CreatedAt: now,
		VHost: used.VHost, FromVHost: cleanName(req.VHost, ""),
	}
	peers = append(peers, peer)
	// The code is spent before anything else is saved: a failure after it
	// costs the person a new code, never leaves one that can be used twice.
	if err := putJSON(ctx, s.store, keyPairing, codes); err != nil {
		return PairResult{}, err
	}
	created, err := s.ensureVHost(ctx, used.VHost, peer.Name)
	if err != nil {
		return PairResult{}, err
	}
	if err := putJSON(ctx, s.store, keyPeers, peers); err != nil {
		return PairResult{}, err
	}
	s.pairFailures = 0
	self, err := loadInstance(ctx, s.store)
	if err != nil {
		return PairResult{}, err
	}
	return PairResult{PeerID: peer.ID, Token: token, Instance: self, Version: s.version, VHost: used.VHost, VHostCreated: created}, nil
}

// ensureVHost creates vhost unless it exists, and reports whether it did.
// A code issued before codes named a vhost has none, and creates nothing.
func (s *Service) ensureVHost(ctx context.Context, vhost, peerName string) (bool, error) {
	if slices.Contains(storage.SharedVHosts, vhost) {
		return false, nil
	}
	existing, _, err := s.store.ListVHosts(ctx, storage.CommonFilter{Limit: maxListed})
	if err != nil {
		return false, fmt.Errorf("listing vhosts: %w", err)
	}
	if slices.ContainsFunc(existing, func(v storage.VHost) bool { return v.Name == vhost }) {
		return false, nil
	}
	v := storage.VHost{ID: uuid.NewString(), Name: vhost, Description: "Created when " + peerName + " linked to it to promote workflows", CreatedAt: s.now().UTC()}
	if err := s.store.CreateVHost(ctx, v); err != nil {
		return false, fmt.Errorf("creating vhost %s: %w", vhost, err)
	}
	return true, nil
}

// Peers lists the instances allowed to send promotions here.
func (s *Service) Peers(ctx context.Context) ([]Peer, error) {
	return loadPeers(ctx, s.store)
}

// RevokePeer stops an instance sending promotions here. Promotions it
// already sent stay in the history.
func (s *Service) RevokePeer(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	peers, err := loadPeers(ctx, s.store)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(peers, func(p Peer) bool { return p.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	return putJSON(ctx, s.store, keyPeers, slices.Delete(peers, i, i+1))
}

// AuthenticatePeer returns the peer a bearer token belongs to.
func (s *Service) AuthenticatePeer(ctx context.Context, token string) (Peer, error) {
	if !strings.HasPrefix(token, tokenPrefix) {
		return Peer{}, ErrForbidden
	}
	want := hashSecret(token)
	peers, err := loadPeers(ctx, s.store)
	if err != nil {
		return Peer{}, err
	}
	i := slices.IndexFunc(peers, func(p Peer) bool {
		return subtle.ConstantTimeCompare([]byte(p.TokenHash), []byte(want)) == 1
	})
	if i < 0 {
		return Peer{}, ErrForbidden
	}
	peer := peers[i]
	now := s.now().UTC()
	if now.Sub(peer.LastUsedAt) >= lastUsedEvery {
		s.mu.Lock()
		if fresh, err := loadPeers(ctx, s.store); err == nil {
			if j := slices.IndexFunc(fresh, func(p Peer) bool { return p.ID == peer.ID }); j >= 0 {
				fresh[j].LastUsedAt = now
				_ = putJSON(ctx, s.store, keyPeers, fresh)
			}
		}
		s.mu.Unlock()
		peer.LastUsedAt = now
	}
	return peer, nil
}

// PeerInfo is what peer learns about this instance: the rules that apply to
// what it sends, and the vhost it sends into.
func (s *Service) PeerInfo(ctx context.Context, peer Peer) (PeerInfo, error) {
	in, err := loadInstance(ctx, s.store)
	if err != nil {
		return PeerInfo{}, err
	}
	rules, err := loadRules(ctx, s.store)
	if err != nil {
		return PeerInfo{}, err
	}
	return PeerInfo{Instance: in, Rules: rules.For(peer.VHost), Version: s.version, VHost: peer.VHost,
		Features: []string{FeatureReleases}}, nil
}

// Links lists the instances this one promotes to.
func (s *Service) Links(ctx context.Context) ([]Link, error) {
	return loadLinks(ctx, s.store)
}

// linkFor is the link vhost's workflows go through: its own, else one made
// before links were per vhost.
func (s *Service) linkFor(links []Link, vhost string) (Link, bool) {
	if i := slices.IndexFunc(links, func(l Link) bool { return l.VHost == vhost }); i >= 0 {
		return links[i], true
	}
	if i := slices.IndexFunc(links, func(l Link) bool { return l.VHost == "" }); i >= 0 {
		return links[i], true
	}
	return Link{}, false
}

func (s *Service) link(ctx context.Context, id string) (Link, string, error) {
	links, err := loadLinks(ctx, s.store)
	if err != nil {
		return Link{}, "", err
	}
	i := slices.IndexFunc(links, func(l Link) bool { return l.ID == id })
	if i < 0 {
		return Link{}, "", ErrNotFound
	}
	token, err := loadLinkToken(ctx, s.store, id)
	if err != nil {
		return Link{}, "", err
	}
	if token == "" {
		return links[i], "", fmt.Errorf("%w: the link to %s has lost its token (was the master key changed?); unlink and link it again", ErrInvalid, links[i].Name)
	}
	return links[i], token, nil
}

// LinkRequest links one vhost here to the instance at URL, with a pairing
// code its admin issued. SelfURL is the address it shows for this one.
type LinkRequest struct {
	URL     string `json:"url"`
	Code    string `json:"code"`
	SelfURL string `json:"self_url"`
	VHost   string `json:"vhost"`
}

// LinkWith redeems a pairing code and keeps the link for req.VHost.
func (s *Service) LinkWith(ctx context.Context, req LinkRequest, username string) (Link, error) {
	u, err := ValidateURL(req.URL)
	if err != nil {
		return Link{}, err
	}
	if err := ValidateVHostName(req.VHost); err != nil {
		return Link{}, err
	}
	if strings.TrimSpace(req.Code) == "" {
		return Link{}, fmt.Errorf("%w: a pairing code is needed", ErrInvalid)
	}
	self, err := loadInstance(ctx, s.store)
	if err != nil {
		return Link{}, err
	}
	res, err := s.remote.Pair(ctx, u, PairRequest{Code: req.Code, Name: displayName(self), URL: req.SelfURL, VHost: req.VHost})
	if err != nil {
		return Link{}, err
	}
	now := s.now().UTC()
	l := Link{
		ID: uuid.NewString(), Name: displayName(res.Instance), URL: u, Role: res.Instance.Role,
		Version: res.Version, CreatedBy: username, CreatedAt: now, CheckedAt: now, Reachable: true,
		VHost: req.VHost, TargetVHost: res.VHost, VHostCreated: res.VHostCreated,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	links, err := loadLinks(ctx, s.store)
	if err != nil {
		return Link{}, err
	}
	// Linking a vhost again replaces its old link; the connections already
	// matched are kept when it goes to the same address.
	if i := slices.IndexFunc(links, func(old Link) bool { return old.VHost == req.VHost }); i >= 0 {
		if links[i].URL == u {
			l.Mapping = links[i].Mapping
		}
		if err := saveLinkToken(ctx, s.store, links[i].ID, ""); err != nil {
			return Link{}, err
		}
		links = slices.Delete(links, i, i+1)
	}
	if err := saveLinkToken(ctx, s.store, l.ID, res.Token); err != nil {
		return Link{}, err
	}
	links = append(links, l)
	return l, putJSON(ctx, s.store, keyLinks, links)
}

// Unlink forgets a link. The other instance is asked to revoke its token;
// when it cannot be reached the link is forgotten here anyway, and the note
// says the token is still valid there.
func (s *Service) Unlink(ctx context.Context, id string) (note string, err error) {
	l, token, err := s.link(ctx, id)
	if err != nil && l.ID == "" {
		return "", err
	}
	if token != "" {
		if err := s.remote.Unpair(ctx, l.URL, token); err != nil {
			note = fmt.Sprintf("%s could not be told (%v); revoke the link there under Settings › Environments.", l.Name, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	links, err := loadLinks(ctx, s.store)
	if err != nil {
		return note, err
	}
	links = slices.DeleteFunc(links, func(x Link) bool { return x.ID == id })
	if err := saveLinkToken(ctx, s.store, id, ""); err != nil {
		return note, err
	}
	return note, putJSON(ctx, s.store, keyLinks, links)
}

// TestLink calls the linked instance and records whether it answered.
func (s *Service) TestLink(ctx context.Context, id string) (Link, PeerInfo, error) {
	l, token, err := s.link(ctx, id)
	if err != nil {
		return Link{}, PeerInfo{}, err
	}
	info, callErr := s.remote.Info(ctx, l.URL, token)

	s.mu.Lock()
	defer s.mu.Unlock()
	links, err := loadLinks(ctx, s.store)
	if err != nil {
		return Link{}, PeerInfo{}, err
	}
	i := slices.IndexFunc(links, func(x Link) bool { return x.ID == id })
	if i < 0 {
		return Link{}, PeerInfo{}, ErrNotFound
	}
	links[i].CheckedAt = s.now().UTC()
	links[i].Reachable = callErr == nil
	links[i].CheckNote = ""
	if callErr != nil {
		links[i].CheckNote = callErr.Error()
	} else {
		links[i].Name = displayName(info.Instance)
		links[i].Role = info.Instance.Role
		links[i].Version = info.Version
	}
	if err := putJSON(ctx, s.store, keyLinks, links); err != nil {
		return Link{}, PeerInfo{}, err
	}
	return links[i], info, callErr
}

// Match statuses.
const (
	MatchMatched   = "matched"
	MatchSuggested = "suggested"
	MatchMissing   = "missing"
)

// MatchRow is one of this instance's connections and what replaces it on
// the linked one.
type MatchRow struct {
	Kind       string       `json:"kind"` // source or sink
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Type       string       `json:"type"`
	VHost      string       `json:"vhost"`
	TargetID   string       `json:"target_id,omitempty"`
	TargetName string       `json:"target_name,omitempty"`
	Status     string       `json:"status"`
	Manual     bool         `json:"manual"`
	Candidates []Connection `json:"candidates"`
}

// envSuffixes are name endings that only say which environment a
// connection is in, so orders-db-stg suggests orders-db.
var envSuffixes = []string{"-staging", "_staging", "-stg", "_stg", "-stage", "-dev", "_dev", "-test", "_test", "-qa", "_qa", "-prod", "_prod", "-production", "_production"}

func baseName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, suf := range envSuffixes {
		if strings.HasSuffix(n, suf) {
			return strings.TrimSuffix(n, suf)
		}
	}
	return n
}

// resolve finds what replaces c among targets: the person's own match
// first, then the one connection of the same type and name (the same vhost
// preferred), else a suggestion by name without its environment suffix.
func resolve(c Connection, mapping map[string]string, targets []Connection) (Connection, string, bool) {
	if id, ok := mapping[c.ID]; ok {
		if i := slices.IndexFunc(targets, func(t Connection) bool { return t.ID == id }); i >= 0 {
			return targets[i], MatchMatched, true
		}
		return Connection{}, MatchMissing, true
	}
	pick := func(match func(Connection) bool) (Connection, bool) {
		var all, sameVHost []Connection
		for _, t := range targets {
			if t.Type == c.Type && match(t) {
				all = append(all, t)
				if t.VHost == c.VHost {
					sameVHost = append(sameVHost, t)
				}
			}
		}
		if len(sameVHost) == 1 {
			return sameVHost[0], true
		}
		if len(sameVHost) == 0 && len(all) == 1 {
			return all[0], true
		}
		return Connection{}, false
	}
	if t, ok := pick(func(t Connection) bool { return strings.EqualFold(t.Name, c.Name) }); ok {
		return t, MatchMatched, false
	}
	if t, ok := pick(func(t Connection) bool { return baseName(t.Name) == baseName(c.Name) }); ok {
		return t, MatchSuggested, false
	}
	return Connection{}, MatchMissing, false
}

func sourceConnections(srcs []storage.Source) []Connection {
	out := make([]Connection, 0, len(srcs))
	for _, x := range srcs {
		out = append(out, Connection{ID: x.ID, Name: x.Name, Type: x.Type, VHost: x.VHost})
	}
	return out
}

func sinkConnections(snks []storage.Sink) []Connection {
	out := make([]Connection, 0, len(snks))
	for _, x := range snks {
		out = append(out, Connection{ID: x.ID, Name: x.Name, Type: x.Type, VHost: x.VHost})
	}
	return out
}

// maxListed bounds how many sources or sinks a listing reads.
const maxListed = 10000

// localConnections lists this instance's sources and sinks, in vhost when
// it is set.
func (s *Service) localConnections(ctx context.Context, vhost string) (Connections, error) {
	srcs, _, err := s.store.ListSources(ctx, storage.CommonFilter{VHost: vhost, Limit: maxListed})
	if err != nil {
		return Connections{}, fmt.Errorf("listing sources: %w", err)
	}
	snks, _, err := s.store.ListSinks(ctx, storage.CommonFilter{VHost: vhost, Limit: maxListed})
	if err != nil {
		return Connections{}, fmt.Errorf("listing sinks: %w", err)
	}
	return Connections{Sources: sourceConnections(srcs), Sinks: sinkConnections(snks)}, nil
}

// PeerConnections lists the connections peer may use: those in its vhost.
func (s *Service) PeerConnections(ctx context.Context, peer Peer) (Connections, error) {
	return s.localConnections(ctx, peer.VHost)
}

// Matching pairs this instance's connections in the link's vhost with the
// linked instance's. vhost picks one on a link that serves every vhost.
func (s *Service) Matching(ctx context.Context, linkID, vhost string) ([]MatchRow, error) {
	l, token, err := s.link(ctx, linkID)
	if err != nil {
		return nil, err
	}
	if l.VHost != "" {
		if vhost != "" && vhost != l.VHost {
			return nil, fmt.Errorf("%w: this link is for vhost %s, not %s", ErrInvalid, l.VHost, vhost)
		}
		vhost = l.VHost
	}
	targets, err := s.remote.Connections(ctx, l.URL, token)
	if err != nil {
		return nil, err
	}
	local, err := s.localConnections(ctx, vhost)
	if err != nil {
		return nil, err
	}
	rows := make([]MatchRow, 0, len(local.Sources)+len(local.Sinks))
	add := func(kind string, conns, candidates []Connection) {
		for _, c := range conns {
			t, status, manual := resolve(c, l.Mapping, candidates)
			row := MatchRow{Kind: kind, ID: c.ID, Name: c.Name, Type: c.Type, VHost: c.VHost,
				Status: status, Manual: manual, Candidates: []Connection{}}
			if status != MatchMissing {
				row.TargetID, row.TargetName = t.ID, t.Name
			}
			for _, cand := range candidates {
				if cand.Type == c.Type {
					row.Candidates = append(row.Candidates, cand)
				}
			}
			rows = append(rows, row)
		}
	}
	add("source", local.Sources, targets.Sources)
	add("sink", local.Sinks, targets.Sinks)
	// What needs a person first, then by name.
	rank := map[string]int{MatchMissing: 0, MatchSuggested: 1, MatchMatched: 2}
	slices.SortStableFunc(rows, func(a, b MatchRow) int {
		if rank[a.Status] != rank[b.Status] {
			return rank[a.Status] - rank[b.Status]
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return rows, nil
}

// maxMapping bounds a link's hand-made matches.
const maxMapping = 5000

// SetMapping records hand-made matches: each key is a connection here, each
// value the connection that replaces it there. An empty value forgets the
// match, so the automatic one applies again.
func (s *Service) SetMapping(ctx context.Context, linkID string, changes map[string]string) (Link, error) {
	for k, v := range changes {
		if k == "" || len(k) > 128 || len(v) > 128 {
			return Link{}, fmt.Errorf("%w: a connection ID is empty or too long", ErrInvalid)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	links, err := loadLinks(ctx, s.store)
	if err != nil {
		return Link{}, err
	}
	i := slices.IndexFunc(links, func(l Link) bool { return l.ID == linkID })
	if i < 0 {
		return Link{}, ErrNotFound
	}
	if links[i].Mapping == nil {
		links[i].Mapping = map[string]string{}
	}
	for k, v := range changes {
		if v == "" {
			delete(links[i].Mapping, k)
		} else {
			links[i].Mapping[k] = v
		}
	}
	if len(links[i].Mapping) > maxMapping {
		return Link{}, fmt.Errorf("%w: at most %d connections can be matched by hand", ErrInvalid, maxMapping)
	}
	return links[i], putJSON(ctx, s.store, keyLinks, links)
}
