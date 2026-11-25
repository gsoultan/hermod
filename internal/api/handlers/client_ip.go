// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package handlers

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the address the request came from, for keying rate limits
// and login lockouts.
//
// X-Forwarded-For is believed only when the direct peer is a loopback or
// private address, which is where a reverse proxy in front of Hermod sits. A
// client that reaches Hermod directly could otherwise send any value it likes
// and get a fresh lockout counter on every attempt.
//
// Behind a proxy the header is read from the right: each proxy appends the
// address it saw, so the rightmost entry that is not itself a private hop is
// the real client, and anything to its left was supplied by that client.
func ClientIP(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || peer == "" {
		peer = r.RemoteAddr
	}
	if !isInternalIP(peer) {
		return peer
	}

	xff := r.Header.Values("X-Forwarded-For")
	if len(xff) == 0 {
		return peer
	}
	var hops []string
	for _, v := range xff {
		for _, part := range strings.Split(v, ",") {
			if ip := net.ParseIP(strings.TrimSpace(part)); ip != nil {
				hops = append(hops, ip.String())
			}
		}
	}
	if len(hops) == 0 {
		return peer
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if !isInternalIP(hops[i]) {
			return hops[i]
		}
	}
	return hops[0]
}

func isInternalIP(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// ClearLoginLockout forgets the failed-login state for a username on every
// client IP, and reports how many entries it removed. It is what an
// administrator's password reset uses to let the user straight back in.
func (h *Handler) ClearLoginLockout(username string) int {
	prefix := LoginAttemptUser(username) + "|"
	n := 0
	h.LoginAttempts.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, prefix) {
			h.LoginAttempts.Delete(key)
			n++
		}
		return true
	})
	return n
}

// LoginAttemptUser normalises a username the way login-attempt keys store it.
func LoginAttemptUser(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
