package features

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("rolling", &Rolling{})
}

// Rolling writes features of a field over its recent values, per key.
//
// Config:
//   - field: the numeric field or expression. Optional when features is only
//     "count", which counts events.
//   - keyBy: the field or expression whose value names the key, e.g.
//     customer_id. Empty keeps one window for every record; a record without
//     the key shares the window of the empty key.
//   - windowType: "count" (default) keeps the last size events; "time" keeps
//     the events of the last window (a duration such as "5m"), at most
//     maxEvents (default 1000) of them.
//   - features: any of count, sum, mean, std, min, max (default all), each
//     written to prefix+feature. prefix defaults to <field>_, or "rolling_"
//     without a field. std is the population standard deviation.
//   - maxKeys: keys held in memory per node (default 10000). The key seen
//     least recently is dropped first.
//   - persistent: keep each key's window in the engine's state store too.
//   - onMissing: "fail" (default) or "skip" a record without a numeric value.
//     A skipped record is not added to the window and gets no features.
//
// The window includes the record being processed. Time is processing time:
// the moment the record reaches the node.
//
// What survives a restart is what aggregate's persistent mode keeps. Without
// persistent, windows live in this process's memory only: a restart, a
// workflow moving to another worker, or a key dropped by maxKeys starts that
// key's window empty again. With persistent, every record saves its key's
// window to the state store, and a key that is not in memory is read back on
// its next record; a time window read back drops what expired meanwhile. The
// window is saved after the record's features are computed, so a record
// redelivered after a crash is counted twice. Changing the field or the window
// starts every key afresh.
type Rolling struct {
	now     func() time.Time
	windows windowStore
}

// rollingFeatures is every feature, in the order they are written.
var rollingFeatures = []string{"count", "sum", "mean", "std", "min", "max"}

type rollingSpec struct {
	win      windowSpec
	features []string
	prefix   string
}

const rollingCacheKey = "_parsed_rolling"

func (r *Rolling) Prepare(config map[string]any) (map[string]any, error) {
	spec, err := parseRolling(config)
	if err != nil {
		return config, err
	}
	config[rollingCacheKey] = spec
	return config, nil
}

func (r *Rolling) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	spec, ok := config[rollingCacheKey].(*rollingSpec)
	if !ok {
		var err error
		if spec, err = parseRolling(config); err != nil {
			return msg, err
		}
	}
	var v float64
	if spec.win.field != "" {
		if v, ok = numberAt(msg, spec.win.field); !ok {
			if spec.win.skip {
				return msg, nil
			}
			return msg, missingErr("rolling", spec.win.field, msg)
		}
	}
	prior := r.windows.record(ctx, "rolling", &spec.win, msg, v, clockNow(r.now))
	// prior is the window before this record, which may already be full.
	prior = append(prior, v)
	s := summarize(prior[max(0, len(prior)-spec.win.size):])
	for _, f := range spec.features {
		msg.SetData(spec.prefix+f, s.get(f))
	}
	return msg, nil
}

func parseRolling(config map[string]any) (*rollingSpec, error) {
	win, err := parseWindow("rolling", config)
	if err != nil {
		return nil, err
	}
	spec := &rollingSpec{win: win}
	if spec.features, err = textList(config["features"]); err != nil {
		return nil, fmt.Errorf("rolling: features %w", err)
	}
	if len(spec.features) == 0 {
		spec.features = rollingFeatures
	}
	for _, f := range spec.features {
		if !slices.Contains(rollingFeatures, f) {
			return nil, fmt.Errorf("rolling: unknown feature %q; choose from count, sum, mean, std, min, max", f)
		}
		if f != "count" && win.field == "" {
			return nil, fmt.Errorf("rolling: %s needs a field to read", f)
		}
	}
	prefix := core.GetConfigString(config, "prefix")
	switch {
	case prefix != "":
		spec.prefix = prefix
	case win.field == "":
		spec.prefix = "rolling_"
	default:
		if spec.prefix, err = evaluator.OutputField(win.field, "", "_"); err != nil {
			return nil, fmt.Errorf("rolling: %w (set a prefix for the feature fields)", err)
		}
	}
	return spec, nil
}

// summary is the statistics of a window.
type summary struct {
	count, sum, mean, std, min, max float64
}

func summarize(values []float64) summary {
	s := summary{count: float64(len(values))}
	if len(values) == 0 {
		return s
	}
	s.min, s.max = values[0], values[0]
	for _, v := range values {
		s.sum += v
		s.min = min(s.min, v)
		s.max = max(s.max, v)
	}
	s.mean = s.sum / s.count
	// Two passes rather than a running sum of squares, which loses precision
	// when the values are large and close together.
	var ss float64
	for _, v := range values {
		ss += (v - s.mean) * (v - s.mean)
	}
	s.std = math.Sqrt(ss / s.count)
	return s
}

