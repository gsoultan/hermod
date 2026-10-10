// Package selfheal keeps the self-correction gate's fixes as proposals that
// change a workflow only when a person approves them.
//
// A proposal is a small patch to a stored workflow: each change names a
// field, the value it expects to find and the value it would set. Approval
// re-reads the workflow, refuses the patch if any field has moved on since
// it was proposed, and applies it through the workflow update path, so the
// change is validated and lands in the workflow's version history, from
// which it can be rolled back.
package selfheal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// Proposal statuses.
const (
	StatusPending  = "pending"
	StatusApplied  = "applied"
	StatusRejected = "rejected"
	// StatusStale marks a proposal whose workflow changed before it was
	// approved; it can no longer be applied.
	StatusStale = "stale"
)

var (
	// ErrNotFound is returned for a proposal that does not exist.
	ErrNotFound = errors.New("proposal not found")
	// ErrNotPending is returned when deciding a proposal already decided.
	ErrNotPending = errors.New("proposal is no longer pending")
	// ErrStale is returned when the workflow no longer has the values the
	// proposal was made against.
	ErrStale = errors.New("the workflow changed since this fix was proposed; it was not applied")
	// ErrNoApplier is returned by Approve on a service built without one.
	ErrNoApplier = errors.New("proposals cannot be applied here")
)

// Change is one field of a patch, as a JSON Patch "replace" operation with
// the value it replaces.
type Change struct {
	Op     string          `json:"op"`
	Path   string          `json:"path"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

// Proposal is a stored fix waiting for, or past, a decision.
type Proposal struct {
	ID              string     `json:"id"`
	WorkflowID      string     `json:"workflow_id"`
	Kind            string     `json:"kind"`
	NodeID          string     `json:"node_id,omitempty"`
	Title           string     `json:"title"`
	Reason          string     `json:"reason"`
	Patch           []Change   `json:"patch"`
	Status          string     `json:"status"`
	Occurrences     int        `json:"occurrences"`
	CreatedAt       time.Time  `json:"created_at"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	DecidedAt       *time.Time `json:"decided_at,omitempty"`
	DecidedBy       string     `json:"decided_by,omitempty"`
	PreviousVersion int        `json:"previous_version,omitempty"`
	AppliedVersion  int        `json:"applied_version,omitempty"`
}

// field reads and writes one patchable workflow field. Only the fields here
// can be patched: a proposal is never a free-form edit.
type field struct {
	get func(storage.Workflow) any
	set func(*storage.Workflow, json.RawMessage) error
}

var fields = map[string]field{
	"/max_retries": {
		get: func(wf storage.Workflow) any { return wf.MaxRetries },
		set: func(wf *storage.Workflow, raw json.RawMessage) error { return json.Unmarshal(raw, &wf.MaxRetries) },
	},
	"/retry_interval": {
		get: func(wf storage.Workflow) any { return wf.RetryInterval },
		set: func(wf *storage.Workflow, raw json.RawMessage) error { return json.Unmarshal(raw, &wf.RetryInterval) },
	},
}

func replace(path string, before, after any) Change {
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	return Change{Op: "replace", Path: path, Before: b, After: a}
}

// patched returns wf with the patch applied, or ErrStale when a field no
// longer holds the value the patch expects.
func patched(wf storage.Workflow, patch []Change) (storage.Workflow, error) {
	for _, c := range patch {
		f, ok := fields[c.Path]
		if !ok || c.Op != "replace" {
			return wf, fmt.Errorf("unsupported change %s %s", c.Op, c.Path)
		}
		current, _ := json.Marshal(f.get(wf))
		if !bytes.Equal(current, c.Before) {
			return wf, ErrStale
		}
		if err := f.set(&wf, c.After); err != nil {
			return wf, fmt.Errorf("change %s: %w", c.Path, err)
		}
	}
	return wf, nil
}
