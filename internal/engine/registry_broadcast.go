package engine

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	pkgengine "github.com/user/hermod/pkg/engine"
	"github.com/user/hermod/pkg/message"
)

// --- Subscribe / Unsubscribe ---

func (r *Registry) SubscribeStatus() chan pkgengine.StatusUpdate {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	ch := make(chan pkgengine.StatusUpdate, 100)
	r.statusSubs[ch] = true
	return ch
}

func (r *Registry) UnsubscribeStatus(ch chan pkgengine.StatusUpdate) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	delete(r.statusSubs, ch)
	close(ch)
}

func (r *Registry) SubscribeDashboardStats() chan DashboardStats {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	ch := make(chan DashboardStats, 10)
	r.dashboardSubs[ch] = true
	return ch
}

func (r *Registry) UnsubscribeDashboardStats(ch chan DashboardStats) {
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

// --- Broadcast ---

func (r *Registry) broadcastStatus(update pkgengine.StatusUpdate) {
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
	if r.logStorage != nil {
		err := r.logStorage.CreateLog(ctx, l)

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

func (r *Registry) getConsistentData(msg hermod.Message) map[string]any {
	data := make(map[string]any)
	if msg != nil {
		if dm, ok := msg.(*message.DefaultMessage); ok {
			// Use the consistent MarshalJSON representation
			msgJSON, _ := dm.MarshalJSON()
			_ = json.Unmarshal(msgJSON, &data)
		} else {
			// Fallback for other message types
			baseData := msg.Data()
			for k, v := range baseData {
				data[k] = v
			}
		}
	}
	return data
}

func (r *Registry) broadcastLiveMessageFromHermod(workflowID, nodeID string, msg hermod.Message, isError bool, errStr string) {
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

func (r *Registry) RecordStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) {
	if r.logStorage != nil {
		_ = r.logStorage.RecordTraceStep(ctx, workflowID, messageID, step)
	}
}