func (s summary) get(feature string) float64 {
	switch feature {
	case "count":
		return s.count
	case "sum":
		return s.sum
	case "mean":
		return s.mean
	case "std":
		return s.std
	case "min":
		return s.min
	default:
		return s.max
	}
}

func clockNow(now func() time.Time) time.Time {
	if now != nil {
		return now()
	}
	return time.Now()
}

// Bounds on what a node may ask to hold.
const (
	maxWindowEvents    = 100_000
	defaultTimeEvents  = 1000
	defaultMaxKeys     = 10_000
	maxKeysLimit       = 1_000_000
	windowTypeCount    = "count"
	windowTypeTime     = "time"
	workflowIDMetadata = "_hermod_workflow_id"
)

// windowSpec is the window part of a rolling or anomaly_score config.
type windowSpec struct {
	field, keyBy string
	byTime       bool
	span         time.Duration
	size         int // the last size events; for a time window, its cap
	maxKeys      int
	persistent   bool
	skip         bool
	// shape names the field and window, so a node whose field or window
	// changes does not read state kept under another.
	shape string
}

func parseWindow(node string, config map[string]any) (windowSpec, error) {
	w := windowSpec{
		field:      core.GetConfigString(config, "field"),
		keyBy:      core.GetConfigString(config, "keyBy"),
		persistent: evaluator.ToBool(config["persistent"]),
		maxKeys:    defaultMaxKeys,
	}
	var err error
	if w.skip, err = onMissingSkip(node, config); err != nil {
		return w, err
	}
	switch t := core.GetConfigString(config, "windowType"); t {
	case "", windowTypeCount:
		if w.size, err = boundedInt(config["size"], 1, maxWindowEvents); err != nil {
			return w, fmt.Errorf("%s: size, the number of events in the window, %w", node, err)
		}
		w.shape = fmt.Sprintf("%s:n%d", w.field, w.size)
	case windowTypeTime:
		if err = w.parseTime(node, config); err != nil {
			return w, err
		}
	default:
		return w, fmt.Errorf("%s: windowType must be %q or %q, not %q", node, windowTypeCount, windowTypeTime, t)
	}
	if v, ok := config["maxKeys"]; ok && v != nil && v != "" {
		if w.maxKeys, err = boundedInt(v, 1, maxKeysLimit); err != nil {
			return w, fmt.Errorf("%s: maxKeys %w", node, err)
		}
	}
	return w, nil
}

func (w *windowSpec) parseTime(node string, config map[string]any) error {
	w.byTime = true
	text := core.GetConfigString(config, "window")
	if text == "" {
		return fmt.Errorf("%s: a time window needs a window duration, e.g. 5m", node)
	}
	var err error
	if w.span, err = time.ParseDuration(text); err != nil || w.span <= 0 {
		return fmt.Errorf("%s: window must be a positive duration such as 30s, 5m or 1h, not %q", node, text)
	}
	w.size = defaultTimeEvents
	if v, ok := config["maxEvents"]; ok && v != nil && v != "" {
		if w.size, err = boundedInt(v, 1, maxWindowEvents); err != nil {
			return fmt.Errorf("%s: maxEvents %w", node, err)
		}
	}
	w.shape = fmt.Sprintf("%s:t%s", w.field, w.span)
	return nil
}

func boundedInt(v any, lo, hi int) (int, error) {
	n, err := wholeNumber(v)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("must be a whole number from %d to %d", lo, hi)
	}
	return n, nil
}

// sample is one value in a window, as it is saved.
type sample struct {
	At int64   `json:"t"` // Unix nanoseconds
	V  float64 `json:"v"`
}

// window is one key's recent values, and its place in its node's
// least-recently-seen order.
type window struct {
	key        string
	samples    []sample
	loaded     bool
	prev, next *window
}

// windowStore holds the windows of every node a transformer serves.
//
// Windows are found under mu, which is held only for the lookup. A window is
// read and changed under its key's stripe lock, taken for the whole
// read-change-save, so two records of one key are applied one after the other,
// and saved in that order. The lock is per key, not per window, which is what
// makes eviction safe with persistent: a window evicted while a record is
// being applied to it is saved before the key's next window is read back.
type windowStore struct {
	mu      sync.Mutex
	scopes  map[string]*keyedWindows
	stripes [64]sync.Mutex
}

// keyedWindows is one node's windows, the most recently seen at head.
type keyedWindows struct {
	byKey      map[string]*window
	head, tail *window
}

func (kw *keyedWindows) unlink(w *window) {
	if w.prev != nil {
		w.prev.next = w.next
	} else {
		kw.head = w.next
	}
	if w.next != nil {
		w.next.prev = w.prev
	} else {
		kw.tail = w.prev
	}
	w.prev, w.next = nil, nil
}

func (kw *keyedWindows) pushFront(w *window) {
	w.next = kw.head
	if kw.head != nil {
		kw.head.prev = w
	}
	kw.head = w
	if kw.tail == nil {
		kw.tail = w
	}
}

