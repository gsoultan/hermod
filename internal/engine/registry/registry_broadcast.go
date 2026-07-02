package registry

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/engine/telemetry"
)

// --- Subscribe / Unsubscribe ---

func (r *Registry) SubscribeStatus() chan telemetry.StatusUpdate {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	ch := make(chan telemetry.StatusUpdate, 100)
	r.statusSubs[ch] = true
	return ch
}

func (r *Registry) UnsubscribeStatus(ch chan telemetry.StatusUpdate) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	delete(r.statusSubs, ch)
	close(ch)
}

// StatusSubscriberCount returns the number of active status subscribers.
// It is primarily useful for observability and tests that assert subscribers
// are released when a client disconnects.
func (r *Registry) StatusSubscriberCount() int {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	return len(r.statusSubs)
}

func (r *Registry) SubscribeDashboardStats() chan storage.DashboardStats {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	ch := make(chan storage.DashboardStats, 10)
	r.dashboardSubs[ch] = true
	return ch
}

func (r *Registry) UnsubscribeDashboardStats(ch chan storage.DashboardStats) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	delete(r.dashboardSubs, ch)
	close(ch)
}

func (r *Registry) SubscribeLogs() chan storage.Log {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	ch := make(chan storage.Log, 100)
	r.logSubs[ch] = true
	return ch
}

func (r *Registry) UnsubscribeLogs(ch chan storage.Log) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	delete(r.logSubs, ch)
	close(ch)
}

func (r *Registry) SubscribeLiveMessages() chan LiveMessage {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	ch := make(chan LiveMessage, 100)
	r.liveMsgSubs[ch] = true
	return ch
}

func (r *Registry) UnsubscribeLiveMessages(ch chan LiveMessage) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	delete(r.liveMsgSubs, ch)
	close(ch)
}

func (r *Registry) SubscribeDebugger(workflowID string) chan DebuggerEvent {
	r.debuggerSubsMu.Lock()
	defer r.debuggerSubsMu.Unlock()
	if r.debuggerSubs == nil {
		r.debuggerSubs = make(map[string]map[chan DebuggerEvent]bool)
	}
	if r.debuggerSubs[workflowID] == nil {
		r.debuggerSubs[workflowID] = make(map[chan DebuggerEvent]bool)
	}
	ch := make(chan DebuggerEvent, 10)
	r.debuggerSubs[workflowID][ch] = true
	return ch
}

func (r *Registry) UnsubscribeDebugger(workflowID string, ch chan DebuggerEvent) {
	r.debuggerSubsMu.Lock()
	defer r.debuggerSubsMu.Unlock()
	if r.debuggerSubs[workflowID] != nil {
		delete(r.debuggerSubs[workflowID], ch)
		if len(r.debuggerSubs[workflowID]) == 0 {
			delete(r.debuggerSubs, workflowID)
		}
	}
	close(ch)
}

func (r *Registry) DebuggerCommand(workflowID, msgID, action string) {
	key := workflowID + ":" + msgID
	r.debugChansMu.Lock()
	ch, ok := r.debugChans[key]
	r.debugChansMu.Unlock()

	if ok {
		select {
		case ch <- action:
		default:
		}
	}
}

func (r *Registry) broadcastDebuggerEvent(ev DebuggerEvent) {
	r.debuggerSubsMu.RLock()
	subs := r.debuggerSubs[ev.WorkflowID]
	if len(subs) == 0 {
		r.debuggerSubsMu.RUnlock()
		return
	}
	// Copy to avoid holding lock while sending
	chans := make([]chan DebuggerEvent, 0, len(subs))
	for ch := range subs {
		chans = append(chans, ch)
	}
	r.debuggerSubsMu.RUnlock()

	for _, ch := range chans {
		select {
		case ch <- ev:
		default:
		}
	}
}

// --- Broadcast ---

func (r *Registry) BroadcastStatus(update telemetry.StatusUpdate) {
	r.statusSubsMu.Lock()
	for ch := range r.statusSubs {
		select {
		case ch <- update:
		default:
		}
	}
	r.statusSubsMu.Unlock()

	// Throttle dashboard stats broadcasts
	r.statusSubsMu.Lock()
	shouldBroadcast := len(r.dashboardSubs) > 0 && time.Since(r.lastDashboardUpdate) > 500*time.Millisecond
	if shouldBroadcast {
		r.lastDashboardUpdate = time.Now()
	}
	r.statusSubsMu.Unlock()

	if shouldBroadcast {
		ctx := context.Background()
		stats, err := r.GetDashboardStats(ctx, "")
		if err == nil {
			r.statusSubsMu.Lock()
			for ch := range r.dashboardSubs {
				select {
				case ch <- stats:
				default:
				}
			}
			r.statusSubsMu.Unlock()
		}
	}
}

func (r *Registry) broadcastLog(engineID, level, msg string) {
	r.broadcastLogWithData(engineID, level, msg, "")
}

func (r *Registry) broadcastLogWithData(engineID, level, msg, data string) {
	l := storage.Log{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
		Data:      data,
	}

	r.mu.Lock()
	if eng, ok := r.engines[engineID]; ok && eng.isWorkflow {
		l.WorkflowID = engineID
	}
	r.mu.Unlock()

	_ = r.CreateLog(context.Background(), l)
}

func (r *Registry) CreateLog(ctx context.Context, l storage.Log) error {
	r.mu.Lock()
	ls := r.logStorage
	r.mu.Unlock()

	if ls != nil {
		err := ls.CreateLog(ctx, l)

		r.statusSubsMu.Lock()
		for ch := range r.logSubs {
			select {
			case ch <- l:
			default:
			}
		}
		r.statusSubsMu.Unlock()

		return err
	}
	return nil
}

