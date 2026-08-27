package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/engine/telemetry"
	"github.com/user/hermod/pkg/infra/evaluator"
	"github.com/user/hermod/pkg/infra/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// --- Multi-Source ---

type subSource struct {
	nodeID   string
	sourceID string
	source   hermod.Source
	running  bool

	// failures counts consecutive read failures and retryAt is when this
	// sub-source may next be respawned. A reader goroutine exits on error and is
	// respawned lazily by the next multiSource.Read; without a backoff that
	// makes a permanently dead source retry once per Read — measured at ~60k
	// attempts/second — which burns a core for as long as the workflow runs.
	// Both fields are guarded by multiSource.mu.
	failures int
	retryAt  time.Time
}

// subSourceRetryBase and subSourceRetryMax bound the per-source backoff. The
// floor keeps a briefly-flapping source rejoining quickly; the ceiling keeps a
// long-dead one cheap. This is deliberately separate from the engine-wide
// reconnect backoff in runner.go: that one pauses the whole workflow, while
// this one only holds back the source that is actually failing.
const (
	subSourceRetryBase = 100 * time.Millisecond
	subSourceRetryMax  = 5 * time.Second
)

// retryDelay returns the backoff for the current consecutive-failure count,
// doubling from the base up to the cap.
func retryDelay(failures int) time.Duration {
	if failures < 1 {
		return subSourceRetryBase
	}
	d := subSourceRetryBase
	for range failures - 1 {
		d *= 2
		if d >= subSourceRetryMax {
			return subSourceRetryMax
		}
	}
	return d
}

type multiSource struct {
	sources []*subSource
	msgChan chan hermod.Message
	errChan chan error
	mu      sync.Mutex
	closed  bool

	// workflowID labels the per-source backoff metric. It is set at build time
	// and never mutated, so it needs no lock.
	workflowID string
}

func (m *multiSource) Read(ctx context.Context) (hermod.Message, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("multiSource closed")
	}

	now := time.Now()
	// If every source ends up backing off, nothing will produce and nothing will
	// wake this call: the select below would block until the workflow is
	// cancelled. Track when the earliest one becomes eligible so the wait can be
	// bounded by that instead.
	anyRunning := false
	var nextRetry time.Time
	for _, s := range m.sources {
		if !s.running && !now.Before(s.retryAt) {
			s.running = true
			go func(ss *subSource) {
				defer func() {
					m.mu.Lock()
					ss.running = false
					m.mu.Unlock()
				}()
				for {
					// A source is allowed to return (nil, nil) meaning "nothing
					// right now" — runner.go handles exactly that. The loop below
					// only returns on a read error, or when *sending a message*
					// loses the race to ctx.Done(); a source that keeps yielding
					// nothing hits neither, so this goroutine outlived every
					// cancellation and spun for the life of the process. That is
					// one leaked, CPU-burning goroutine per source per workflow
					// stop, which is what a restart loop accumulates.
					if ctx.Err() != nil {
						return
					}
					msg, err := ss.source.Read(ctx)
					if err != nil {
						// Hold this source back before it is respawned. Only the
						// failing source is delayed; its healthy peers keep
						// streaming through the same multiplexer.
						m.mu.Lock()
						ss.failures++
						ss.retryAt = time.Now().Add(retryDelay(ss.failures))
						m.mu.Unlock()
						// A source stuck in backoff delivers nothing while the
						// workflow still reports healthy, because its siblings
						// are fine. Without this counter that is invisible.
						telemetry.SubSourceBackoff.WithLabelValues(m.workflowID, ss.sourceID).Inc()
						if ctx.Err() == nil {
							select {
							case m.errChan <- err:
							case <-ctx.Done():
							}
						}
						return
					}
					if msg != nil {
						// A successful read clears the backoff, so a source that
						// flaps once is not penalised for the rest of its life.
						m.mu.Lock()
						ss.failures = 0
						ss.retryAt = time.Time{}
						m.mu.Unlock()
						msg.SetMetadata("_source_node_id", ss.nodeID)

						// Where a trace begins for the workflow path. Started and
						// ended around the handover rather than around the Read
						// above, which blocks until something arrives — a quiet
						// source would otherwise report its idle time as work. The
						// context cannot be passed on, because the nodes that
						// process this message run on other goroutines, so it
						// travels on the message and is picked up in
						// RunWorkflowNode.
						readCtx, span := tracer.Start(ctx, "source.receive", trace.WithAttributes(
							attribute.String("workflow_id", m.workflowID),
							attribute.String("source_id", ss.sourceID),
							attribute.String("node_id", ss.nodeID),
							attribute.String("message_id", msg.ID()),
							attribute.String("table", msg.Table()),
						))
						tracing.Inject(readCtx, msg)
						span.End()

						// Remember the latest record this source actually
						// forwarded downstream so passive sampling can surface
						// real data even when the live consumer drains the
						// source (see lastDeliveredSamples).
						recordDeliveredSample(ss.sourceID, msg)
						select {
						case m.msgChan <- msg:
						case <-ctx.Done():
							return
						}
					}
				}
			}(s)
		}
		if s.running {
			anyRunning = true
		} else if nextRetry.IsZero() || s.retryAt.Before(nextRetry) {
			nextRetry = s.retryAt
		}
	}
	m.mu.Unlock()

	if !anyRunning && !nextRetry.IsZero() {
		wait := max(time.Until(nextRetry), 0)
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-m.errChan:
			return nil, err
		case msg := <-m.msgChan:
			return msg, nil
		case <-timer.C:
			// A source is eligible again. Return the sentinel so the engine's
			// read loop calls back in and respawns it, rather than reporting a
			// failure that has not happened.
			return nil, nil
		}
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-m.errChan:
		return nil, err
	case msg := <-m.msgChan:
		return msg, nil
	}
}