// get returns key's window in scope, creating it -- and evicting the least
// recently seen key past maxKeys -- when it is not held.
func (s *windowStore) get(scope, key string, maxKeys int) *window {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scopes == nil {
		s.scopes = map[string]*keyedWindows{}
	}
	kw := s.scopes[scope]
	if kw == nil {
		kw = &keyedWindows{byKey: map[string]*window{}}
		s.scopes[scope] = kw
	}
	if w, ok := kw.byKey[key]; ok {
		kw.unlink(w)
		kw.pushFront(w)
		return w
	}
	w := &window{key: key}
	kw.byKey[key] = w
	kw.pushFront(w)
	for len(kw.byKey) > maxKeys {
		oldest := kw.tail
		kw.unlink(oldest)
		delete(kw.byKey, oldest.key)
	}
	return w
}

// keys counts the windows held in memory.
func (s *windowStore) keys() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, kw := range s.scopes {
		n += len(kw.byKey)
	}
	return n
}

func (s *windowStore) stripe(key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &s.stripes[h.Sum32()%uint32(len(s.stripes))]
}

// record adds v at now to the window of msg's key, and returns the values the
// window held before it, oldest first. kind ("rolling", "anomaly") keeps two
// node types with the same settings apart in the state store.
func (s *windowStore) record(ctx context.Context, kind string, spec *windowSpec, msg hermod.Message, v float64, now time.Time) []float64 {
	workflowID, ok := hermod.MetadataValue(msg, workflowIDMetadata)
	if !ok || workflowID == "" {
		workflowID, _ = ctx.Value(hermod.WorkflowIDKey).(string)
	}
	nodeID, _ := ctx.Value(hermod.NodeIDKey).(string)
	key := ""
	if spec.keyBy != "" {
		key = text(evaluator.EvaluateField(msg, spec.keyBy))
	}
	scope := fmt.Sprintf("%s:%s:%s:%s", kind, workflowID, nodeID, spec.shape)
	stateKey := scope + ":" + key

	var store hermod.StateStore
	if spec.persistent {
		store, _ = ctx.Value(hermod.StateStoreKey).(hermod.StateStore)
	}

	mu := s.stripe(stateKey)
	mu.Lock()
	defer mu.Unlock()
	w := s.get(scope, key, spec.maxKeys)
	if !w.loaded {
		w.loaded = true
		load(ctx, kind, store, stateKey, w)
	}

	if spec.byTime {
		cutoff := now.Add(-spec.span).UnixNano()
		drop := 0
		for drop < len(w.samples) && w.samples[drop].At <= cutoff {
			drop++
		}
		w.samples = w.samples[drop:]
	}
	prior := make([]float64, len(w.samples), len(w.samples)+1)
	for i, smp := range w.samples {
		prior[i] = smp.V
	}

	w.samples = append(w.samples, sample{At: now.UnixNano(), V: v})
	if over := len(w.samples) - spec.size; over > 0 {
		// Copied down rather than resliced, so the dropped samples' backing
		// array does not grow without bound.
		w.samples = append(w.samples[:0], w.samples[over:]...)
	}
	save(ctx, kind, store, stateKey, w)
	return prior
}

// load reads a window saved by an earlier run. A store that fails, or holds
// something unreadable, starts the key empty: the record still gets features,
// from less history.
func load(ctx context.Context, kind string, store hermod.StateStore, stateKey string, w *window) {
	if store == nil {
		return
	}
	data, err := store.Get(ctx, stateKey)
	if err == nil && len(data) > 0 {
		if err = json.Unmarshal(data, &w.samples); err != nil {
			w.samples = nil
		}
	}
	if err != nil {
		warn(ctx, kind+": could not read a saved window; the key starts empty", "key", w.key, "error", err)
	}
}

// save writes the window for the next run. A failure keeps the record and the
// window in memory, both right; what is lost is the window after a restart,
// so it is logged.
func save(ctx context.Context, kind string, store hermod.StateStore, stateKey string, w *window) {
	if store == nil {
		return
	}
	data, err := json.Marshal(w.samples)
	if err == nil {
		err = store.Set(ctx, stateKey, data)
	}
	if err != nil {
		warn(ctx, kind+": could not save a window; after a restart this key's window will be stale", "key", w.key, "error", err)
	}
}

// warn logs through the engine's logger when the context carries one. The
// registry in the context offers it; a test or an embedder without one is
// silent.
func warn(ctx context.Context, msg string, keysAndValues ...any) {
	provider, ok := ctx.Value(hermod.RegistryKey).(interface{ Logger() hermod.Logger })
	if !ok {
		return
	}
	if logger := provider.Logger(); logger != nil {
		nodeID, _ := ctx.Value(hermod.NodeIDKey).(string)
		logger.Warn(msg, append(keysAndValues, "node_id", nodeID)...)
	}
}
