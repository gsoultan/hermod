package source

import (
	"context"
	"fmt"
	"sync"
	"time"

	hermod "github.com/gsoultan/Hermod"
)

// PrioritySource wraps two sources and prioritizes reading from 'recovery' before 'primary'.
type PrioritySource struct {
	primary  hermod.Source
	recovery hermod.Source

	// loggerMu guards logger: SetLogger can arrive at any time via
	// Engine.SetLogger while Read is already running.
	loggerMu sync.RWMutex
	logger   hermod.Logger
}

func NewPrioritySource(recovery, primary hermod.Source, logger hermod.Logger) *PrioritySource {
	return &PrioritySource{
		primary:  primary,
		recovery: recovery,
		logger:   logger,
	}
}

func (s *PrioritySource) Read(ctx context.Context) (hermod.Message, error) {
	// Attempt a non-blocking or short-timeout read from recovery (DLQ) first.
	// This ensures we don't starve the primary source if the DLQ is empty.
	recoveryCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	msg, err := s.recovery.Read(recoveryCtx)
	if err == nil && msg != nil {
		if l := s.log(); l != nil {
			l.Info("Prioritizing message from Dead Letter Sink", "message_id", msg.ID())
		}
		msg.SetMetadata("_hermod_source", "recovery")
		return msg, nil
	}

	// Fallback to primary source (blocking read)
	msg, err = s.primary.Read(ctx)
	if err == nil && msg != nil {
		msg.SetMetadata("_hermod_source", "primary")
	}
	return msg, err
}

func (s *PrioritySource) Ack(ctx context.Context, msg hermod.Message) error {
	if s_id, ok := msg.Metadata()["_hermod_source"]; ok && s_id == "recovery" {
		return s.recovery.Ack(ctx, msg)
	}
	return s.primary.Ack(ctx, msg)
}

func (s *PrioritySource) Ping(ctx context.Context) error {
	// Recovery source might be optional or might fail without affecting primary necessarily,
	// but for priority source we usually want both healthy if recovery is enabled.
	if err := s.recovery.Ping(ctx); err != nil {
		return fmt.Errorf("DLQ source ping failed: %w", err)
	}
	return s.primary.Ping(ctx)
}

func (s *PrioritySource) Close() error {
	_ = s.recovery.Close()
	return s.primary.Close()
}

func (s *PrioritySource) log() hermod.Logger {
	s.loggerMu.RLock()
	defer s.loggerMu.RUnlock()
	return s.logger
}

// SetLogger forwards the engine's logger to both wrapped sources and keeps a
// copy for this wrapper's own DLQ-priority reporting.
func (s *PrioritySource) SetLogger(l hermod.Logger) {
	s.loggerMu.Lock()
	s.logger = l
	s.loggerMu.Unlock()

	for _, src := range []hermod.Source{s.recovery, s.primary} {
		if lg, ok := src.(hermod.Loggable); ok {
			lg.SetLogger(l)
		}
	}
}

// IsReady runs each wrapped source's deep readiness check, falling back to Ping
// for sources that do not have one.
//
// Without this, wrapping a source for DLQ priority downgraded the engine's
// health check from the CDC-aware IsReady (which verifies wal_level, that the
// replication slot exists and is active, and that the publication is present —
// pkg/comm/source/postgres/postgres.go:1671) to a plain connection Ping, which
// keeps returning healthy long after replication has stopped.
func (s *PrioritySource) IsReady(ctx context.Context) error {
	if err := readyOrPing(ctx, s.recovery); err != nil {
		return fmt.Errorf("DLQ source readiness failed: %w", err)
	}
	return readyOrPing(ctx, s.primary)
}

func readyOrPing(ctx context.Context, src hermod.Source) error {
	if rc, ok := src.(hermod.ReadyChecker); ok {
		return rc.IsReady(ctx)
	}
	return src.Ping(ctx)
}

// GetLag reports the outstanding work held by the wrapped sources.
//
// Embedding or wrapping a source does not carry its optional interfaces across,
// and every lag-based check downstream — workflow status, WAL-retention
// alerting, and the stall watchdog's "is any work outstanding?" question
// (pkg/engine/stall.go:164) — reads zero when the assertion fails. Zero lag on a
// wedged CDC pipeline is indistinguishable from an idle healthy one, so the
// wedge goes unreported for as long as it lasts. This is the same defect already
// found and fixed in MetricsSource (pkg/comm/source/decorators.go:120).
func (s *PrioritySource) GetLag(ctx context.Context) (uint64, error) {
	var total uint64
	for _, src := range []hermod.Source{s.primary, s.recovery} {
		lr, ok := src.(hermod.LagReporter)
		if !ok {
			continue
		}
		lag, err := lr.GetLag(ctx)
		if err != nil {
			// Report the failure rather than a zero: an unanswerable lag query
			// is not evidence that there is no work outstanding.
			return 0, err
		}
		total += lag
	}
	return total, nil
}

// PendingWork reports whether either wrapped source is still owed
// acknowledgements: a DLQ being drained holds outstanding work just as a primary
// stream does.
func (s *PrioritySource) PendingWork() (pending bool, known bool) {
	for _, src := range []hermod.Source{s.primary, s.recovery} {
		pw, ok := src.(hermod.PendingWorkReporter)
		if !ok {
			continue
		}
		srcPending, srcKnown := pw.PendingWork()
		if !srcKnown {
			continue
		}
		known = true
		if srcPending {
			return true, true
		}
	}
	return false, known
}

// LastStreamActivity reports the primary source's stream liveness. Only the
// primary holds a replication stream; the recovery source is a dead-letter queue
// being drained, which has no server-push cadence to vouch for.
func (s *PrioritySource) LastStreamActivity() time.Time {
	if lr, ok := s.primary.(hermod.StreamLivenessReporter); ok {
		return lr.LastStreamActivity()
	}
	return time.Time{}
}

// StreamSilenceThreshold reports the primary source's silence deadline.
func (s *PrioritySource) StreamSilenceThreshold() time.Duration {
	if lr, ok := s.primary.(hermod.StreamLivenessReporter); ok {
		return lr.StreamSilenceThreshold()
	}
	return 0
}

func (s *PrioritySource) GetState() map[string]string {
	state := make(map[string]string)
	if st, ok := s.recovery.(hermod.Stateful); ok {
		for k, v := range st.GetState() {
			state["recovery:"+k] = v
		}
	}
	if st, ok := s.primary.(hermod.Stateful); ok {
		for k, v := range st.GetState() {
			state["primary:"+k] = v
		}
	}
	return state
}

func (s *PrioritySource) SetState(state map[string]string) {
	recoveryState := make(map[string]string)
	primaryState := make(map[string]string)
	for k, v := range state {
		if len(k) > 9 && k[:9] == "recovery:" {
			recoveryState[k[9:]] = v
		} else if len(k) > 8 && k[:8] == "primary:" {
			primaryState[k[8:]] = v
		}
	}
	if st, ok := s.recovery.(hermod.Stateful); ok {
		st.SetState(recoveryState)
	}
	if st, ok := s.primary.(hermod.Stateful); ok {
		st.SetState(primaryState)
	}
}
