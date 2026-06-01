package source

import (
	"context"
	"time"

	"github.com/user/hermod"
)

// MetricsSource wraps a Source and records metrics.
type MetricsSource struct {
	hermod.Source
	sourceID   string
	workflowID string
	onRead     func(duration time.Duration)
}

func NewMetricsSource(s hermod.Source, sourceID, workflowID string, onRead func(time.Duration)) *MetricsSource {
	return &MetricsSource{
		Source:     s,
		sourceID:   sourceID,
		workflowID: workflowID,
		onRead:     onRead,
	}
}

func (s *MetricsSource) Read(ctx context.Context) (hermod.Message, error) {
	start := time.Now()
	msg, err := s.Source.Read(ctx)
	if s.onRead != nil {
		s.onRead(time.Since(start))
	}
	return msg, err
}

func (s *MetricsSource) GetState() map[string]string {
	if st, ok := s.Source.(hermod.Stateful); ok {
		return st.GetState()
	}
	return nil
}

func (s *MetricsSource) SetState(state map[string]string) {
	if st, ok := s.Source.(hermod.Stateful); ok {
		st.SetState(state)
	}
}

func (s *MetricsSource) IsReady(ctx context.Context) error {
	if rc, ok := s.Source.(hermod.ReadyChecker); ok {
		return rc.IsReady(ctx)
	}
	return s.Source.Ping(ctx)
}
