package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/infra/evaluator"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// --- Multi-Source ---

type subSource struct {
	nodeID   string
	sourceID string
	source   hermod.Source
	running  bool
}

type multiSource struct {
	sources []*subSource
	msgChan chan hermod.Message
	errChan chan error
	mu      sync.Mutex
	closed  bool
}

func (m *multiSource) Read(ctx context.Context) (hermod.Message, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("multiSource closed")
	}

	for _, s := range m.sources {
		if !s.running {
			s.running = true
			go func(ss *subSource) {
				defer func() {
					m.mu.Lock()
					ss.running = false
					m.mu.Unlock()
				}()
				for {
					msg, err := ss.source.Read(ctx)
					if err != nil {
						if ctx.Err() == nil {
							select {
							case m.errChan <- err:
							case <-ctx.Done():
							}
						}
						return
					}
					if msg != nil {
						msg.SetMetadata("_source_node_id", ss.nodeID)
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
	}
	m.mu.Unlock()

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

// --- Workflow Node Execution ---

func (r *Registry) runWorkflowNode(workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	if msg == nil {
		return nil, "", nil
	}

	ctx, span := tracer.Start(context.Background(), "RunWorkflowNode", trace.WithAttributes(
		attribute.String("workflow_id", workflowID),
		attribute.String("node_id", node.ID),
		attribute.String("node_type", node.Type),
		attribute.String("message_id", msg.ID()),
	))
	defer span.End()

	// Broadcast live message for observability
	r.broadcastLiveMessageFromHermod(workflowID, node.ID, msg, false, "")

	if executor, ok := GetNodeExecutor(node.Type); ok {
		return executor.Execute(ctx, r, workflowID, node, msg)
	}

	// Default/Fallback behavior (merging, sink, source, etc.)
	return []hermod.Message{msg}, "", nil
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
