package storage

// How a trace is stored by the document backends (MongoDB, Pebble).
//
// They keep a whole trace in one document, which the SQL backends do not, and
// that changes the shape of the same two savings:
//
//  1. `hermod.TraceStep` carries Before *and* After, and both were persisted —
//     the payload chain twice, exactly what dropping before_data removed from
//     SQL. Before is the previous step's After and is reconstructed on read.
//
//  2. Payloads live in a map keyed by content, so a step records a key rather
//     than a copy. Measured on a realistic nine-step workflow, 66.6% of payload
//     bytes were byte-identical to another step of the same message.
//
// Dedup is *inherent* here, which it is not in SQL. A document holds one map:
// writing the same payload twice writes the same key twice, which is a no-op.
// There is no carrier row to elect, no cache to consult and no race to lose —
// see TraceDedupCache for the machinery SQL needs instead, and why it has to
// fail towards storing a duplicate.

import (
	"encoding/json"
	"time"

	"github.com/gsoultan/hermod"
)

// StoredTrace is one trace as written to a document store.
type StoredTrace struct {
	ID         string    `bson:"id" json:"id"`
	WorkflowID string    `bson:"workflow_id" json:"workflow_id"`
	MessageID  string    `bson:"message_id" json:"message_id"`
	CreatedAt  time.Time `bson:"created_at" json:"created_at"`

	Steps []StoredStep `bson:"steps" json:"steps"`

	// Payloads maps a content key to the encoded payload that produced it.
	// Compressed, and self-describing about how — see EncodeTracePayload.
	Payloads map[string][]byte `bson:"payloads,omitempty" json:"payloads,omitempty"`
}

// StoredStep is one node's step. It names its payload instead of carrying it.
type StoredStep struct {
	NodeID    string        `bson:"node_id" json:"node_id"`
	Timestamp time.Time     `bson:"timestamp" json:"timestamp"`
	Duration  time.Duration `bson:"duration,omitempty" json:"duration,omitempty"`
	AfterKey  string        `bson:"after_key,omitempty" json:"after_key,omitempty"`
	Error     string        `bson:"error,omitempty" json:"error,omitempty"`
	Lineage   string        `bson:"lineage,omitempty" json:"lineage,omitempty"`

	// Documents written before payload dedup carry the maps inline, under the
	// field names the driver derived from hermod.TraceStep's own fields. They
	// are read and never written, so an upgrade needs no backfill.
	LegacyBefore map[string]any `bson:"before,omitempty" json:"before,omitempty"`
	LegacyAfter  map[string]any `bson:"after,omitempty" json:"after,omitempty"`
}

// PackTraceStep turns a step into its stored form plus the payload to file
// under the returned key. An empty key means the step carried no payload.
func PackTraceStep(step hermod.TraceStep) (StoredStep, string, []byte) {
	stored := StoredStep{
		NodeID:    step.NodeID,
		Timestamp: step.Timestamp,
		Duration:  step.Duration,
		Error:     step.Error,
		Lineage:   step.Lineage,
	}
	if step.After == nil {
		return stored, "", nil
	}
	raw, err := json.Marshal(step.After)
	if err != nil {
		return stored, "", nil
	}
	key := TracePayloadKey(raw)
	stored.AfterKey = key
	return stored, key, EncodeTracePayload(raw)
}

// UnpackTrace rebuilds the trace a caller sees from the document that was
// stored.
func (t StoredTrace) UnpackTrace() MessageTrace {
	out := MessageTrace{
		ID:         t.ID,
		WorkflowID: t.WorkflowID,
		MessageID:  t.MessageID,
		CreatedAt:  t.CreatedAt,
		StepCount:  len(t.Steps),
	}

	// Decode each distinct payload once, however many steps name it. A trace
	// whose nine steps share three payloads costs three decompressions.
	decoded := make(map[string]map[string]any, len(t.Payloads))
	for key, blob := range t.Payloads {
		raw, err := DecodeTracePayload(blob)
		if err != nil || len(raw) == 0 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			decoded[key] = m
		}
	}

	for i, s := range t.Steps {
		step := hermod.TraceStep{
			NodeID:    s.NodeID,
			Timestamp: s.Timestamp,
			Duration:  s.Duration,
			Error:     s.Error,
			Lineage:   s.Lineage,
		}
		switch {
		case s.AfterKey != "":
			// A key whose payload is missing stays nil rather than inheriting
			// the neighbour's. Showing one node's data under another node's
			// name is the one thing a trace viewer must never do.
			step.After = decoded[s.AfterKey]
		case s.LegacyAfter != nil:
			step.After = s.LegacyAfter
		}
		if step.Error != "" {
			out.ErrorCount++
		}

		// Before is not stored: what entered this node is what left the one
		// before it. The first step has no predecessor, so its Before stays
		// nil — honest, where the old field held the source's own input.
		if i > 0 {
			step.Before = out.Steps[i-1].After
		}
		out.Steps = append(out.Steps, step)
	}
	return out
}

// ErrorCount is how many steps recorded an error, without decoding a payload.
//
// The trace list needs it and nothing else about the steps; decompressing every
// payload of every trace to count errors would be the whole-table read that the
// SQL backends added a parent row to avoid.
func (t StoredTrace) ErrorCount() int {
	n := 0
	for _, s := range t.Steps {
		if s.Error != "" {
			n++
		}
	}
	return n
}
