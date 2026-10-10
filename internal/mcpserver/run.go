package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/reply"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
)

// Run delivers input to an exposed workflow's webhook source, as a POST to
// that webhook would, and answers the way the source is configured to: with
// the workflow's result when it replies synchronously (pkg/comm/reply), with
// "dispatched" otherwise.
//
// The webhook's own API key and signature are not asked for. They
// authenticate anonymous HTTP callers; an MCP caller has already
// authenticated as a Hermod user whose role and vhost were checked, against a
// workflow its owner opted in.
func (s *Service) Run(ctx context.Context, c Caller, id string, input map[string]any) (RunResult, error) {
	wf, err := s.lookup(ctx, c, id)
	if err != nil {
		return RunResult{}, err
	}
	if !c.CanRun {
		return RunResult{}, ErrNotAllowed
	}
	src, ok, err := s.webhookSource(ctx, wf)
	if err != nil {
		return RunResult{}, err
	}
	if !ok {
		return RunResult{}, ErrNotRunnable
	}
	if input == nil {
		input = map[string]any{}
	}
	body, err := json.Marshal(input)
	if err != nil {
		return RunResult{}, fmt.Errorf("encoding the input: %w", err)
	}
	return s.deliver(ctx, c.Name, src, body)
}

// deliver sends body to the webhook source src and waits for the workflow's
// reply when the source is configured to give one.
func (s *Service) deliver(ctx context.Context, caller string, src storage.Source, body []byte) (RunResult, error) {
	path := src.Config["path"]

	// Read after dispatch, the message may already be the engine's and back
	// in the pool, so the id is kept here.
	msgID := uuid.NewString()
	msg := message.AcquireMessage()
	msg.SetID(msgID)
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("webhook")
	msg.SetAfter(body)
	msg.SetMetadata("webhook_path", path)
	msg.SetMetadata("http_method", http.MethodPost)

	// Logged like any webhook request, so the run can be found and replayed.
	_ = s.Store.CreateWebhookRequest(ctx, storage.WebhookRequest{
		Timestamp: time.Now(),
		Path:      path,
		Method:    http.MethodPost,
		Headers:   map[string]string{"X-Hermod-Trigger": "mcp", "X-Hermod-User": caller},
		Body:      body,
	})

	// The waiter is registered before the dispatch, or a fast workflow could
	// finish first and answer nobody.
	wait, timeout := reply.ModeOf(src.Config)
	var pending *reply.Pending
	if wait {
		p, err := reply.Expect(msg)
		if err != nil {
			message.ReleaseMessage(msg)
			return RunResult{}, err
		}
		pending = p
		defer pending.Cancel()
	}

	if err := s.dispatch(ctx, path, msg); err != nil {
		message.ReleaseMessage(msg)
		return RunResult{}, ErrNotListening
	}
	if pending == nil {
		return RunResult{ID: msgID, Status: "dispatched"}, nil
	}
	return await(ctx, pending, msgID, timeout), nil
}

// await waits up to timeout for the workflow to finish with the message.
func await(ctx context.Context, pending *reply.Pending, msgID string, timeout time.Duration) RunResult {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	outcome, err := pending.Wait(waitCtx)
	if err != nil {
		// The message is still the workflow's and may yet be delivered, so
		// this is not a failure: a caller that retries one sends it twice.
		return RunResult{ID: msgID, Status: "pending"}
	}
	res := RunResult{ID: msgID, Status: string(outcome.Status), Error: outcome.Error}
	if len(outcome.Record) > 0 {
		var rec any
		if json.Unmarshal(outcome.Record, &rec) == nil {
			res.Record = rec
		} else {
			res.Record = string(outcome.Record)
		}
	}
	return res
}

// dispatch hands msg to the webhook source listening on path, waking a parked
// workflow and retrying once when nothing is listening.
func (s *Service) dispatch(ctx context.Context, path string, msg hermod.Message) error {
	err := webhook.Dispatch(path, msg)
	if err == nil {
		return nil
	}
	if s.Wake != nil && s.Wake(ctx, "webhook", path) {
		if retryErr := webhook.Dispatch(path, msg); retryErr == nil {
			return nil
		}
	}
	return err
}
