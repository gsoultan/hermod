package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/message"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
		return nil, fmt.Errorf("multiSource closed")
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
	return nil, fmt.Errorf("source does not support database discovery")
}

func (s *statefulSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if d, ok := s.Source.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, fmt.Errorf("source does not support table discovery")
}

func (s *statefulSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if d, ok := s.Source.(hermod.Sampler); ok {
		return d.Sample(ctx, table)
	}
	return nil, fmt.Errorf("source does not support sampling")
}

func (s *statefulSource) Snapshot(ctx context.Context, tables ...string) error {
	if sn, ok := s.Source.(hermod.Snapshottable); ok {
		return sn.Snapshot(ctx, tables...)
	}
	return fmt.Errorf("source %s does not support manual snapshots", s.sourceID)
}

func (s *statefulSource) IsReady(ctx context.Context) error {
	if r, ok := s.Source.(hermod.ReadyChecker); ok {
		return r.IsReady(ctx)
	}
	return s.Ping(ctx)
}

// --- Workflow Node Execution ---

func (r *Registry) runWorkflowNode(workflowID string, node *storage.WorkflowNode, msg hermod.Message) (hermod.Message, string, error) {
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

	switch node.Type {
	case "approval":
		return r.runApprovalNode(ctx, span, workflowID, node, msg)
	case "validator":
		return r.runValidatorNode(ctx, span, msg, node)
	case "transformation":
		return r.runTransformationNode(ctx, span, workflowID, node, msg)
	case "condition":
		return r.runConditionNode(ctx, span, msg, node)
	case "router":
		return r.runRouterNode(ctx, span, msg, node)
	case "switch":
		return r.runSwitchNode(ctx, span, msg, node)
	case "stateful":
		return r.runStatefulNode(ctx, span, workflowID, node, msg)
	case "merge":
		// Merge is largely handled by the router which combines messages
		return msg, "", nil
	case "sink", "source":
		return msg, "", nil
	default:
		return msg, "", nil
	}
}

func (r *Registry) runApprovalNode(ctx context.Context, span trace.Span, workflowID string, node *storage.WorkflowNode, msg hermod.Message) (hermod.Message, string, error) {
	app := storage.Approval{
		ID:         uuid.New().String(),
		WorkflowID: workflowID,
		NodeID:     node.ID,
		MessageID:  msg.ID(),
		Payload:    msg.Payload(),
		Metadata:   msg.Metadata(),
		Data:       msg.Data(),
		Status:     "pending",
		CreatedAt:  time.Now(),
	}
	if r.storage != nil {
		_ = r.storage.CreateApproval(ctx, app)
	}
	// Emit live event/log for visibility
	go r.broadcastLiveMessage(LiveMessage{
		WorkflowID: workflowID,
		NodeID:     node.ID,
		Timestamp:  time.Now(),
		Data:       map[string]any{"approval_id": app.ID, "status": app.Status},
	})
	r.broadcastLogWithData(workflowID, "INFO", fmt.Sprintf("Approval requested at node %s", r.getNodeName(*node)), msg.ID())
	// Halt the message until approved (no forward routing)
	return nil, "pending", nil
}

func (r *Registry) runValidatorNode(ctx context.Context, span trace.Span, msg hermod.Message, node *storage.WorkflowNode) (hermod.Message, string, error) {
	res, err := r.applyTransformation(ctx, msg, "validator", node.Config)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else if res == nil {
		span.SetAttributes(attribute.Bool("filtered", true))
	}
	return res, "", err
}

func (r *Registry) runTransformationNode(ctx context.Context, span trace.Span, workflowID string, node *storage.WorkflowNode, msg hermod.Message) (hermod.Message, string, error) {
	transType, _ := node.Config["transType"].(string)
	modifiedMsg := msg.Clone()

	if transType == "pipeline" {
		stepsStr, _ := node.Config["steps"].(string)
		var steps []map[string]any
		_ = json.Unmarshal([]byte(stepsStr), &steps)

		var err error
		for _, step := range steps {
			st, _ := step["transType"].(string)
			modifiedMsg, err = r.applyTransformation(ctx, modifiedMsg, st, step)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return nil, "", err
			}
			if modifiedMsg == nil {
				span.SetAttributes(attribute.Bool("filtered", true))
				return nil, "", nil // Filtered
			}
		}
		return modifiedMsg, "", nil
	}

	res, err := r.applyTransformation(ctx, modifiedMsg, transType, node.Config)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		r.broadcastLiveMessageFromHermod(workflowID, node.ID, msg, true, err.Error())
	} else if res == nil {
		span.SetAttributes(attribute.Bool("filtered", true))
	}
	return res, "", err
}

