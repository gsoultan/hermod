package ml

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/onnxscore"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// ErrScoringNotTrained is returned for in-process scoring of a model served
// elsewhere: Hermod does not hold its graph.
var ErrScoringNotTrained = errors.New("only a model trained here can be scored in-process")

const (
	// scorerLoadTimeout bounds fetching and checking a version's graph.
	scorerLoadTimeout = 30 * time.Second
	// scorerRetryAfter is how long a load that failed for a reason that may
	// pass — the worker unreachable — is remembered before it is tried again.
	// A graph the scorer refuses is remembered until the version changes.
	scorerRetryAfter = 30 * time.Second
	// maxScorers bounds the cache: one entry per model scored in-process.
	maxScorers = 256
)

// SetScoring sets where a trained model's predictions are computed:
// storage.MLScoringWorker, or storage.MLScoringInProcess to score its live
// version inside Hermod whenever its graph allows.
func (s *Service) SetScoring(ctx context.Context, vhost, name, scoring string) error {
	if err := storage.ValidateMLScoring(scoring); err != nil {
		return err
	}
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return err
	}
	if m.Backend != storage.MLBackendWorker && scoring == storage.MLScoringInProcess {
		return fmt.Errorf("model %q is served elsewhere: %w", name, ErrScoringNotTrained)
	}
	ss, ok := s.store().(storage.MLScoringStore)
	if !ok {
		return storage.ErrMLModelsUnsupported
	}
	if err := ss.SetMLModelScoring(ctx, vhost, name, scoring); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("%w: %q in vhost %q", ErrModelNotFound, name, vhost)
		}
		return err
	}
	return nil
}

// ScoringStatus says how a model's predictions are computed now.
type ScoringStatus struct {
	// Scoring is the setting: storage.MLScoringWorker or MLScoringInProcess.
	Scoring string `json:"scoring"`
	// InProcess reports whether the live version is scored in-process.
	InProcess bool `json:"in_process"`
	// Version is the live version the status is about.
	Version string `json:"version,omitempty"`
	// Ops are the operators of its graph, when it was loaded.
	Ops []string `json:"ops,omitempty"`
	// Reason says why in-process scoring is set but not in use.
	Reason string `json:"reason,omitempty"`
}

// Scoring reports how the model is scored. With in-process scoring set, it
// loads the live version's graph, as the next prediction would, so the
// answer is the one Predict acts on.
func (s *Service) Scoring(ctx context.Context, vhost, name string) (ScoringStatus, error) {
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return ScoringStatus{}, err
	}
	st := ScoringStatus{Scoring: storage.MLScoringWorker, Version: m.RemoteVersion}
	if m.Scoring != storage.MLScoringInProcess {
		return st, nil
	}
	st.Scoring = storage.MLScoringInProcess
	// A graph that cannot be scored is not an error of the status call: it
	// is the answer, in Reason.
	e, loadErr := s.scorer(ctx, m)
	if loadErr == nil {
		st.InProcess, st.Ops = true, e.Ops()
	} else {
		st.Reason = loadErr.Error()
	}
	return st, nil
}

// predictInProcess scores rows with the live version's graph, and reports
// false when the caller must ask the worker instead: the graph is not
// supported, it could not be fetched, or the rows hold a value the scorer
// will not guess at.
func (s *Service) predictInProcess(ctx context.Context, m storage.MLModel, rows []inference.Row) ([]inference.Row, bool) {
	sc, err := s.scorer(ctx, m)
	if err != nil {
		scoringTotal.WithLabelValues(m.VHost, m.Name, "fallback").Inc()
		return nil, false
	}
	out, err := sc.Predict(rows)
	if err != nil {
		scoringTotal.WithLabelValues(m.VHost, m.Name, "fallback").Inc()
		return nil, false
	}
	scoringTotal.WithLabelValues(m.VHost, m.Name, "in_process").Inc()
	return out, true
}

