package optimizer_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/optimizer"
	"github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/config"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// The gate used to change a running engine's sink config on its own. A fix
// is now a suggestion: it goes to the proposer, which stores it for a person
// to approve, and the engine is left exactly as it was.

type proposals struct {
	mu  sync.Mutex
	got []optimizer.Suggestion
}

func (p *proposals) Propose(_ context.Context, s optimizer.Suggestion) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, s)
	return nil
}

func (p *proposals) all() []optimizer.Suggestion {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]optimizer.Suggestion(nil), p.got...)
}

type notes struct {
	mu     sync.Mutex
	titles []string
	bodies []string
}

func (n *notes) notify(_, title, body string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.titles = append(n.titles, title)
	n.bodies = append(n.bodies, body)
}

func (n *notes) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.titles)
}

func engineWithSink(id string) *engine.Engine {
	eng := engine.NewEngine(&mockSource{}, []hermod.Sink{&mockSink{}}, nil)
	eng.SetIDs("wf", "src", []string{id})
	eng.SetSinkConfigs([]config.SinkConfig{{MaxRetries: 2, RetryInterval: time.Second, BatchSize: 100}})
	return eng
}

func TestSelfCorrection_HighErrorRateProposesARetryChangeAndAppliesNothing(t *testing.T) {
	var n notes
	var p proposals
	gate := optimizer.NewSelfCorrectionGate(&mockLogger{}, n.notify)
	gate.SetProposer(&p)
	eng := engineWithSink("n1")

	gate.Analyze("wf", eng, telemetry.StatusUpdate{
		NodeMetrics:      map[string]uint64{"n1": 200},
		NodeErrorMetrics: map[string]uint64{"n1": 60},
	})

	cfg := eng.GetSinkConfigs()[0]
	if cfg.MaxRetries != 2 || cfg.RetryInterval != time.Second {
		t.Fatalf("the gate changed the running sink: %+v", cfg)
	}
	got := p.all()
	if len(got) != 1 || got[0].WorkflowID != "wf" || got[0].NodeID != "n1" || got[0].Action != optimizer.ActionIncreaseRetry {
		t.Fatalf("proposals = %+v", got)
	}
	if got[0].Reason == "" {
		t.Error("the proposal does not say why it was made")
	}
	if n.count() != 1 || !strings.Contains(n.titles[0], "approval") {
		t.Errorf("the operator was not told a fix is waiting: %v", n.titles)
	}
}

func TestSelfCorrection_WithoutAProposerNothingIsApplied(t *testing.T) {
	var n notes
	gate := optimizer.NewSelfCorrectionGate(&mockLogger{}, n.notify)
	eng := engineWithSink("n1")

	gate.Analyze("wf", eng, telemetry.StatusUpdate{
		NodeMetrics:      map[string]uint64{"n1": 200},
		NodeErrorMetrics: map[string]uint64{"n1": 60},
	})

	if cfg := eng.GetSinkConfigs()[0]; cfg.MaxRetries != 2 || cfg.RetryInterval != time.Second {
		t.Fatalf("the gate changed the running sink: %+v", cfg)
	}
}

func TestSelfCorrection_LatencySpikeIsAdviceNotABatchChange(t *testing.T) {
	var n notes
	var p proposals
	gate := optimizer.NewSelfCorrectionGate(&mockLogger{}, n.notify)
	gate.SetProposer(&p)
	eng := engineWithSink("global")

	gate.Analyze("wf", eng, telemetry.StatusUpdate{AvgLatency: 2 * time.Second, ProcessedCount: 600})

	if cfg := eng.GetSinkConfigs()[0]; cfg.BatchSize != 100 {
		t.Fatalf("the gate changed the batch size: %+v", cfg)
	}
	if len(p.all()) != 0 {
		t.Errorf("a batch change was proposed at workflow level, where there is no batch size: %+v", p.all())
	}
	if n.count() != 1 {
		t.Errorf("notifications = %v", n.titles)
	}
}

// advisor records what it was asked.
type advisor struct {
	mu      sync.Mutex
	samples []map[string]any
}

func (a *advisor) SuggestMapping(_ context.Context, _, _ string, sample map[string]any) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.samples = append(a.samples, sample)
	return "map email to contact_email", nil
}

func (a *advisor) calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.samples)
}

func validationFailures() telemetry.StatusUpdate {
	return telemetry.StatusUpdate{
		NodeErrorMetrics: map[string]uint64{"validate-1": 80},
		NodeSamples:      map[string]any{"validate-1": map[string]any{"email": "jane@example.com"}},
	}
}

func TestSelfCorrection_ValidationFailuresWithoutAnAdvisorCallNoModel(t *testing.T) {
	var n notes
	gate := optimizer.NewSelfCorrectionGate(&mockLogger{}, n.notify)
	gate.Analyze("wf", engineWithSink("s"), validationFailures())

	if n.count() != 1 || strings.Contains(n.bodies[0], "AI") {
		t.Fatalf("notifications = %v / %v", n.titles, n.bodies)
	}
}

func TestSelfCorrection_ValidationFailuresAskTheConfiguredAdvisor(t *testing.T) {
	var n notes
	var a advisor
	gate := optimizer.NewSelfCorrectionGate(&mockLogger{}, n.notify)
	gate.SetMappingAdvisor(&a)
	gate.Analyze("wf", engineWithSink("s"), validationFailures())

	deadline := time.Now().Add(2 * time.Second)
	for n.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.calls() != 1 || n.count() != 1 || !strings.Contains(n.bodies[0], "contact_email") {
		t.Fatalf("advisor calls %d, notifications %v", a.calls(), n.bodies)
	}
}