func (r *Registry) runConditionNode(ctx context.Context, span trace.Span, msg hermod.Message, node *storage.WorkflowNode) (hermod.Message, string, error) {
	conditionsStr, _ := node.Config["conditions"].(string)
	var conditions []map[string]any
	if conditionsStr != "" {
		_ = json.Unmarshal([]byte(conditionsStr), &conditions)
	}

	// Fallback to old format
	if len(conditions) == 0 {
		field, _ := node.Config["field"].(string)
		op, _ := node.Config["operator"].(string)
		val, _ := node.Config["value"].(string)
		if field != "" {
			conditions = append(conditions, map[string]any{
				"field":    field,
				"operator": op,
				"value":    val,
			})
		}
	}

	if r.evaluateConditions(msg, conditions) {
		span.SetAttributes(attribute.String("branch", "true"))
		return msg, "true", nil
	}
	span.SetAttributes(attribute.String("branch", "false"))
	return msg, "false", nil
}

func (r *Registry) runRouterNode(ctx context.Context, span trace.Span, msg hermod.Message, node *storage.WorkflowNode) (hermod.Message, string, error) {
	// rules is stored as a JSON array string in Config["rules"]
	rulesStr, _ := node.Config["rules"].(string)
	var rules []map[string]any
	_ = json.Unmarshal([]byte(rulesStr), &rules)

	for _, rule := range rules {
		label, _ := rule["label"].(string)

		// Rule can have multiple conditions
		var ruleConditions []map[string]any
		if condsRaw, ok := rule["conditions"].([]any); ok {
			for _, cr := range condsRaw {
				if condMap, ok := cr.(map[string]any); ok {
					ruleConditions = append(ruleConditions, condMap)
				}
			}
		}

		// If no conditions array, try single condition fields
		if len(ruleConditions) == 0 {
			field, _ := rule["field"].(string)
			op, _ := rule["operator"].(string)
			val := rule["value"]
			if field != "" && op != "" {
				ruleConditions = append(ruleConditions, map[string]any{
					"field":    field,
					"operator": op,
					"value":    val,
				})
			}
		}

		if len(ruleConditions) > 0 {
			if r.evaluateConditions(msg, ruleConditions) {
				span.SetAttributes(attribute.String("branch", label))
				return msg, label, nil
			}
		}
	}
	span.SetAttributes(attribute.String("branch", "default"))
	return msg, "default", nil
}

func (r *Registry) runSwitchNode(ctx context.Context, span trace.Span, msg hermod.Message, node *storage.WorkflowNode) (hermod.Message, string, error) {
	// cases is stored as a JSON array string in Config["cases"]
	casesStr, _ := node.Config["cases"].(string)
	var cases []map[string]any
	_ = json.Unmarshal([]byte(casesStr), &cases)

	field, _ := node.Config["field"].(string)
	fieldValStr := fmt.Sprintf("%v", getMsgValByPath(msg, field))

	for _, c := range cases {
		label, _ := c["label"].(string)

		// Check for conditions array in the case
		var caseConditions []map[string]any
		if condsRaw, ok := c["conditions"].([]any); ok {
			for _, cr := range condsRaw {
				if condMap, ok := cr.(map[string]any); ok {
					caseConditions = append(caseConditions, condMap)
				}
			}
		}

		if len(caseConditions) > 0 {
			if r.evaluateConditions(msg, caseConditions) {
				span.SetAttributes(attribute.String("branch", label))
				return msg, label, nil
			}
		} else {
			// Fallback to value comparison with the main field
			val, _ := c["value"].(string)
			if val == fieldValStr {
				span.SetAttributes(attribute.String("branch", label))
				return msg, label, nil
			}
		}
	}
	span.SetAttributes(attribute.String("branch", "default"))
	return msg, "default", nil
}