func (m *multiSource) Ack(ctx context.Context, msg hermod.Message) error {
	nodeID := msg.Metadata()["_source_node_id"]
	for _, s := range m.sources {
		if s.nodeID == nodeID {
			return s.source.Ack(ctx, msg)
		}
	}
	return nil
}

func (m *multiSource) GetState() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()

	combined := make(map[string]string)
	for _, s := range m.sources {
		if st, ok := s.source.(hermod.Stateful); ok {
			state := st.GetState()
			for k, v := range state {
				combined[s.nodeID+":"+k] = v
			}
		}
	}
	return combined
}

func (m *multiSource) SetState(state map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	perSource := make(map[string]map[string]string)
	for key, val := range state {
		for _, s := range m.sources {
			prefix := s.nodeID + ":"
			if len(key) > len(prefix) && key[:len(prefix)] == prefix {
				realKey := key[len(prefix):]
				if perSource[s.nodeID] == nil {
					perSource[s.nodeID] = make(map[string]string)
				}
				perSource[s.nodeID][realKey] = val
			}
		}
	}
	for _, s := range m.sources {
		if st, ok := s.source.(hermod.Stateful); ok {
			if ps, found := perSource[s.nodeID]; found {
				st.SetState(ps)
			}
		}
	}
}

func (m *multiSource) Ping(ctx context.Context) error {
	for _, s := range m.sources {
		if err := s.source.Ping(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (m *multiSource) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}
	m.closed = true
	var lastErr error
	for _, s := range m.sources {
		if err := s.source.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// --- Stateful Source Wrapper ---

type statefulSource struct {
	hermod.Source
	registry *Registry
	sourceID string
}

func (s *statefulSource) Ack(ctx context.Context, msg hermod.Message) error {
	if err := s.Source.Ack(ctx, msg); err != nil {
		return err
	}
	if st, ok := s.Source.(hermod.Stateful); ok {
		state := st.GetState()
		if len(state) > 0 && s.registry != nil && s.registry.storage != nil {
			if err := s.registry.storage.UpdateSourceState(ctx, s.sourceID, state); err != nil {
				return fmt.Errorf("failed to persist source state for %s: %w", s.sourceID, err)
			}
		}
	}
	return nil
}

func (s *statefulSource) GetState() map[string]string {
	if st, ok := s.Source.(hermod.Stateful); ok {
		return st.GetState()
	}
	return nil
}

func (s *statefulSource) SetState(state map[string]string) {
	if st, ok := s.Source.(hermod.Stateful); ok {
		st.SetState(state)
	}
}

func (s *statefulSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if d, ok := s.Source.(hermod.Discoverer); ok {
		return d.DiscoverDatabases(ctx)
	}
	return nil, errors.New("source does not support database discovery")
}

func (s *statefulSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if d, ok := s.Source.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, errors.New("source does not support table discovery")
}

func (s *statefulSource) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if d, ok := s.Source.(hermod.ColumnDiscoverer); ok {
		return d.DiscoverColumns(ctx, table)
	}
	return nil, errors.New("source does not support column discovery")
}

func (s *statefulSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if d, ok := s.Source.(hermod.Sampler); ok {
		return d.Sample(ctx, table)
	}
	return nil, errors.New("source does not support sampling")
}

func (s *statefulSource) Snapshot(ctx context.Context, tables ...string) error {
	if sn, ok := s.Source.(hermod.Snapshottable); ok {
		return sn.Snapshot(ctx, tables...)
	}
	return fmt.Errorf("source %s does not support manual snapshots", s.sourceID)
}

func (s *statefulSource) ExecuteSQL(ctx context.Context, query string) ([]map[string]any, error) {
	if se, ok := s.Source.(hermod.SQLExecutor); ok {
		return se.ExecuteSQL(ctx, query)
	}
	return nil, fmt.Errorf("%w: source does not support SQL execution", hermod.ErrNotSupported)
}

func (s *statefulSource) SetLogger(logger hermod.Logger) {
	if l, ok := s.Source.(hermod.Loggable); ok {
		l.SetLogger(logger)
	}
}

func (s *statefulSource) IsReady(ctx context.Context) error {
	if r, ok := s.Source.(hermod.ReadyChecker); ok {
		return r.IsReady(ctx)
	}
	return s.Ping(ctx)
}

// The forwards below exist because statefulSource embeds the hermod.Source
// interface, and embedding an interface promotes only the methods that
// interface declares. Every optional capability of the wrapped source —
// lag, outstanding work, stream liveness — is therefore invisible through this
// wrapper unless it is forwarded by hand.
//
// This is not hypothetical: GetLag was dropped here, so multiSource.GetLag
// found no LagReporter, and the engine read zero lag for every CDC workflow.
// Workflow status, WAL-retention alerting and the stall watchdog's lag fallback
// were all reading a constant zero. TestSourceWrappersForwardOptionalInterfaces
// exists to fail the moment a wrapper or an interface is added without a
// matching forward.

// GetLag forwards replication lag from the wrapped source.
func (s *statefulSource) GetLag(ctx context.Context) (uint64, error) {
	if lr, ok := s.Source.(hermod.LagReporter); ok {
		return lr.GetLag(ctx)
	}
	return 0, nil
}

// PendingWork forwards the wrapped source's outstanding-work signal.
func (s *statefulSource) PendingWork() (pending bool, known bool) {
	if pw, ok := s.Source.(hermod.PendingWorkReporter); ok {
		return pw.PendingWork()
	}
	return false, false
}

// LastStreamActivity forwards the wrapped source's stream liveness clock.
func (s *statefulSource) LastStreamActivity() time.Time {
	if lr, ok := s.Source.(hermod.StreamLivenessReporter); ok {
		return lr.LastStreamActivity()
	}
	return time.Time{}
}

// StreamSilenceThreshold forwards the wrapped source's silence deadline.
func (s *statefulSource) StreamSilenceThreshold() time.Duration {
	if lr, ok := s.Source.(hermod.StreamLivenessReporter); ok {
		return lr.StreamSilenceThreshold()
	}
	return 0
}

// --- Workflow Node Execution ---

func (r *Registry) RunWorkflowNode(workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	if msg == nil {
		return nil, "", nil
	}

	ctx := context.WithValue(context.Background(), hermod.RegistryKey, r)
	// Background is a fresh root, so without this every node was its own
	// trace and a message passing through five nodes produced five unrelated
	// traces. The link comes off the message because the node runs on a
	// different goroutine from the read that produced it.
	ctx = tracing.Extract(ctx, msg)
	ctx, span := tracer.Start(ctx, "RunWorkflowNode", trace.WithAttributes(
		attribute.String("workflow_id", workflowID),
		attribute.String("node_id", node.ID),
		attribute.String("node_type", node.Type),
		attribute.String("message_id", msg.ID()),
	))
	defer span.End()

	// Broadcast live message for observability
	r.broadcastLiveMessageFromHermod(workflowID, node.ID, msg, false, "")

	msgs, branch, err := func() ([]hermod.Message, string, error) {
		if executor, ok := interfaces.GetNodeExecutor(node.Type); ok {
			return executor.Execute(ctx, r, workflowID, node, msg)
		}
		// Default/Fallback behavior (merging, sink, source, etc.)
		return []hermod.Message{msg}, "", nil
	}()

	// Ensure all messages returned are owned by the caller (they should each have
	// exactly one reference for the caller to manage). If we are returning the
	// input message (which is owned by our caller), we must increment its
	// reference count so that our caller can safely release the result later
	// without impacting the input's original reference count.
	for _, m := range msgs {
		if m == msg {
			m.Retain()
		}
	}

	return msgs, branch, err
}

// --- Helper Functions ---

func getValByPath(data map[string]any, path string) any {
	return evaluator.GetValByPath(data, path)
}

func setValByPath(data map[string]any, path string, val any) {
	evaluator.SetValByPath(data, path, val)
}

func (r *Registry) evaluateConditions(msg hermod.Message, conditions []map[string]any) bool {
	return evaluator.EvaluateConditions(msg, conditions)
}

func (r *Registry) evaluateAdvancedExpression(msg hermod.Message, expr any) any {
	return r.evaluator.EvaluateAdvancedExpression(msg, expr)
}

func findNodeByID(nodes []storage.WorkflowNode, id string) *storage.WorkflowNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

// GetLag reports the largest lag among the sources feeding this workflow.
//
// A workflow can read from several sources; it is behind by as much as its
// worst one. Without this the multiplexer hid every source's lag from the
// engine, so status, retention alerting and stall detection all saw zero.
func (m *multiSource) GetLag(ctx context.Context) (uint64, error) {
	var worst uint64
	for _, s := range m.sources {
		if s == nil || s.source == nil {
			continue
		}
		lr, ok := s.source.(hermod.LagReporter)
		if !ok {
			continue
		}
		if lag, err := lr.GetLag(ctx); err == nil && lag > worst {
			worst = lag
		}
	}
	return worst, nil
}

// PendingWork reports whether any source feeding this workflow is still owed
// acknowledgements. known is false only when no source can answer at all, so a
// multiplexer over sources that cannot tell leaves the engine on its own
// fallback rather than asserting that nothing is outstanding.
func (m *multiSource) PendingWork() (pending bool, known bool) {
	for _, s := range m.sources {
		if s == nil || s.source == nil {
			continue
		}
		pw, ok := s.source.(hermod.PendingWorkReporter)
		if !ok {
			continue
		}
		srcPending, srcKnown := pw.PendingWork()
		if !srcKnown {
			continue
		}
		known = true
		if srcPending {
			return true, true
		}
	}
	return false, known
}

// LastStreamActivity reports the least recent activity across the sources that
// hold a server-pushed stream, so the quietest stream is the one judged.
//
// Sources without a stream (a polling SQL source, say) report the zero time and
// are skipped: they have no cadence to be measured against, and including them
// would make every mixed workflow look permanently wedged.
func (m *multiSource) LastStreamActivity() time.Time {
	var oldest time.Time
	for _, s := range m.sources {
		if s == nil || s.source == nil {
			continue
		}
		lr, ok := s.source.(hermod.StreamLivenessReporter)
		if !ok || lr.StreamSilenceThreshold() <= 0 {
			continue
		}
		last := lr.LastStreamActivity()
		if last.IsZero() {
			continue
		}
		if oldest.IsZero() || last.Before(oldest) {
			oldest = last
		}
	}
	return oldest
}

// StreamSilenceThreshold reports the most generous deadline among the sources
// that hold a stream.
//
// Pairing the least recent activity with the largest threshold is deliberately
// conservative: with sources on servers configured differently, this can be slow
// to notice one of them going quiet, but it can never declare a healthy stream
// dead. Under-reporting is recoverable — the progress watchdog is still
// watching — while a spurious restart of a working pipeline is not.
func (m *multiSource) StreamSilenceThreshold() time.Duration {
	var widest time.Duration
	for _, s := range m.sources {
		if s == nil || s.source == nil {
			continue
		}
		lr, ok := s.source.(hermod.StreamLivenessReporter)
		if !ok {
			continue
		}
		if th := lr.StreamSilenceThreshold(); th > widest {
			widest = th
		}
	}
	return widest
}
