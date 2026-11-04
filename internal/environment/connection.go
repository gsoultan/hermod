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
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"
)

// One connection per server. An instance pairs with another once, with a
// code, for one vhost. Every vhost it adds afterwards is asked for over the
// token it already holds: the other side issues a new peer with a token of
// its own, scoped to that one vhost, in the same group. So each token still
// reaches a single vhost, and removing one vhost or cutting the whole
// connection off are both a matter of deleting peers.

// maxVHostRequests bounds the vhost requests waiting for an administrator.
const maxVHostRequests = 100

// PairingOption changes what a pairing code allows.
type PairingOption func(*PairingCode)

// AllowAddingVHosts lets the instance that redeems the code add vhosts to
// its connection later without an administrator here allowing each one.
func AllowAddingVHosts() PairingOption {
	return func(c *PairingCode) { c.AddVHosts = true }
}

// groupOf is the ID every peer of p's connection shares.
func groupOf(p Peer) string {
	if p.Group != "" {
		return p.Group
	}
	return p.ID
}

func loadVHostRequests(ctx context.Context, s Settings) ([]VHostRequest, error) {
	var out []VHostRequest
	err := getJSON(ctx, s, keyVHostRequests, &out)
	return out, err
}

// AddPeerVHost answers a linked instance asking to add vhost here to its
// connection. With the connection allowed to add vhosts it gets a token for
// it at once; otherwise the request waits for an administrator, and the
// instance asks again until one allows or declines it.
func (s *Service) AddPeerVHost(ctx context.Context, peer Peer, req AddVHostRequest) (AddVHostResult, error) {
	if err := ValidateVHostName(req.VHost); err != nil {
		return AddVHostResult{}, err
	}
	if req.VHost == "all" {
		return AddVHostResult{}, fmt.Errorf("%w: \"all\" stands for every vhost; pick one", ErrInvalid)
	}
	group := groupOf(peer)
	out := AddVHostResult{VHost: req.VHost}

	s.mu.Lock()
	defer s.mu.Unlock()
	peers, err := loadPeers(ctx, s.store)
	if err != nil {
		return out, err
	}
	if err := groupReaches(peers, group, req.VHost); err != nil {
		return out, err
	}
	reqs, err := loadVHostRequests(ctx, s.store)
	if err != nil {
		return out, err
	}
	i := slices.IndexFunc(reqs, func(r VHostRequest) bool { return r.Group == group && r.VHost == req.VHost })
	allowedBy := ""
	switch {
	case i >= 0 && reqs[i].Declined:
		out.Declined = true
		return out, putJSON(ctx, s.store, keyVHostRequests, slices.Delete(reqs, i, i+1))
	case i >= 0 && reqs[i].Allowed:
		allowedBy = reqs[i].AllowedBy
		reqs = slices.Delete(reqs, i, i+1)
	case i >= 0:
		out.Pending = true
		return out, nil
	case !peer.AddVHosts:
		if len(reqs) >= maxVHostRequests {
			return out, fmt.Errorf("%w: too many vhost requests are waiting here; ask an administrator to clear them", ErrConflict)
		}
		reqs = append(reqs, VHostRequest{
			ID: uuid.NewString(), Group: group, PeerName: peer.Name, VHost: req.VHost,
			FromVHost: cleanName(req.FromVHost, ""), RequestedAt: s.now().UTC(),
		})
		out.Pending = true
		return out, putJSON(ctx, s.store, keyVHostRequests, reqs)
	}

	return s.issueVHostPeer(ctx, peers, reqs, peer, req, allowedBy)
}

// issueVHostPeer gives the connection a peer with its own token for the
// vhost, and drops the request it was allowed by, if any. s.mu is held.
func (s *Service) issueVHostPeer(ctx context.Context, peers []Peer, reqs []VHostRequest, peer Peer, req AddVHostRequest, allowedBy string) (AddVHostResult, error) {
	out := AddVHostResult{VHost: req.VHost}
	token, err := randomToken()
	if err != nil {
		return out, err
	}
	by := allowedBy
	if by == "" {
		by = peer.CreatedBy
	}
	added := Peer{
		ID: uuid.NewString(), Name: peer.Name, URL: peer.URL, TokenHash: hashSecret(token),
		CreatedBy: by, CreatedAt: s.now().UTC(), VHost: req.VHost, FromVHost: cleanName(req.FromVHost, ""),
		Group: groupOf(peer), AddVHosts: peer.AddVHosts, Decide: peer.Decide,
	}
	created, err := s.ensureVHost(ctx, req.VHost, peer.Name)
	if err != nil {
		return out, err
	}
	if err := putJSON(ctx, s.store, keyPeers, append(peers, added)); err != nil {
		return out, err
	}
	if allowedBy != "" {
		if err := putJSON(ctx, s.store, keyVHostRequests, reqs); err != nil {
			return out, err
		}
	}
	out.Token, out.VHostCreated = token, created
	return out, nil
}

