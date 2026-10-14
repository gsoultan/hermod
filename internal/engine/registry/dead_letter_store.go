package registry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/gsoultan/hermod/internal/storage"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// deadLetterQueueSize bounds the dead letters waiting to be written. A store
// that falls behind costs dead letters on the page, never pipeline throughput:
// the queue is the only thing between the engine's write path and the store.
const deadLetterQueueSize = 1024

// defaultDeadLetterRetention is how long a dead letter stays on the page.
const defaultDeadLetterRetention = 30 * 24 * time.Hour

// deadLetterWriteTimeout bounds one write, so a hung store cannot stall the
// queue behind it forever.
const deadLetterWriteTimeout = 10 * time.Second

// deadLetterRecorder writes dead letters to the store from one goroutine,
// started on first use. Its zero value is ready.
type deadLetterRecorder struct {
	once    sync.Once
	ch      chan storage.DeadLetter
	pending atomic.Int64
	dropped atomic.Int64
}

// recordDeadLetter copies what the page and a replay need out of d and queues
// it. It never blocks: when the queue is full the dead letter is dropped and
// counted. The message is still in the workflow's dead-letter sink.
func (r *Registry) recordDeadLetter(wf storage.Workflow, d pkgengine.DeadLetter) {
	if _, ok := r.store().(storage.DeadLetterStore); !ok {
		return
	}
	rec := &r.deadLetterRecords
	rec.once.Do(func() {
		rec.ch = make(chan storage.DeadLetter, deadLetterQueueSize)
		go r.runDeadLetterRecorder()
	})
	failedAt := d.FailedAt
	if failedAt.IsZero() {
		failedAt = time.Now()
	}
	where := d.NodeID
	if where == "" {
		where = d.SinkID
	}
	dl := storage.DeadLetter{
		ID: uuid.NewString(), VHost: wf.VHost, WorkflowID: wf.ID, MessageID: d.MessageID,
		NodeID: d.NodeID, SinkID: d.SinkID, Error: d.Error, FailedAt: failedAt.UTC(),
		Status:  storage.DeadLetterQueued,
		History: []storage.DeadLetterEvent{{At: failedAt.UTC(), Action: "failed", Detail: where}},
	}
	if m := d.Message; m != nil {
		dl.Payload = m.Payload()
		dl.Metadata = m.Metadata()
		dl.Origin = storage.DeadLetterOrigin{
			Operation: string(m.Operation()), Table: m.Table(), Schema: m.Schema(), Before: m.Before(),
		}
	}
	rec.pending.Add(1)
	select {
	case rec.ch <- dl:
	default:
		rec.pending.Add(-1)
		if n := rec.dropped.Add(1); n == 1 || n%1000 == 0 {
			r.logger.Warn("Dead letter store is behind; dead letters are being left off the dead letter page "+
				"(they are still in the workflow's dead-letter sink)", "dropped_total", n, "workflow_id", wf.ID)
		}
		telemetry.DeadLetterRecordsDropped.Inc()
	}
}

func (r *Registry) runDeadLetterRecorder() {
	rec := &r.deadLetterRecords
	for {
		select {
		case <-r.ctx.Done():
			return
		case dl := <-rec.ch:
			r.writeDeadLetter(dl)
			rec.pending.Add(-1)
		}
	}
}

func (r *Registry) writeDeadLetter(dl storage.DeadLetter) {
	ds, ok := r.store().(storage.DeadLetterStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadLetterWriteTimeout)
	defer cancel()
	if err := ds.PutDeadLetter(ctx, dl); err != nil {
		r.logger.Error("Could not keep a dead letter for the dead letter page", "workflow_id", dl.WorkflowID,
			"message_id", dl.MessageID, "error", err)
	}
}

// FlushDeadLetterRecords waits, up to timeout, for queued dead letters to be
// written. It reports whether the queue drained.
func (r *Registry) FlushDeadLetterRecords(timeout time.Duration) bool {
	rec := &r.deadLetterRecords
	deadline := time.Now().Add(timeout)
	for rec.pending.Load() > 0 {
		if time.Now().After(deadline) {
			r.logger.Warn("Dead letters were still being written when the flush deadline passed",
				"pending", rec.pending.Load())
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

func deadLetterRetention() time.Duration {
	if d, ok := durationFromEnv("HERMOD_DEAD_LETTER_RETENTION"); ok && d > 0 {
		return d
	}
	return defaultDeadLetterRetention
}

// purgeDeadLetters removes dead letters older than the retention, whatever
// their status: a queued one that old is still in the workflow's sink.
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
