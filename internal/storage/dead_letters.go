// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DeadLetterStatus is where a dead letter is in its life: still waiting in
// the queue, or dealt with by a replay or a discard.
type DeadLetterStatus string

const (
	DeadLetterQueued    DeadLetterStatus = "queued"
	DeadLetterReplayed  DeadLetterStatus = "replayed"
	DeadLetterDiscarded DeadLetterStatus = "discarded"
)

// DeadLetterPayloadCap bounds the payload kept with a dead letter. A larger
// payload is kept up to the cap and marked truncated, and a truncated payload
// cannot be replayed as it is: the message would arrive cut short.
const DeadLetterPayloadCap = 256 << 10

// Bounds on the free text a dead letter carries.
const (
	maxDeadLetterError   = 8 << 10
	maxDeadLetterHistory = 100
)

// ErrDeadLetterSettled is returned when a dead letter has already been
// replayed or discarded, so a second action cannot act on it again.
var ErrDeadLetterSettled = errors.New("this dead letter has already been replayed or discarded")

// ErrDeadLettersUnsupported is returned when the configured backend cannot
// keep dead letters.
var ErrDeadLettersUnsupported = errors.New("this storage backend cannot keep dead letters")

// DeadLetter is one message a workflow could not deliver, as Hermod keeps it
// for the Dead Letters page: what failed, where, why, and
// the message itself, so it can be read, replayed or discarded from the UI.
type DeadLetter struct {
	ID         string `json:"id"`
	VHost      string `json:"vhost"`
	WorkflowID string `json:"workflow_id"`
	MessageID  string `json:"message_id"`
	// NodeID is the node that failed the message; SinkID is the sink that
	// refused it. One of them is set.
	NodeID   string    `json:"node_id,omitempty"`
	SinkID   string    `json:"sink_id,omitempty"`
	Error    string    `json:"error"`
	FailedAt time.Time `json:"failed_at"`

	Payload []byte `json:"payload,omitempty"`
	// PayloadSize is the size of the whole payload, which is more than
	// len(Payload) when it was truncated.
	PayloadSize int               `json:"payload_size"`
	Truncated   bool              `json:"truncated,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Origin      DeadLetterOrigin  `json:"origin"`

	Status    DeadLetterStatus  `json:"status"`
	SettledAt *time.Time        `json:"settled_at,omitempty"`
	SettledBy string            `json:"settled_by,omitempty"`
	History   []DeadLetterEvent `json:"history,omitempty"`
}

// DeadLetterOrigin is what a replay needs besides the payload: the change the
// message described and the row as it was before it.
type DeadLetterOrigin struct {
	Operation string `json:"operation,omitempty"`
	Table     string `json:"table,omitempty"`
	Schema    string `json:"schema,omitempty"`
	Before    []byte `json:"before,omitempty"`
}

// DeadLetterEvent is one thing that happened to a dead letter.
type DeadLetterEvent struct {
	At     time.Time `json:"at"`
	Action string    `json:"action"`
	By     string    `json:"by,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// DeadLetterFilter narrows a list of dead letters. VHosts nil means every
// vhost; otherwise the shared vhosts are always included, as elsewhere.
type DeadLetterFilter struct {
	VHosts     []string
	WorkflowID string
	// Where matches the failing node or the refusing sink.
	Where  string
	Status DeadLetterStatus
	// Search matches the error, the message id, the node and the sink.
	Search string
	Since  time.Time
	Until  time.Time
	Limit  int
	Offset int
}

// DeadLetterStats is the summary the dead letter page leads with.
type DeadLetterStats struct {
	Queued           int    `json:"queued"`
	Last24h          int    `json:"last_24h"`
	TopError         string `json:"top_error,omitempty"`
	TopErrorCount    int    `json:"top_error_count"`
	ReplayedThisWeek int    `json:"replayed_this_week"`
}

// DeadLetterStore keeps dead letters. It is optional: a backend that has it
// gives the dead letter page something to show.
type DeadLetterStore interface {
	PutDeadLetter(ctx context.Context, d DeadLetter) error
	GetDeadLetter(ctx context.Context, id string) (DeadLetter, error)
	// ListDeadLetters returns a page, newest first, and the total that
	// matches the filter. The page leaves the payload, origin, metadata and
	// history out.
	ListDeadLetters(ctx context.Context, f DeadLetterFilter) ([]DeadLetter, int, error)
	// SettleDeadLetter moves a queued dead letter to status and records ev.
	// It returns ErrDeadLetterSettled when the dead letter is no longer
	// queued, and ErrNotFound when there is none.
	SettleDeadLetter(ctx context.Context, id string, status DeadLetterStatus, ev DeadLetterEvent) error
	// AddDeadLetterEvent records ev without changing the status, such as a
	// replay that failed and left the dead letter in the queue.
	AddDeadLetterEvent(ctx context.Context, id string, ev DeadLetterEvent) error
	DeadLetterStats(ctx context.Context, vhosts []string, now time.Time) (DeadLetterStats, error)
	// PurgeDeadLetters deletes dead letters that failed before cutoff and
	// were replayed or discarded. One still waiting is the only copy of its
	// message, so it stays until someone deals with it.
	PurgeDeadLetters(ctx context.Context, cutoff time.Time) (int64, error)
}

// NewDeadLetterEvent is ev with its time set to now, in UTC.
func NewDeadLetterEvent(action, by, detail string) DeadLetterEvent {
	return DeadLetterEvent{At: time.Now().UTC(), Action: action, By: by, Detail: detail}
}

// CapDeadLetter applies the bounds every backend keeps to: the payload is cut
// at DeadLetterPayloadCap and marked truncated, the error and history are
// bounded, and an empty status is queued.
func CapDeadLetter(d DeadLetter) DeadLetter {
	if d.PayloadSize < len(d.Payload) {
		d.PayloadSize = len(d.Payload)
	}
	if len(d.Payload) > DeadLetterPayloadCap {
		d.Payload = d.Payload[:DeadLetterPayloadCap:DeadLetterPayloadCap]
		d.Truncated = true
	}
	if len(d.Origin.Before) > DeadLetterPayloadCap {
		d.Origin.Before = nil
		d.Truncated = true
	}
	if len(d.Error) > maxDeadLetterError {
		d.Error = d.Error[:maxDeadLetterError]
	}
	if d.Status == "" {
		d.Status = DeadLetterQueued
	}
	d.History = capHistory(d.History)
	d.FailedAt = d.FailedAt.UTC()
	return d
}

func capHistory(h []DeadLetterEvent) []DeadLetterEvent {
	if len(h) > maxDeadLetterHistory {
		return h[len(h)-maxDeadLetterHistory:]
	}
	return h
}

// AppendDeadLetterEvent adds ev to h, dropping the oldest events past the bound.
func AppendDeadLetterEvent(h []DeadLetterEvent, ev DeadLetterEvent) []DeadLetterEvent {
	return capHistory(append(append([]DeadLetterEvent{}, h...), ev))
}

// ValidateDeadLetter rejects a dead letter no backend should keep.
func ValidateDeadLetter(d DeadLetter) error {
	switch {
	case d.ID == "":
		return errors.New("a dead letter needs an id")
	case d.WorkflowID == "":
		return errors.New("a dead letter needs a workflow")
	}
	switch d.Status {
	case DeadLetterQueued, DeadLetterReplayed, DeadLetterDiscarded:
	default:
		return fmt.Errorf("unknown dead letter status %q", d.Status)
	}
	return nil
}

// ValidSettledStatus reports whether a dead letter can be settled to s.
func ValidSettledStatus(s DeadLetterStatus) bool {
	return s == DeadLetterReplayed || s == DeadLetterDiscarded
}

// WithSharedVHosts is vhosts with the shared vhosts added, or nil for nil.
func WithSharedVHosts(vhosts []string) []string {
	if vhosts == nil {
		return nil
	}
	return append(append([]string{}, SharedVHosts...), vhosts...)
}
