package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/user/hermod"
)

// PrioritySource wraps two sources and prioritizes reading from 'recovery' before 'primary'.
type PrioritySource struct {
	primary  hermod.Source
	recovery hermod.Source
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
		if s.logger != nil {
			s.logger.Info("Prioritizing message from Dead Letter Sink", "message_id", msg.ID())
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
