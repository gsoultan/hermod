// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package registry

import (
	"context"
	"errors"
	"maps"
	"time"

	"github.com/google/uuid"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// defaultDeadLetterRetention is how long a replayed or discarded dead letter
// stays on the page. One still waiting stays until someone deals with it.
const defaultDeadLetterRetention = 30 * 24 * time.Hour

// deadLetterWriteTimeout bounds one write, so a hung store fails the park
// rather than stalling the writer behind it forever.
const deadLetterWriteTimeout = 10 * time.Second

// deadLetterQueue is the dead-letter queue of a workflow that keeps its dead
// letters, or nil when it does not or the backend cannot keep them. The
// engine parks a message by writing it here.
func (r *Registry) deadLetterQueue(wf storage.Workflow) hermod.Sink {
	if !wf.DeadLetters {
		return nil
	}
	ds, ok := r.store().(storage.DeadLetterStore)
	if !ok {
		r.logger.Warn("This storage backend cannot keep dead letters; failed messages stay on the source",
			"workflow_id", wf.ID)
		return nil
	}
	return &deadLetterSink{store: ds, workflowID: wf.ID, vhost: wf.VHost}
}

// deadLetterSink keeps each message the engine parks in Hermod's own store,
// where the Dead Letters page lists it. The write is synchronous and its error
// is the engine's: a message is acknowledged to its source only once it is
// kept, because the page is the only copy.
type deadLetterSink struct {
	store      storage.DeadLetterStore
	workflowID string
	vhost      string
}

func (s *deadLetterSink) Write(ctx context.Context, m hermod.Message) error {
	if m == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, deadLetterWriteTimeout)
	defer cancel()
	return s.store.PutDeadLetter(ctx, deadLetterRecord(s.workflowID, s.vhost, m))
}

func (s *deadLetterSink) Ping(context.Context) error { return nil }
func (s *deadLetterSink) Close() error               { return nil }

// deadLetterRecord is what the page and a replay need of m: the message, and
// what failed it, where and why, from the marks the engine put on it.
func deadLetterRecord(workflowID, vhost string, m hermod.Message) storage.DeadLetter {
	meta := m.Metadata()
	failedAt, err := time.Parse(time.RFC3339, meta["_hermod_failed_at"])
	if err != nil {
		failedAt = time.Now()
	}
	failedAt = failedAt.UTC()
	nodeID, sinkID := meta["_hermod_failed_node"], meta["_hermod_failed_sink"]
	where := nodeID
	if where == "" {
		where = sinkID
	}
	return storage.DeadLetter{
		ID: uuid.NewString(), VHost: vhost, WorkflowID: workflowID, MessageID: m.ID(),
		NodeID: nodeID, SinkID: sinkID, Error: meta["_hermod_last_error"], FailedAt: failedAt,
		Payload: m.Payload(), Metadata: maps.Clone(meta),
		Origin: storage.DeadLetterOrigin{
			Operation: string(m.Operation()), Table: m.Table(), Schema: m.Schema(), Before: m.Before(),
		},
		Status:  storage.DeadLetterQueued,
		History: []storage.DeadLetterEvent{{At: failedAt, Action: "failed", Detail: where}},
	}
}

func deadLetterRetention() time.Duration {
	if d, ok := durationFromEnv("HERMOD_DEAD_LETTER_RETENTION"); ok && d > 0 {
		return d
	}
	return defaultDeadLetterRetention
}

// purgeDeadLetters removes replayed and discarded dead letters older than the
// retention. A waiting one is the only copy of its message, so it stays.
func (r *Registry) purgeDeadLetters(ctx context.Context, store any) {
	ds, ok := store.(storage.DeadLetterStore)
	if !ok {
		return
	}
	if _, err := ds.PurgeDeadLetters(ctx, time.Now().Add(-deadLetterRetention())); err != nil &&
		!errors.Is(err, storage.ErrDeadLettersUnsupported) {
		r.logger.Error("Registry: purging dead letters failed", "error", err)
	}
}