func (r *Registry) CreateLogs(ctx context.Context, logs []storage.Log) error {
	r.mu.Lock()
	ls := r.logStorage
	r.mu.Unlock()

	if ls != nil {
		err := ls.CreateLogs(ctx, logs)

		r.statusSubsMu.Lock()
		for _, l := range logs {
			for ch := range r.logSubs {
				select {
				case ch <- l:
				default:
				}
			}
		}
		r.statusSubsMu.Unlock()

		return err
	}
	return nil
}

func (r *Registry) PurgeLogs(ctx context.Context, before time.Time) error {
	r.mu.Lock()
	ls := r.logStorage
	r.mu.Unlock()

	if ls != nil {
		return ls.PurgeLogs(ctx, before)
	}
	return nil
}

func (r *Registry) DeleteLogs(ctx context.Context, filter storage.LogFilter) error {
	r.mu.Lock()
	ls := r.logStorage
	r.mu.Unlock()

	if ls != nil {
		return ls.DeleteLogs(ctx, filter)
	}
	return nil
}

func (r *Registry) broadcastLiveMessage(msg LiveMessage) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()

	for ch := range r.liveMsgSubs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// hasLiveMessageSubscribers reports whether any client is currently watching the
// live message stream. It is used to skip the (expensive) full-payload
// serialization performed by broadcastLiveMessageFromHermod when nobody is
// observing, which is the common case and a major source of allocation churn.
func (r *Registry) hasLiveMessageSubscribers() bool {
	r.statusSubsMu.RLock()
	defer r.statusSubsMu.RUnlock()
	return len(r.liveMsgSubs) > 0
}

// hasStatusObservers reports whether any client is watching status, dashboard
// or live-message streams. Node payload samples are only consumed by these
// observers, so capturing them (which copies the message payload) is skipped
// entirely when nobody is connected.
func (r *Registry) hasStatusObservers() bool {
	r.statusSubsMu.RLock()
	defer r.statusSubsMu.RUnlock()
	return len(r.statusSubs) > 0 || len(r.dashboardSubs) > 0 || len(r.liveMsgSubs) > 0
}

func (r *Registry) getConsistentData(msg hermod.Message) map[string]any {
	if msg == nil {
		return nil
	}

	// Optimization: Use ToMap() instead of JSON marshal/unmarshal cycle.
	return msg.ToMap()
}

func (r *Registry) broadcastLiveMessageFromHermod(workflowID, nodeID string, msg hermod.Message, isError bool, errStr string) {
	// Skip all work (including the full-payload JSON round-trip in
	// getConsistentData) when no client is watching the live stream.
	if !r.hasLiveMessageSubscribers() {
		return
	}

	data := r.getConsistentData(msg)

	r.broadcastLiveMessage(LiveMessage{
		WorkflowID: workflowID,
		NodeID:     nodeID,
		Timestamp:  time.Now(),
		Data:       data,
		IsError:    isError,
		Error:      errStr,
	})
}

func (r *Registry) isDebuggerAttached(workflowID string) bool {
	r.debuggerSubsMu.RLock()
	defer r.debuggerSubsMu.RUnlock()
	return len(r.debuggerSubs[workflowID]) > 0
}

func (r *Registry) pauseForDebugger(workflowID, nodeID string, msg hermod.Message) {
	if msg == nil {
		return
	}

	ev := DebuggerEvent{
		WorkflowID: workflowID,
		NodeID:     nodeID,
		MsgID:      msg.ID(),
		Data:       r.getConsistentData(msg),
		State:      "paused",
	}
	r.broadcastDebuggerEvent(ev)

	key := workflowID + ":" + msg.ID()
	ch := make(chan string, 1)

	r.debugChansMu.Lock()
	r.debugChans[key] = ch
	r.debugChansMu.Unlock()

	defer func() {
		r.debugChansMu.Lock()
		delete(r.debugChans, key)
		r.debugChansMu.Unlock()
	}()

	select {
	case action := <-ch:
		r.broadcastDebuggerEvent(DebuggerEvent{
			WorkflowID: workflowID,
			NodeID:     nodeID,
			MsgID:      msg.ID(),
			State:      action,
		})
	case <-time.After(5 * time.Minute):
		// Timeout
	}
}

func (r *Registry) RecordStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) {
	r.mu.Lock()
	ls := r.logStorage
	r.mu.Unlock()

	if ls != nil {
		_ = ls.RecordTraceStep(ctx, workflowID, messageID, step)
	}
}

// recordSourceIngestTrace records an ingestion trace step and broadcasts a live
// message the moment a message first enters the workflow at its source node.
// This guarantees the message trace and the live log always show "message
// received" activity even before any transformation runs, so a working source
// is never mistaken for a silent/broken one.
func (r *Registry) recordSourceIngestTrace(ctx context.Context, workflowID, sourceNodeID string, msg hermod.Message) {
	if msg == nil || workflowID == "" {
		return
	}
	nodeID := sourceNodeID
	if nodeID == "" {
		nodeID = "source"
	}
	// Only build and persist the trace step (which serializes the whole
	// payload) when a log store is actually configured to receive it.
	if r.hasLogStorage() {
		r.RecordStep(ctx, workflowID, msg.ID(), hermod.TraceStep{
			NodeID:    nodeID,
			Timestamp: time.Now(),
			After:     r.getConsistentData(msg),
			Lineage:   "source_ingest",
		})
	}
	r.broadcastLiveMessageFromHermod(workflowID, nodeID, msg, false, "")
}

// hasLogStorage reports whether a log storage backend is configured. Trace
// steps are only meaningful (and only serialized) when there is somewhere to
// persist them.
func (r *Registry) hasLogStorage() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.logStorage != nil
}
