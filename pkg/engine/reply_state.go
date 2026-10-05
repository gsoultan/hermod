package engine

import (
	"encoding/json"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/reply"
)

// errNoSinkReached is what a waiting caller is told when the workflow has
// sinks and routed its message to none of them.
const errNoSinkReached = "the workflow reached no sink for this message"

// replyState is what processMessage knows about the caller waiting for the
// message it is handling, if there is one.
//
// It is a plain value on processMessage's stack, and every method does nothing
// for a message nobody awaits: the common case pays one metadata lookup and no
// allocation.
type replyState struct {
	id      string
	awaited bool
	out     reply.Outcome
}

// conclude records what happened to the message. rec is the message the caller
// is shown — what a sink was sent, or the message itself when none was.
//
// The record is captured here and not when the caller is answered, because by
// then the routed messages have been released.
func (s *replyState) conclude(status reply.Status, cause string, rec hermod.Message) {
	if !s.awaited {
		return
	}
	s.out = reply.Outcome{Status: status, Error: cause, Record: recordJSON(rec)}
}

// resolve answers the waiting caller.
func (s *replyState) resolve() {
	if !s.awaited {
		return
	}
	reply.Resolve(s.id, s.out)
}

// recordJSON renders a message the way a JSON sink receives it.
func recordJSON(msg hermod.Message) []byte {
	if msg == nil {
		return nil
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return nil
	}
	return b
}

// firstRouted is the message sent to the first sink the workflow routed to, or
// fallback when it routed to none.
func firstRouted(targets []RoutedMessage, fallback hermod.Message) hermod.Message {
	for _, t := range targets {
		if t.Message != nil {
			return t.Message
		}
	}
	return fallback
}

// firstDeadLettered is the first routed message whose write failed and was
// parked in the dead-letter sink, or nil.
func firstDeadLettered(targets []RoutedMessage) hermod.Message {
	for _, t := range targets {
		if v, _ := hermod.MetadataValue(t.Message, MetaDeadLettered); v == "true" {
			return t.Message
		}
	}
	return nil
}

// lastError is the failure the engine recorded on a message it parked.
func lastError(msg hermod.Message) string {
	if v, _ := hermod.MetadataValue(msg, "_hermod_last_error"); v != "" {
		return v
	}
	return "the message was parked in the dead-letter sink"
}
