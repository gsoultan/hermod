// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	mimirwebhook "github.com/gsoultan/mimir-server-sdk/webhook"
)

// A webhook source with "preset": "mimir" receives the events a mimir-server
// sends to a webhook made on its console: model.created, training.finished,
// model.accuracy_dropped and the rest. Such a delivery is signed in a
// Mimir-Signature header with the webhook's whsec_ secret, which the source
// holds as its "secret"; the API key and the X-Hub-Signature-256 check of a
// plain webhook do not apply. The server retries a delivery for about a day
// until it gets a 2xx, so the same event can arrive more than once: a repeat
// (same Event.ID on the same path) is acknowledged and not dispatched again.
// "events", a comma-separated list of event types, keeps only those; an
// event of another type is acknowledged and dropped.
const (
	configPreset = "preset"
	configEvents = "events"
	presetMimir  = "mimir"

	// metaMimirEvent and metaMimirEventID are set on the message so a
	// workflow can route on the event's type and keep its id.
	metaMimirEvent   = "mimir_event"
	metaMimirEventID = "mimir_event_id"

	// seenFor is how long an event id is remembered: as long as mimir
	// retries. seenLimit bounds the memory a flood of ids can take.
	seenFor   = 25 * time.Hour
	seenLimit = 10_000
)

// msgMimirNeedsSecret is what a sender is told when the source names the
// mimir preset but no secret to check deliveries with.
const msgMimirNeedsSecret = "this webhook takes mimir-server events but has no webhook secret to check them with"

// isMimir reports whether a source takes mimir-server events.
func isMimir(config map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(config[configPreset]), presetMimir)
}

// mimirEvent checks and reads one mimir delivery. It returns the event, or an
// HTTP status and a client-facing message when the delivery is refused.
func mimirEvent(r *http.Request, config map[string]string, body []byte) (*mimirwebhook.Event, int, string) {
	secret := config["secret"]
	if secret == "" {
		return nil, http.StatusServiceUnavailable, msgMimirNeedsSecret
	}
	ev, err := mimirwebhook.Parse(secret, r.Header.Get(mimirwebhook.SignatureHeader), body, 0)
	switch {
	case errors.Is(err, mimirwebhook.ErrNoSignature):
		return nil, http.StatusUnauthorized, "Missing " + mimirwebhook.SignatureHeader
	case errors.Is(err, mimirwebhook.ErrBadSignature), errors.Is(err, mimirwebhook.ErrExpired):
		return nil, http.StatusUnauthorized, "Invalid " + mimirwebhook.SignatureHeader + ": " + err.Error()
	case err != nil:
		return nil, http.StatusBadRequest, err.Error()
	}
	if ev.ID == "" || ev.Type == "" {
		return nil, http.StatusBadRequest, "a mimir event has an id and a type"
	}
	return ev, http.StatusOK, ""
}

// wantsEvent reports whether the source keeps events of this type: every type
// when it lists none.
func wantsEvent(config map[string]string, eventType string) bool {
	list := strings.TrimSpace(config[configEvents])
	if list == "" {
		return true
	}
	for part := range strings.SplitSeq(list, ",") {
		if strings.EqualFold(strings.TrimSpace(part), eventType) {
			return true
		}
	}
	return false
}

// recentEvents remembers which event ids each path has already taken, so a
// retried delivery is acknowledged without being dispatched twice. It is
// per process: a second Hermod node behind the same address keeps its own.
type recentEvents struct {
	mu    sync.Mutex
	seen  map[string]time.Time
	order []string
	now   func() time.Time
}

func newRecentEvents() *recentEvents {
	return &recentEvents{seen: make(map[string]time.Time), now: time.Now}
}

// Seen marks id as taken for path and reports whether it already was.
func (c *recentEvents) Seen(path, id string) bool {
	key := path + "\x00" + id
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.expire(now)
	if _, ok := c.seen[key]; ok {
		return true
	}
	c.seen[key] = now
	c.order = append(c.order, key)
	for len(c.order) > seenLimit {
		delete(c.seen, c.order[0])
		c.order = c.order[1:]
	}
	return false
}

// expire forgets the ids older than seenFor. order is oldest first.
func (c *recentEvents) expire(now time.Time) {
	for len(c.order) > 0 {
		at, ok := c.seen[c.order[0]]
		if ok && now.Sub(at) < seenFor {
			return
		}
		delete(c.seen, c.order[0])
		c.order = c.order[1:]
	}
}
