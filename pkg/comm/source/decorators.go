package source

import (
	"context"
	"errors"
	"fmt"
	"time"

	hermod "github.com/gsoultan/Hermod"
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
	return s.Ping(ctx)
}

func (s *MetricsSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if d, ok := s.Source.(hermod.Discoverer); ok {
		return d.DiscoverDatabases(ctx)
	}
	return nil, errors.New("source does not support database discovery")
}

func (s *MetricsSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if d, ok := s.Source.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, errors.New("source does not support table discovery")
}

func (s *MetricsSource) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if d, ok := s.Source.(hermod.ColumnDiscoverer); ok {
		return d.DiscoverColumns(ctx, table)
	}
	return nil, errors.New("source does not support column discovery")
}

func (s *MetricsSource) DiscoverReplicationSlots(ctx context.Context) ([]hermod.ReplicationSlotInfo, error) {
	if d, ok := s.Source.(hermod.ReplicationDiscoverer); ok {
		return d.DiscoverReplicationSlots(ctx)
	}
	return nil, errors.New("source does not support replication slot discovery")
}

func (s *MetricsSource) DiscoverPublications(ctx context.Context) ([]hermod.PublicationInfo, error) {
	if d, ok := s.Source.(hermod.ReplicationDiscoverer); ok {
		return d.DiscoverPublications(ctx)
	}
	return nil, errors.New("source does not support publication discovery")
}

func (s *MetricsSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if sm, ok := s.Source.(hermod.Sampler); ok {
		return sm.Sample(ctx, table)
	}
	return nil, errors.New("source does not support sampling")
}

func (s *MetricsSource) Snapshot(ctx context.Context, tables ...string) error {
	if sn, ok := s.Source.(hermod.Snapshottable); ok {
		return sn.Snapshot(ctx, tables...)
	}
	return errors.New("source does not support manual snapshots")
}

func (s *MetricsSource) ExecuteSQL(ctx context.Context, query string) ([]map[string]any, error) {
	if se, ok := s.Source.(hermod.SQLExecutor); ok {
		return se.ExecuteSQL(ctx, query)
	}
	return nil, fmt.Errorf("%w: source does not support SQL execution", hermod.ErrNotSupported)
}

func (s *MetricsSource) SetLogger(logger hermod.Logger) {
	if l, ok := s.Source.(hermod.Loggable); ok {
		l.SetLogger(logger)
	}
}

// GetLag forwards lag reporting to the wrapped source.
//
// MetricsSource embeds the hermod.Source interface, and embedding an interface
// does not promote methods the interface does not declare — so wrapping a
// PostgresSource silently hid its GetLag. Every lag-based check downstream
// (workflow status, WAL-retention alerting, the stall watchdog) therefore read
// zero and concluded there was no outstanding work, which is exactly wrong when
// a replication slot is retaining WAL because the pipeline has stopped.
func (s *MetricsSource) GetLag(ctx context.Context) (uint64, error) {
	if lr, ok := s.Source.(hermod.LagReporter); ok {
		return lr.GetLag(ctx)
	}
	return 0, nil
}

// PendingWork forwards the wrapped source's outstanding-work signal. A source
// that cannot answer reports false, which leaves the stall watchdog on its lag
// fallback rather than asserting there is nothing outstanding.
func (s *MetricsSource) PendingWork() (pending bool, known bool) {
	if pw, ok := s.Source.(hermod.PendingWorkReporter); ok {
		return pw.PendingWork()
	}
	return false, false
}

// LastStreamActivity forwards stream liveness from the wrapped source, for the
// same reason GetLag has to be forwarded: an embedded interface does not promote
// methods it does not declare, and a hidden liveness signal reads as "this
// source has no stream to watch" rather than as "this source's stream is fine".
func (s *MetricsSource) LastStreamActivity() time.Time {
	if lr, ok := s.Source.(hermod.StreamLivenessReporter); ok {
		return lr.LastStreamActivity()
	}
	return time.Time{}
}

// StreamSilenceThreshold forwards the wrapped source's silence deadline. Zero
// for a source that does not hold a push stream, which disables the check.
func (s *MetricsSource) StreamSilenceThreshold() time.Duration {
	if lr, ok := s.Source.(hermod.StreamLivenessReporter); ok {
		return lr.StreamSilenceThreshold()
	}
	return 0
}