// groupReaches refuses a vhost the connection already reaches: one of its
// peers sends into it, or into every vhost.
func groupReaches(peers []Peer, group, vhost string) error {
	for _, p := range peers {
		if groupOf(p) != group {
			continue
		}
		if p.VHost == "" {
			return fmt.Errorf("%w: %s already sends into every vhost here", ErrConflict, p.Name)
		}
		if p.VHost == vhost {
			return fmt.Errorf("%w: %s already sends into vhost %s here", ErrConflict, p.Name, vhost)
		}
	}
	return nil
}

// VHostRequests lists the vhosts linked instances asked to add and nobody
// has decided yet.
func (s *Service) VHostRequests(ctx context.Context) ([]VHostRequest, error) {
	reqs, err := loadVHostRequests(ctx, s.store)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(reqs, func(r VHostRequest) bool { return r.Allowed || r.Declined }), nil
}

// AllowVHostRequest lets the instance that asked add the vhost; it gets its
// token the next time it asks.
func (s *Service) AllowVHostRequest(ctx context.Context, id, username string) error {
	return s.decideVHostRequest(ctx, id, func(r *VHostRequest) { r.Allowed, r.AllowedBy = true, username })
}

// DeclineVHostRequest refuses it; the instance that asked is told once.
func (s *Service) DeclineVHostRequest(ctx context.Context, id string) error {
	return s.decideVHostRequest(ctx, id, func(r *VHostRequest) { r.Declined = true })
}

func (s *Service) decideVHostRequest(ctx context.Context, id string, decide func(*VHostRequest)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	reqs, err := loadVHostRequests(ctx, s.store)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(reqs, func(r VHostRequest) bool { return r.ID == id })
	if i < 0 || reqs[i].Allowed || reqs[i].Declined {
		return ErrNotFound
	}
	decide(&reqs[i])
	return putJSON(ctx, s.store, keyVHostRequests, reqs)
}

// RevokePeerGroup cuts a connection off: every peer in id's group, and any
// vhost it is still waiting for.
func (s *Service) RevokePeerGroup(ctx context.Context, id string) error {
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
	group := groupOf(peers[i])
	if err := putJSON(ctx, s.store, keyPeers, slices.DeleteFunc(peers, func(p Peer) bool { return groupOf(p) == group })); err != nil {
		return err
	}
	reqs, err := loadVHostRequests(ctx, s.store)
	if err != nil {
		return err
	}
	return putJSON(ctx, s.store, keyVHostRequests, slices.DeleteFunc(reqs, func(r VHostRequest) bool { return r.Group == group }))
}

// VHostRoute is a vhost here and the vhost there its workflows land in.
type VHostRoute struct {
	VHost       string `json:"vhost"`
	TargetVHost string `json:"target_vhost"`
}

// AddVHosts adds vhosts to the connection linkID belongs to, without a new
// pairing code: the other instance either issues a token for each at once
// or holds it for an administrator there, and the link waits (Pending).
func (s *Service) AddVHosts(ctx context.Context, linkID string, routes []VHostRoute, username string) ([]Link, error) {
	if len(routes) == 0 {
		return nil, fmt.Errorf("%w: pick at least one vhost", ErrInvalid)
	}
	via, token, err := s.link(ctx, linkID)
	if err != nil {
		return nil, err
	}
	links, err := loadLinks(ctx, s.store)
	if err != nil {
		return nil, err
	}
	if err := checkRoutes(routes, links); err != nil {
		return nil, err
	}

	var out []Link
	for _, r := range routes {
		res, err := s.remote.AddVHost(ctx, via.URL, token, AddVHostRequest{VHost: r.TargetVHost, FromVHost: r.VHost})
		if err != nil {
			return out, olderPeer(via, err)
		}
		if res.Declined {
			return out, fmt.Errorf("%w: an administrator in %s declined vhost %s", ErrForbidden, via.Name, r.TargetVHost)
		}
		now := s.now().UTC()
		l := Link{
			ID: uuid.NewString(), Name: via.Name, URL: via.URL, Role: via.Role, Version: via.Version,
			CreatedBy: username, CreatedAt: now, CheckedAt: now, Reachable: true,
			VHost: r.VHost, TargetVHost: r.TargetVHost, VHostCreated: res.VHostCreated,
			Pending: res.Token == "", Via: via.ID,
		}
		if l.Pending {
			l.CheckNote = waitingNote(via.Name)
		}
		if err := s.keepLink(ctx, l, res.Token); err != nil {
			return out, err
		}
		out = append(out, l)
	}
	return out, nil
}

