package registry

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod"
	commsource "github.com/user/hermod/pkg/comm/source"
	enginesource "github.com/user/hermod/pkg/engine/source"
)

// fullSource implements every optional source interface the engine consults.
type fullSource struct {
	lag       uint64
	pending   bool
	lastSeen  time.Time
	threshold time.Duration
	ready     error
	logger    hermod.Logger
}

func (s *fullSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (s *fullSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *fullSource) Ping(context.Context) error                { return nil }
func (s *fullSource) Close() error                              { return nil }

func (s *fullSource) GetLag(context.Context) (uint64, error) { return s.lag, nil }
func (s *fullSource) PendingWork() (bool, bool)              { return s.pending, true }
func (s *fullSource) LastStreamActivity() time.Time          { return s.lastSeen }
func (s *fullSource) StreamSilenceThreshold() time.Duration  { return s.threshold }
func (s *fullSource) IsReady(context.Context) error          { return s.ready }
func (s *fullSource) SetLogger(l hermod.Logger)              { s.logger = l }

// A source is never handed to the engine bare. It is wrapped for metrics, for
// state persistence, for multiplexing across a workflow's source nodes, and
// sometimes for DLQ priority. Go promotes only the methods an embedded
// *interface* declares, so each wrapper silently erases every optional
// capability of what it wraps unless someone forwards it by hand.
//
// That has now been found three separate times — GetLag through MetricsSource,
// then through PrioritySource, then through statefulSource, where it meant the
// engine read zero lag for every CDC workflow in production. Each fix was
// correct and none of them prevented the next occurrence, because nothing
// checked the property itself.
//
// This test checks the property: every wrapper, every interface. It fails when a
// wrapper is added without forwards, and when an interface is added without
// updating the wrappers.
func TestSourceWrappersForwardOptionalInterfaces(t *testing.T) {
	const (
		wantLag       = uint64(21 * 1024 * 1024)
		wantThreshold = 90 * time.Second
	)
	wantSeen := time.Unix(1_700_000_000, 0)

	newInner := func() *fullSource {
		return &fullSource{
			lag:       wantLag,
			pending:   true,
			lastSeen:  wantSeen,
			threshold: wantThreshold,
		}
	}

	// Every wrapper a source can be seen through, and the chain the registry
	// actually builds (statefulSource over MetricsSource over the source).
	wrappers := map[string]func(hermod.Source) hermod.Source{
		"MetricsSource": func(s hermod.Source) hermod.Source {
			return commsource.NewMetricsSource(s, "src-1", "wf-1", nil)
		},
		"statefulSource": func(s hermod.Source) hermod.Source {
			return &statefulSource{Source: s, sourceID: "src-1"}
		},
		"multiSource": func(s hermod.Source) hermod.Source {
			return &multiSource{sources: []*subSource{{nodeID: "n1", sourceID: "src-1", source: s}}}
		},
		"PrioritySource": func(s hermod.Source) hermod.Source {
			return enginesource.NewPrioritySource(&inertSource{}, s, nil)
		},
		"registry chain (multiSource>statefulSource>MetricsSource)": func(s hermod.Source) hermod.Source {
			metrics := commsource.NewMetricsSource(s, "src-1", "wf-1", nil)
			stateful := &statefulSource{Source: metrics, sourceID: "src-1"}
			return &multiSource{sources: []*subSource{{nodeID: "n1", sourceID: "src-1", source: stateful}}}
		},
	}

	for name, wrap := range wrappers {
		t.Run(name, func(t *testing.T) {
			inner := newInner()
			wrapped := wrap(inner)

			t.Run("LagReporter", func(t *testing.T) {
				lr, ok := wrapped.(hermod.LagReporter)
				if !ok {
					t.Fatal("wrapper hides hermod.LagReporter: the engine reads zero lag, so retention alerting and the stall fallback go dead")
				}
				got, err := lr.GetLag(context.Background())
				if err != nil {
					t.Fatalf("GetLag: %v", err)
				}
				if got != wantLag {
					t.Errorf("GetLag = %d, want %d", got, wantLag)
				}
			})

			t.Run("PendingWorkReporter", func(t *testing.T) {
				pw, ok := wrapped.(hermod.PendingWorkReporter)
				if !ok {
					t.Fatal("wrapper hides hermod.PendingWorkReporter: the watchdog cannot tell retained WAL from outstanding work")
				}
				pending, known := pw.PendingWork()
				if !known {
					t.Fatal("wrapper reports the source cannot answer, though it can")
				}
				if !pending {
					t.Error("wrapper reported no outstanding work while the source had some: a real wedge would look idle")
				}
			})

			t.Run("StreamLivenessReporter", func(t *testing.T) {
				lr, ok := wrapped.(hermod.StreamLivenessReporter)
				if !ok {
					t.Fatal("wrapper hides hermod.StreamLivenessReporter: a replication stream that stops being served is never detected")
				}
				if got := lr.LastStreamActivity(); !got.Equal(wantSeen) {
					t.Errorf("LastStreamActivity = %v, want %v", got, wantSeen)
				}
				if got := lr.StreamSilenceThreshold(); got != wantThreshold {
					t.Errorf("StreamSilenceThreshold = %v, want %v", got, wantThreshold)
				}
			})
		})
	}
}

// inertSource is a stand-in for a recovery/DLQ source that has no optional
// capabilities of its own.
type inertSource struct{}

func (inertSource) Read(ctx context.Context) (hermod.Message, error) { return nil, context.Canceled }
func (inertSource) Ack(context.Context, hermod.Message) error        { return nil }
func (inertSource) Ping(context.Context) error                       { return nil }
func (inertSource) Close() error                                     { return nil }
