package engine

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
)

func (e *Engine) SetOutboxStorage(outbox hermod.OutboxStorage) {
	e.outboxStore = outbox
}

func (e *Engine) runOutboxRelay(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			e.logger.Error("Panic in runOutboxRelay internal", "error", r, "stack", string(debug.Stack()))
		}
	}()
	if e.outboxStore == nil {
		return
	}

	interval := e.config.OutboxRelayInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			items, err := e.outboxStore.ListOutboxItems(ctx, "pending", 100)
			if err != nil {
				e.logger.Error("Failed to fetch pending outbox items", "workflow_id", e.workflowID, "error", err)
				continue
			}

			for _, item := range items {
				if item.WorkflowID != e.workflowID {
					continue
				}

				// Reconstruct message
				msg := message.AcquireMessage()
				msg.SetPayload(item.Payload)
				for k, v := range item.Metadata {
					msg.SetMetadata(k, v)
				}
				// If we have a stored MessageID, we should probably restore it
				if mid, ok := item.Metadata["_message_id"]; ok {
					msg.SetID(mid)
				}

				// Mark that this message came from the outbox so we can delete it later
				msg.SetMetadata("_outbox_id", item.ID)

				// Try to push back to buffer
				if err := e.buffer.Produce(ctx, msg); err != nil {
					e.logger.Error("Failed to re-produce outbox item to buffer", "workflow_id", e.workflowID, "item_id", item.ID, "error", err)
					item.Attempts++
					item.LastError = err.Error()
					if item.Attempts > 10 {
						item.Status = "failed"
					}
					_ = e.outboxStore.UpdateOutboxItem(ctx, item)
					continue
				}

				// Do NOT delete here! It should be deleted only after successful processing by sinks.
				// We update it to 'processing' to avoid other relays picking it up
				item.Status = "processing"
				_ = e.outboxStore.UpdateOutboxItem(ctx, item)
			}
		}
	}
}