// scorer returns the live version's scorer, loading it once for every
// caller. Its key is the worker, the model and the version, and the time the
// model was last saved: a model deleted and trained again from version 1 is
// saved anew, so it is never scored with the old graph.
func (s *Service) scorer(ctx context.Context, m storage.MLModel) (*onnxscore.Scorer, error) {
	if s.worker == nil {
		return nil, ErrNoWorker
	}
	if m.RemoteVersion == "" {
		return nil, ErrNoLiveVersion
	}
	id := scorerID{version: m.RemoteVersion, saved: m.UpdatedAt}
	e := scorers.get(s.worker.URL+"\x00"+m.VHost+"\x00"+m.Name, id, func(e *scorerEntry) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scorerLoadTimeout)
		defer cancel()
		e.scorer, e.err = loadScorer(ctx, s.worker, m)
	})
	select {
	case <-e.ready:
		return e.scorer, e.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// loadScorer fetches the version's signature and graph from the worker and
// checks the graph can be scored exactly.
func loadScorer(ctx context.Context, w *worker.Client, m storage.MLModel) (*onnxscore.Scorer, error) {
	vs, err := w.Versions(ctx, m.VHost, m.Name)
	if err != nil {
		return nil, fmt.Errorf("reading version %s: %w", m.RemoteVersion, err)
	}
	var v *worker.Version
	for i := range vs {
		if vs[i].Version == m.RemoteVersion {
			v = &vs[i]
		}
	}
	if v == nil {
		return nil, fmt.Errorf("%w: %q has no version %q on the worker", ErrVersionNotFound, m.Name, m.RemoteVersion)
	}
	raw, err := w.ModelFile(ctx, m.VHost, m.Name, m.RemoteVersion)
	if err != nil {
		return nil, fmt.Errorf("reading the graph of version %s: %w", m.RemoteVersion, err)
	}
	return onnxscore.NewScorer(raw, onnxscore.Signature{
		Task: v.Task, Features: v.Features, FeatureTypes: v.FeatureTypes, Fill: v.Fill, Labels: v.Labels,
	})
}

// scorerID is what an entry was loaded for.
type scorerID struct {
	version string
	saved   time.Time
}

type scorerEntry struct {
	id     scorerID
	ready  chan struct{}
	scorer *onnxscore.Scorer
	err    error
	done   time.Time
}

// lasting reports whether a failed load stays failed until the version
// changes: the graph is not supported, or the worker does not have it.
func (e *scorerEntry) lasting() bool {
	return errors.Is(e.err, onnxscore.ErrUnsupported) || errors.Is(e.err, worker.ErrNotFound) ||
		errors.Is(e.err, ErrVersionNotFound)
}

// scorerCache holds one entry per model. It is shared by every Service,
// which are built per use.
type scorerCache struct {
	mu      sync.Mutex
	entries map[string]*scorerEntry
}

var scorers = &scorerCache{entries: map[string]*scorerEntry{}}

// get returns the model's entry for id, starting load in the background of
// the first caller when there is none, or when the one there is for another
// version or failed for a reason that may since have passed.
func (c *scorerCache) get(key string, id scorerID, load func(*scorerEntry)) *scorerEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && e.id == id {
		select {
		case <-e.ready:
			if e.err == nil || e.lasting() || time.Since(e.done) < scorerRetryAfter {
				return e
			}
		default:
			return e // still loading
		}
	}
	if _, ok := c.entries[key]; !ok && len(c.entries) >= maxScorers {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	e := &scorerEntry{id: id, ready: make(chan struct{})}
	c.entries[key] = e
	go func() {
		defer func() {
			e.done = time.Now()
			close(e.ready)
		}()
		load(e)
	}()
	return e
}

// forget drops a model's entry, on any worker.
func (c *scorerCache) forget(vhost, name string) {
	suffix := "\x00" + vhost + "\x00" + name
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if len(k) >= len(suffix) && k[len(k)-len(suffix):] == suffix {
			delete(c.entries, k)
		}
	}
}
