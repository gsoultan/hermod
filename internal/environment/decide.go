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
	"strings"
)

// maxDeciderName bounds the username a linked instance relays with a
// decision.
const maxDeciderName = 100

// AllowDeciding lets the linked instance's administrators approve, reject
// and roll back what it sends, so nobody has to sign in here for it.
func AllowDeciding() PairingOption {
	return func(c *PairingCode) { c.Decide = true }
}

// SetPeerDecide lets a linked instance's administrators decide what it
// sends here, or stops them.
func (s *Service) SetPeerDecide(ctx context.Context, id string, decide bool) (Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	peers, err := loadPeers(ctx, s.store)
	if err != nil {
		return Peer{}, err
	}
	i := slices.IndexFunc(peers, func(p Peer) bool { return p.ID == id })
	if i < 0 {
		return Peer{}, ErrNotFound
	}
	peers[i].Decide = decide
	return peers[i], putJSON(ctx, s.store, keyPeers, peers)
}

// PeerDecide carries out a decision an administrator made on the linked
// instance about a promotion it sent here. The rules here still apply:
// under admin_except_sender the person who sent it cannot approve it from
// there either. It is recorded as "name (via instance)".
func (s *Service) PeerDecide(ctx context.Context, peer Peer, d Decision) (Promotion, error) {
	if !peer.Decide {
		return Promotion{}, fmt.Errorf("%w: %s may not decide promotions here; an administrator here has to, or can allow it under Receives from", ErrForbidden, peer.Name)
	}
	by := strings.TrimSpace(d.By)
	if by == "" || len(by) > maxDeciderName {
		return Promotion{}, fmt.Errorf("%w: a decision needs the username of who made it", ErrInvalid)
	}
	p, err := s.promotion(ctx, d.PromotionID)
	if err != nil {
		return Promotion{}, err
	}
	// Only what this peer sent: another's promotion is not there for it.
	if p.PeerID != peer.ID {
		return Promotion{}, ErrNotFound
	}
	who := by + " (via " + peer.Name + ")"
	switch d.Action {
	case DecisionApprove:
		return s.approve(ctx, p.ID, by, who, nil)
	case DecisionReject:
		return s.Reject(ctx, p.ID, who, d.Reason)
	case DecisionRollback:
		reason := strings.TrimSpace(d.Reason)
		if reason == "" {
			reason = "Rolled back by " + who
		}
		return s.Rollback(ctx, p.ID, who, reason)
	default:
		return Promotion{}, fmt.Errorf("%w: unknown decision %q", ErrInvalid, d.Action)
	}
}

// DecideSent approves, rejects or rolls back, on the instance it went to, a
// promotion this instance sent, as username, and records the outcome in
// the sent log.
func (s *Service) DecideSent(ctx context.Context, id, action, username, reason string) (Promotion, error) {
	if !slices.Contains([]string{DecisionApprove, DecisionReject, DecisionRollback}, action) {
		return Promotion{}, fmt.Errorf("%w: unknown decision %q", ErrInvalid, action)
	}
	if len(reason) > 1000 {
		return Promotion{}, fmt.Errorf("%w: the reason must be at most 1000 characters", ErrInvalid)
	}
	var log []Promotion
	if err := getJSON(ctx, s.store, keySent, &log); err != nil {
		return Promotion{}, err
	}
	i := indexOf(log, id)
	if i < 0 {
		return Promotion{}, ErrNotFound
	}
	l, token, err := s.link(ctx, log[i].PeerID)
	if err != nil {
		return Promotion{}, fmt.Errorf("%w: the link it was sent through is gone; decide it in %s", ErrConflict, log[i].FromEnv)
	}
	if !l.Decide {
		// Production may have allowed it since the link was last checked.
		if fresh, _, terr := s.TestLink(ctx, l.ID); terr == nil {
			l = fresh
		}
		if !l.Decide {
			return Promotion{}, fmt.Errorf("%w: %s does not let administrators here decide; approve it in %s, or ask an administrator there to allow it", ErrForbidden, l.Name, l.Name)
		}
	}
	there, err := s.remote.Decide(ctx, l.URL, token, Decision{PromotionID: id, Action: action, By: username, Reason: reason})
	if err != nil {
		return Promotion{}, remoteDecisionErr(l, err)
	}
	return s.recordDecision(ctx, there)
}

// remoteDecisionErr turns the other instance's refusal into the error kind
// it stands for, so the person sees a 403 or 409 rather than a 502.
func remoteDecisionErr(l Link, err error) error {
	var remote *RemoteError
	if !errors.As(err, &remote) {
		return err
	}
	kinds := map[int]error{
		http.StatusBadRequest: ErrInvalid, http.StatusForbidden: ErrForbidden,
		http.StatusNotFound: ErrNotFound, http.StatusConflict: ErrConflict,
	}
	if kind, ok := kinds[remote.Status]; ok {
		return fmt.Errorf("%w: %s says: %s", kind, l.Name, remote.Message)
	}
	return err
}

// recordDecision updates the sent log with what the other instance reports.
func (s *Service) recordDecision(ctx context.Context, there Promotion) (Promotion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var log []Promotion
	if err := getJSON(ctx, s.store, keySent, &log); err != nil {
		return Promotion{}, err
	}
	i := indexOf(log, there.ID)
	if i < 0 {
		return Promotion{}, ErrNotFound
	}
	log[i].Status, log[i].Reason = there.Status, there.Reason
	log[i].DecidedBy, log[i].DecidedAt, log[i].DeployedAt = there.DecidedBy, there.DecidedAt, there.DeployedAt
	return log[i], putJSON(ctx, s.store, keySent, log)
}