// checkRoutes fills in each blank target with the vhost's own name and
// refuses a bad name or a vhost here that already promotes somewhere.
func checkRoutes(routes []VHostRoute, links []Link) error {
	seen := map[string]bool{}
	for i, r := range routes {
		if r.TargetVHost == "" {
			routes[i].TargetVHost = r.VHost
		}
		for _, name := range []string{r.VHost, routes[i].TargetVHost} {
			if err := ValidateVHostName(name); err != nil {
				return err
			}
		}
		if seen[r.VHost] || slices.ContainsFunc(links, func(l Link) bool { return l.VHost == r.VHost }) {
			return fmt.Errorf("%w: vhost %s here already promotes somewhere", ErrConflict, r.VHost)
		}
		seen[r.VHost] = true
	}
	return nil
}

func waitingNote(name string) string {
	return "Waiting for an administrator in " + name + " to allow it."
}

// olderPeer explains a 404 from an instance that predates adding vhosts.
func olderPeer(l Link, err error) error {
	var remote *RemoteError
	if errors.As(err, &remote) && (remote.Status == http.StatusNotFound || remote.Status == http.StatusMethodNotAllowed) {
		return fmt.Errorf("%w: %s runs an older Hermod that cannot add vhosts to a connection; connect this vhost with a pairing code instead", ErrInvalid, l.Name)
	}
	return err
}

// keepLink saves a new link and its token, unless a link for the same
// vhost was saved meanwhile.
func (s *Service) keepLink(ctx context.Context, l Link, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	links, err := loadLinks(ctx, s.store)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(links, func(x Link) bool { return x.VHost == l.VHost }) {
		return fmt.Errorf("%w: vhost %s here already promotes somewhere", ErrConflict, l.VHost)
	}
	if token != "" {
		if err := saveLinkToken(ctx, s.store, l.ID, token); err != nil {
			return err
		}
	}
	return putJSON(ctx, s.store, keyLinks, append(links, l))
}

// askAgain asks the other instance about a pending vhost and records the
// answer: its token once allowed, a note while it waits or after a decline.
func (s *Service) askAgain(ctx context.Context, l Link) (Link, error) {
	var (
		res     AddVHostResult
		callErr error
	)
	viaToken, err := loadLinkToken(ctx, s.store, l.Via)
	if err != nil {
		return l, err
	}
	links, err := loadLinks(ctx, s.store)
	if err != nil {
		return l, err
	}
	if viaToken == "" || !slices.ContainsFunc(links, func(x Link) bool { return x.ID == l.Via && !x.Pending }) {
		callErr = fmt.Errorf("%w: the vhost this one was added through is no longer linked; remove it and add it again", ErrConflict)
	} else {
		res, callErr = s.remote.AddVHost(ctx, l.URL, viaToken, AddVHostRequest{VHost: l.TargetVHost, FromVHost: l.VHost})
		switch {
		case callErr != nil:
		case res.Declined:
			callErr = fmt.Errorf("%w: an administrator in %s declined vhost %s; remove it here", ErrForbidden, l.Name, l.TargetVHost)
		case res.Token == "":
			callErr = fmt.Errorf("%w: %s", ErrConflict, waitingNote(l.Name))
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	links, err = loadLinks(ctx, s.store)
	if err != nil {
		return l, err
	}
	i := slices.IndexFunc(links, func(x Link) bool { return x.ID == l.ID })
	if i < 0 {
		return l, ErrNotFound
	}
	links[i].CheckedAt = s.now().UTC()
	if callErr != nil {
		links[i].CheckNote = callErr.Error()
	} else {
		if err := saveLinkToken(ctx, s.store, l.ID, res.Token); err != nil {
			return l, err
		}
		links[i].Pending, links[i].CheckNote, links[i].Reachable = false, "", true
		links[i].VHostCreated = res.VHostCreated
	}
	if err := putJSON(ctx, s.store, keyLinks, links); err != nil {
		return l, err
	}
	return links[i], callErr
}