func (r *Registry) runStatefulNode(ctx context.Context, span trace.Span, workflowID string, node *storage.WorkflowNode, msg hermod.Message) (hermod.Message, string, error) {
	op, _ := node.Config["operation"].(string) // "count", "sum"
	field, _ := node.Config["field"].(string)
	outputField, _ := node.Config["outputField"].(string)
	if outputField == "" {
		outputField = field + "_" + op
	}

	key := workflowID + ":" + node.ID
	var currentVal float64

	if r.stateStore != nil {
		valBytes, err := r.stateStore.Get(ctx, "node:"+key)
		if err == nil && valBytes != nil {
			currentVal, _ = strconv.ParseFloat(string(valBytes), 64)
		}
	} else {
		r.nodeStatesMu.Lock()
		state, ok := r.nodeStates[key]
		if !ok {
			state = float64(0)
		}
		currentVal = state.(float64)
		r.nodeStatesMu.Unlock()
	}

	switch op {
	case "count":
		currentVal++
	case "sum":
		val := getMsgValByPath(msg, field)
		if v, ok := toFloat64(val); ok {
			currentVal += v
		}
	}

	if r.stateStore != nil {
		_ = r.stateStore.Set(ctx, "node:"+key, []byte(fmt.Sprintf("%f", currentVal)))
	} else {
		r.nodeStatesMu.Lock()
		r.nodeStates[key] = currentVal
		r.nodeStatesMu.Unlock()
	}

	span.SetAttributes(attribute.Float64("current_val", currentVal))

	modifiedMsg := msg.Clone()
	dm, isDefault := modifiedMsg.(*message.DefaultMessage)
	data := modifiedMsg.Data()

	if isDefault {
		dm.SetData(outputField, currentVal)
	} else {
		setValByPath(data, outputField, currentVal)
	}
	return modifiedMsg, "", nil
}

// --- Helper Functions ---

func toFloat64(val any) (float64, bool) {
	return evaluator.ToFloat64(val)
}

func toBool(val any) bool {
	return evaluator.ToBool(val)
}

func getValByPath(data map[string]any, path string) any {
	return evaluator.GetValByPath(data, path)
}

func getMsgValByPath(msg hermod.Message, path string) any {
	return evaluator.GetMsgValByPath(msg, path)
}

func setValByPath(data map[string]any, path string, val any) {
	evaluator.SetValByPath(data, path, val)
}

func (r *Registry) getValByPath(data map[string]any, path string) any {
	return evaluator.GetValByPath(data, path)
}

func (r *Registry) getMsgValByPath(msg hermod.Message, path string) any {
	return evaluator.GetMsgValByPath(msg, path)
}

func (r *Registry) toFloat64(val any) (float64, bool) {
	return evaluator.ToFloat64(val)
}

func (r *Registry) toBool(val any) bool {
	return evaluator.ToBool(val)
}

func (r *Registry) setValByPath(data map[string]any, path string, val any) {
	evaluator.SetValByPath(data, path, val)
}

func (r *Registry) resolveTemplate(temp string, data map[string]any) string {
	return evaluator.ResolveTemplate(temp, data)
}

func (r *Registry) evaluateConditions(msg hermod.Message, conditions []map[string]any) bool {
	return evaluator.EvaluateConditions(msg, conditions)
}

func (r *Registry) evaluateAdvancedExpression(msg hermod.Message, expr any) any {
	return r.evaluator.EvaluateAdvancedExpression(msg, expr)
}

func (r *Registry) parseAndEvaluate(msg hermod.Message, expr string) any {
	return r.evaluator.ParseAndEvaluate(msg, expr)
}

func (r *Registry) parseArgs(argsStr string) []string {
	var args []string
	var current strings.Builder
	parenCount := 0
	inQuotes := false
	var quoteChar byte

	for i := 0; i < len(argsStr); i++ {
		c := argsStr[i]
		if (c == '"' || c == '\'') && (i == 0 || argsStr[i-1] != '\\') {
			if !inQuotes {
				inQuotes = true
				quoteChar = c
			} else if c == quoteChar {
				inQuotes = false
			}
			current.WriteByte(c)
		} else if !inQuotes && c == '(' {
			parenCount++
			current.WriteByte(c)
		} else if !inQuotes && c == ')' {
			parenCount--
			current.WriteByte(c)
		} else if !inQuotes && parenCount == 0 && c == ',' {
			args = append(args, strings.TrimSpace(current.String()))
			current.Reset()
		} else {
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 || len(args) > 0 {
		args = append(args, strings.TrimSpace(current.String()))
	}
	return args
}

func (r *Registry) callFunction(name string, args []any) any {
	return r.evaluator.CallFunction(name, args)
}

func findNodeByID(nodes []storage.WorkflowNode, id string) *storage.WorkflowNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}
