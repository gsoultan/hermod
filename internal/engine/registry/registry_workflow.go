package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/buffer"
	"github.com/user/hermod/pkg/comm/message"
	pkgengine "github.com/user/hermod/pkg/engine"
	"github.com/user/hermod/pkg/engine/config"
	"github.com/user/hermod/pkg/engine/telemetry"
	"github.com/user/hermod/pkg/infra/compression"
	"github.com/user/hermod/pkg/infra/schema"
)

// --- StartWorkflow sub-functions ---

// buildWorkflowSources finds source nodes, creates sources, and returns configs and multiSource.
func (r *Registry) buildWorkflowSources(ctx context.Context, wf storage.Workflow) ([]factory.SourceConfig, *multiSource, error) {
	var sourceNodes []*storage.WorkflowNode
	for i, node := range wf.Nodes {
		if node.Type == "source" {
			sourceNodes = append(sourceNodes, &wf.Nodes[i])
		}
	}
	if len(sourceNodes) == 0 {
		return nil, nil, errors.New("workflow must have at least one source node")
	}

	var srcConfigs []factory.SourceConfig
	var subSources []*subSource
	for _, sn := range sourceNodes {
		dbSrc, err := r.storage.GetSource(ctx, sn.RefID)
		if err != nil {
			for _, ss := range subSources {
				ss.source.Close()
			}
			return nil, nil, fmt.Errorf("failed to get source %s: %w", sn.RefID, err)
		}

		srcCfg := factory.SourceConfig{
			ID:     dbSrc.ID,
			Type:   dbSrc.Type,
			Config: dbSrc.Config,
			State:  dbSrc.State,
		}

		if val, ok := dbSrc.Config["reconnect_intervals"]; ok && val != "" {
			parts := strings.Split(val, ",")
			var intervals []time.Duration
			for _, p := range parts {
				if d, err := parseDuration(strings.TrimSpace(p)); err == nil {
					intervals = append(intervals, d)
				}
			}
			if len(intervals) > 0 {
				srcCfg.ReconnectIntervals = intervals
			}
		} else if val, ok := dbSrc.Config["reconnect_interval"]; ok && val != "" {
			if d, err := parseDuration(val); err == nil {
				srcCfg.ReconnectIntervals = []time.Duration{d}
			}
		}

		srcConfigs = append(srcConfigs, srcCfg)

		src, err := r.createSourceInternal(ctx, srcCfg)
		if err != nil {
			for _, ss := range subSources {
				ss.source.Close()
			}
			return nil, nil, err
		}
		subSources = append(subSources, &subSource{nodeID: sn.ID, sourceID: sn.RefID, source: src})
	}

	ms := &multiSource{
		sources: subSources,
		msgChan: make(chan hermod.Message, 100),
		errChan: make(chan error, len(subSources)),
	}
	return srcConfigs, ms, nil
}

// discoverWorkflowSinks uses BFS from source nodes to find and create all sinks.
func (r *Registry) discoverWorkflowSinks(ctx context.Context, wf storage.Workflow, ms *multiSource) ([]hermod.Sink, []factory.SinkConfig, map[string]int, error) {
	adj := make(map[string][]string)
	for _, edge := range wf.Edges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge.TargetID)
	}

	var sinks []hermod.Sink
	var snkConfigs []factory.SinkConfig
	sinkNodeToIndex := make(map[string]int)

	queue := []string{}
	visited := make(map[string]bool)
	for _, node := range wf.Nodes {
		if node.Type == "source" {
			queue = append(queue, node.ID)
			visited[node.ID] = true
		}
	}

	for len(queue) > 0 {
		currID := queue[0]
		queue = queue[1:]

		node := findNodeByID(wf.Nodes, currID)
		if node == nil {
			continue
		}

		if node.Type == "sink" {
			dbSnk, err := r.storage.GetSink(context.Background(), node.RefID)
			if err != nil {
				for _, s := range sinks {
					s.Close()
				}
				ms.Close()
				return nil, nil, nil, fmt.Errorf("failed to get sink %s: %w", node.RefID, err)
			}
			snkCfg := factory.SinkConfig{
				ID:     dbSnk.ID,
				Type:   dbSnk.Type,
				Config: dbSnk.Config,
			}
			snk, err := r.createSinkInternal(ctx, snkCfg)
			if err != nil {
				for _, s := range sinks {
					s.Close()
				}
				ms.Close()
				return nil, nil, nil, err
			}
			sinkNodeToIndex[node.ID] = len(sinks)
			sinks = append(sinks, snk)
			snkConfigs = append(snkConfigs, snkCfg)
		}

		for _, nextID := range adj[currID] {
			if !visited[nextID] {
				visited[nextID] = true
				queue = append(queue, nextID)
			}
		}
	}

	if len(sinks) == 0 {
		ms.Close()
		return nil, nil, nil, errors.New("workflow must have at least one sink node reachable from sources")
	}
	return sinks, snkConfigs, sinkNodeToIndex, nil
}

// defaultRingBufferCap is the default in-memory ring buffer capacity. It favors
// a small footprint suitable for the lightweight, low-memory target and can be
// raised via HERMOD_BUFFER_RING_CAP for high-throughput deployments.
const defaultRingBufferCap = 256

// ringBufferCap returns the configured ring buffer capacity, falling back to
// defaultRingBufferCap when HERMOD_BUFFER_RING_CAP is unset or invalid.
func ringBufferCap() int {
	if v := strings.TrimSpace(os.Getenv("HERMOD_BUFFER_RING_CAP")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultRingBufferCap
}

// createWorkflowBuffer selects the appropriate buffer based on environment variables.
func createWorkflowBuffer() hermod.Producer {
	bufType := strings.ToLower(strings.TrimSpace(os.Getenv("HERMOD_BUFFER_TYPE")))
	switch bufType {
	case "combined_buffer", "combined":
		ringCap := ringBufferCap()
		fileDir := strings.TrimSpace(os.Getenv("HERMOD_BUFFER_DIR"))
		fileSize := 0
		if v := strings.TrimSpace(os.Getenv("HERMOD_FILEBUFFER_SIZE")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				fileSize = n
			}
		}

		compAlgo := compression.Algorithm(strings.ToLower(strings.TrimSpace(os.Getenv("HERMOD_BUFFER_COMPRESSION"))))
		compressor, _ := compression.NewCompressor(compAlgo)

		cb, err := buffer.NewCombinedBuffer(ringCap, fileDir, fileSize, &buffer.CombinedOptions{
			Compressor: compressor,
		})
		if err != nil {
			log.Printf("Registry: failed to create CombinedBuffer, falling back to ring: %v", err)
			return buffer.NewRingBuffer(ringCap)
		}
		return cb
	case "file_buffer", "file":
		fileDir := strings.TrimSpace(os.Getenv("HERMOD_BUFFER_DIR"))
		if fileDir == "" {
			fileDir = ".hermod-buffer"
		}
		fileSize := 0
		if v := strings.TrimSpace(os.Getenv("HERMOD_FILEBUFFER_SIZE")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				fileSize = n
			}
		}

		compAlgo := compression.Algorithm(strings.ToLower(strings.TrimSpace(os.Getenv("HERMOD_BUFFER_COMPRESSION"))))
		compressor, _ := compression.NewCompressor(compAlgo)

		fb, err := buffer.NewFileBufferWithCompressor(fileDir, fileSize, compressor)
		if err != nil {
			log.Printf("Registry: failed to create FileBuffer, falling back to ring: %v", err)
			return buffer.NewRingBuffer(ringBufferCap())
		}
		return fb
	default:
		return buffer.NewRingBuffer(ringBufferCap())
	}
}

// buildSinkEngineConfigs maps internal factory.SinkConfigs to config.SinkConfig slice.
func buildSinkEngineConfigs(snkConfigs []factory.SinkConfig) ([]string, []string, []config.SinkConfig) {
	sinkIDs := make([]string, len(snkConfigs))
	sinkTypes := make([]string, len(snkConfigs))
	pkgSnkConfigs := make([]config.SinkConfig, len(snkConfigs))

	for i, cfg := range snkConfigs {
		sinkIDs[i] = cfg.ID
		sinkTypes[i] = cfg.Type
		psc := parseSinkEngineConfig(cfg)
		pkgSnkConfigs[i] = psc
	}
	return sinkIDs, sinkTypes, pkgSnkConfigs
}

func parseSinkEngineConfig(cfg factory.SinkConfig) config.SinkConfig {
	psc := config.SinkConfig{}
	if val, ok := cfg.Config["max_retries"]; ok && val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			psc.MaxRetries = n
		}
	}
	if val, ok := cfg.Config["retry_interval"]; ok && val != "" {
		if d, err := parseDuration(val); err == nil {
			psc.RetryInterval = d
		}
	}
	if val, ok := cfg.Config["batch_size"]; ok && val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			psc.BatchSize = n
		}
	}
	if val, ok := cfg.Config["batch_timeout"]; ok && val != "" {
		if d, err := parseDuration(val); err == nil {
			psc.BatchTimeout = d
		}
	}
	if val, ok := cfg.Config["batch_bytes"]; ok && val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			psc.BatchBytes = n
		}
	}
	if val, ok := cfg.Config["shard_count"]; ok && val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			psc.ShardCount = n
		}
	}
	if val, ok := cfg.Config["shard_key_meta"]; ok && val != "" {
		psc.ShardKeyMeta = val
	}
	if val, ok := cfg.Config["circuit_threshold"]; ok && val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			psc.CircuitBreakerThreshold = n
		}
	}
	if val, ok := cfg.Config["circuit_interval"]; ok && val != "" {
		if d, err := parseDuration(val); err == nil {
			psc.CircuitBreakerInterval = d
		}
	}
	if val, ok := cfg.Config["circuit_cool_off"]; ok && val != "" {
		if d, err := parseDuration(val); err == nil {
			psc.CircuitBreakerCoolDown = d
		}
	}
	if val, ok := cfg.Config["retry_intervals"]; ok && val != "" {
		parts := strings.Split(val, ",")
		for _, p := range parts {
			if d, err := parseDuration(strings.TrimSpace(p)); err == nil {
				psc.RetryIntervals = append(psc.RetryIntervals, d)
			}
		}
	}
	if val, ok := cfg.Config["backpressure_strategy"]; ok && val != "" {
		psc.BackpressureStrategy = config.BackpressureStrategy(val)
	}
	if val, ok := cfg.Config["backpressure_buffer"]; ok && val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			psc.BackpressureBuffer = n
		}
	}
	if val, ok := cfg.Config["sampling_rate"]; ok && val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			psc.SamplingRate = f
		}
	}
	if val, ok := cfg.Config["spill_path"]; ok && val != "" {
		psc.SpillPath = val
	}
	if val, ok := cfg.Config["spill_max_size"]; ok && val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			psc.SpillMaxSize = n
		}
	}
	return psc
}

// StartWorkflow creates and starts a workflow engine for the given workflow configuration.
func (r *Registry) StartWorkflow(id string, wf storage.Workflow) error {
	if r.ctx.Err() != nil {
		return errors.New("registry is closing, cannot start new workflow")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.engines[id]; ok {
		return fmt.Errorf("workflow %s already running", id)
	}

	ctx := context.Background()
	if r.storage == nil {
		return fmt.Errorf("registry storage is not initialized, cannot start workflow %s", id)
	}
	if err := r.ValidateWorkflow(ctx, wf); err != nil {
		return fmt.Errorf("workflow validation failed: %w", err)
	}

	// Load node states for stateful transformations
	nodeStates, err := r.storage.GetNodeStates(ctx, id)
	if err == nil {
		r.nodeStatesMu.Lock()
		for nodeID, state := range nodeStates {
			r.nodeStates[id+":"+nodeID] = state
		}
		r.nodeStatesMu.Unlock()
	}

	// 1. Build sources
	srcConfigs, ms, err := r.buildWorkflowSources(ctx, wf)
	if err != nil {
		return err
	}

	// 2. Discover and create sinks
	sinks, snkConfigs, sinkNodeToIndex, err := r.discoverWorkflowSinks(ctx, wf, ms)
	if err != nil {
		return err
	}

	// 3. Create buffer
	buf := createWorkflowBuffer()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	eng := pkgengine.NewEngine(ms, sinks, buf)
	eng.SetConfig(r.config)

	// Apply workflow level overrides
	engCfg := r.config
	if wf.MaxRetries > 0 {
		engCfg.MaxRetries = wf.MaxRetries
	}
	if wf.RetryInterval != "" {
		if d, err := parseDuration(wf.RetryInterval); err == nil {
			engCfg.RetryInterval = d
		}
	}
	if wf.ReconnectInterval != "" {
		if d, err := parseDuration(wf.ReconnectInterval); err == nil {
			engCfg.ReconnectInterval = d
		}
	}
	engCfg.PrioritizeDLQ = wf.PrioritizeDLQ
	engCfg.DryRun = wf.DryRun
	// Apply the per-workflow trace sample rate BEFORE SetConfig: SetConfig copies
	// the config by value into the engine, so any field set on engCfg afterwards
	// would never reach the engine and per-workflow tracing would be silently
	// ignored (the engine's RecordTraceStep early-returns when the rate is <= 0).
	engCfg.TraceSampleRate = wf.TraceSampleRate
	eng.SetConfig(engCfg)

	// Set Dead Letter Sink if configured
	if wf.DeadLetterSinkID != "" {
		dbDls, err := r.storage.GetSink(ctx, wf.DeadLetterSinkID)
		if err == nil {
			dlsCfg := factory.SinkConfig{
				ID:     dbDls.ID,
				Type:   dbDls.Type,
				Config: dbDls.Config,
			}
			dls, err := r.createSinkInternal(ctx, dlsCfg)
			if err == nil {
				eng.SetDeadLetterSink(dls)
			} else {
				r.logger.Error("Registry: failed to create dead letter sink", "sink_id", wf.DeadLetterSinkID, "error", err)
			}
		} else {
			r.logger.Error("Registry: failed to get dead letter sink", "sink_id", wf.DeadLetterSinkID, "error", err)
		}
	}

	// Set source config for engine reconnect loop
	if len(srcConfigs) > 0 {
		eng.SetSourceConfig(config.SourceConfig{
			ReconnectIntervals: srcConfigs[0].ReconnectIntervals,
		})
	}

	// Pre-map nodes and edges for performance
	nodeMap := make(map[string]*storage.WorkflowNode)
	nodeIndex := make(map[string]int)
	for i := range wf.Nodes {
		nodeMap[wf.Nodes[i].ID] = &wf.Nodes[i]
		nodeIndex[wf.Nodes[i].ID] = i
	}

	edgeLabels := make(map[string]string)
	inDegree := make(map[string]int)
	// Visual breakpoints map: when true, messages should not traverse this edge
	edgeBreakpoints := make(map[string]bool)
	for _, edge := range wf.Edges {
		label := edge.SourceHandle
		if l, ok := edge.Config["label"].(string); ok && l != "" {
			label = l
		}
		if label != "" {
			edgeLabels[edge.SourceID+":"+edge.TargetID] = label
		}
		if bp, ok := edge.Config["breakpoint"].(bool); ok && bp {
			edgeBreakpoints[edge.SourceID+":"+edge.TargetID] = true
		}
		inDegree[edge.TargetID]++
	}

	eng.SetTraceRecorder(r)

	adj := make(map[string][]string)
	for _, edge := range wf.Edges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge.TargetID)
	}

	// Find source nodes for router
	var sourceNodes []*storage.WorkflowNode
	for i, node := range wf.Nodes {
		if node.Type == "source" {
			sourceNodes = append(sourceNodes, &wf.Nodes[i])
		}
	}

	// Set Workflow Router
	r.setupWorkflowRouter(eng, id, sourceNodes, nodeMap, adj, nodeIndex, edgeLabels, edgeBreakpoints, inDegree, sinkNodeToIndex)

	// Per-source configuration
	sourceEngineCfg := config.SourceConfig{}
	for _, sn := range sourceNodes {
		dbSrc, _ := r.storage.GetSource(ctx, sn.RefID)

		val := dbSrc.Config["reconnect_intervals"]
		if val == "" {
			val = dbSrc.Config["reconnect_interval"]
		}

		if val != "" {
			parts := strings.Split(val, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if d, err := parseDuration(part); err == nil {
					sourceEngineCfg.ReconnectIntervals = append(sourceEngineCfg.ReconnectIntervals, d)
				}
			}
		}
	}
	eng.SetSourceConfig(sourceEngineCfg)

	// 4. Configure sink engine configs
	sinkIDs, sinkTypes, pkgSnkConfigs := buildSinkEngineConfigs(snkConfigs)
	eng.SetIDs(id, "multi", sinkIDs)
	eng.SetSinkTypes(sinkTypes)
	eng.SetSinkConfigs(pkgSnkConfigs)

	// Schema validation
	if wf.Schema != "" && wf.SchemaType != "" {
		r.setupSchemaValidation(eng, ctx, id, wf)
	}

	// Setup callbacks and checkpoint handler
	r.setupWorkflowCallbacks(eng, id, wf)

	r.engines[id] = &activeEngine{
		engine:          eng,
		cancel:          cancel,
		done:            done,
		srcConfigs:      srcConfigs,
		snkConfigs:      snkConfigs,
		isWorkflow:      true,
		workflow:        wf,
		baseProcessed:   wf.TotalProcessed,
		baseErrors:      wf.TotalErrors,
		baseLag:         wf.TotalLag,
		sinks:           sinks,
		nodeMap:         nodeMap,
		adj:             adj,
		nodeIndex:       nodeIndex,
		edgeLabels:      edgeLabels,
		edgeBreakpoints: edgeBreakpoints,
		inDegree:        inDegree,
		sinkNodeToIndex: sinkNodeToIndex,
	}

	if r.optimizer != nil {
		r.optimizer.Register(id, eng)
	}

	go r.runWorkflowEngine(eng, ctx, cancel, done, id, wf, ms, sinks)

	return nil
}

// setupWorkflowRouter configures the engine's message router for the workflow DAG traversal.
func (r *Registry) setupWorkflowRouter(
	eng *pkgengine.Engine, id string,
	sourceNodes []*storage.WorkflowNode,
	nodeMap map[string]*storage.WorkflowNode,
	adj map[string][]string,
	nodeIndex map[string]int,
	edgeLabels map[string]string,
	edgeBreakpoints map[string]bool,
	inDegree map[string]int,
	sinkNodeToIndex map[string]int,
) {
	eng.SetRouter(func(ctx context.Context, msg hermod.Message) ([]pkgengine.RoutedMessage, error) {
		// Stamp the workflow id onto every message as it enters the workflow.
		// Downstream trace recording (doApplyTransformation) and PII discovery
		// stats (recordPIIDiscoveries) read "_hermod_workflow_id" from message
		// metadata; without this stamp those per-node trace steps and stats are
		// always skipped, leaving the message trace empty of node detail.
		msg.SetMetadata("_hermod_workflow_id", id)

		sourceNodeID := msg.Metadata()["_source_node_id"]
		if sourceNodeID == "" && len(sourceNodes) > 0 {
			sourceNodeID = sourceNodes[0].ID
		}

		// Record an ingestion trace step at the source node so the message
		// trace always shows "message received" even before any transform runs.
		r.recordSourceIngestTrace(ctx, id, sourceNodeID, msg)

		t := acquireWorkflowTraversal(r, eng, id, nodeMap, adj, nodeIndex, edgeLabels, edgeBreakpoints, inDegree, sinkNodeToIndex)
		msg.Retain()
		t.currentMessages[nodeIndex[sourceNodeID]] = msg

		t.wg.Add(1)
		t.processNode(ctx, sourceNodeID)
		t.wg.Wait()

		routed := t.routed
		releaseWorkflowTraversal(t)

		return routed, nil
	})
}

var traversalPool = sync.Pool{
	New: func() any {
		return &workflowTraversal{}
	},
}

func acquireWorkflowTraversal(
	registry *Registry,
	eng *pkgengine.Engine,
	workflowID string,
	nodeMap map[string]*storage.WorkflowNode,
	adj map[string][]string,
	nodeIndex map[string]int,
	edgeLabels map[string]string,
	edgeBreakpoints map[string]bool,
	inDegree map[string]int,
	sinkNodeToIndex map[string]int,
) *workflowTraversal {
	t := traversalPool.Get().(*workflowTraversal)
	t.registry = registry
	t.eng = eng
	t.workflowID = workflowID
	t.nodeMap = nodeMap
	t.adj = adj
	t.nodeIndex = nodeIndex
	t.edgeLabels = edgeLabels
	t.edgeBreakpoints = edgeBreakpoints
	t.inDegree = inDegree
	t.sinkNodeToIndex = sinkNodeToIndex

	numNodes := len(nodeIndex)
	if cap(t.currentMessages) < numNodes {
		t.currentMessages = make([]hermod.Message, numNodes)
		t.receivedCount = make([]int32, numNodes)
		t.resolvedCount = make([]int32, numNodes)
		t.fired = make([]int32, numNodes)
	} else {
		t.currentMessages = t.currentMessages[:numNodes]
		clear(t.currentMessages)
		t.receivedCount = t.receivedCount[:numNodes]
		clear(t.receivedCount)
		t.resolvedCount = t.resolvedCount[:numNodes]
		clear(t.resolvedCount)
		t.fired = t.fired[:numNodes]
		clear(t.fired)
	}
	t.routed = t.routed[:0]
	return t
}

func releaseWorkflowTraversal(t *workflowTraversal) {
	// Clear message references and ensure they are released to avoid memory leaks
	// while the traversal object is sitting in the pool.
	for i := range t.currentMessages {
		if m := t.currentMessages[i]; m != nil {
			m.Release()
			t.currentMessages[i] = nil
		}
	}
	traversalPool.Put(t)
}

func (r *Registry) setupSchemaValidation(eng *pkgengine.Engine, ctx context.Context, id string, wf storage.Workflow) {
	var v schema.Validator
	var err error

	if strings.HasPrefix(wf.Schema, "registry:") {
		schemaName := strings.TrimPrefix(wf.Schema, "registry:")
		v, _, err = r.schemaRegistry.GetLatestValidator(ctx, schemaName)
	} else {
		v, err = schema.NewValidator(schema.SchemaConfig{
			Type:   schema.SchemaType(wf.SchemaType),
			Schema: wf.Schema,
		})
	}

	if err != nil {
		r.broadcastLog(id, "ERROR", fmt.Sprintf("Failed to initialize schema validator: %v", err))
	} else {
		eng.SetValidator(v)
		r.broadcastLog(id, "INFO", fmt.Sprintf("Schema validation enabled (Type: %s)", wf.SchemaType))
	}
}

func (r *Registry) setupWorkflowCallbacks(eng *pkgengine.Engine, id string, wf storage.Workflow) {
	if r.storage != nil {
		eng.SetLogger(NewDatabaseLogger(context.Background(), r, id))
		eng.SetOnStatusChange(func(update telemetry.StatusUpdate) {
			// Ensure every broadcast carries the workflow ID so real-time UI
			// consumers can reliably associate the update with this workflow.
			if update.WorkflowID == "" {
				update.WorkflowID = id
			}

			// Synchronously update status in storage as they change rarely and are
			// critical for visibility.
			dbCtx := context.Background()
			_ = r.storage.UpdateWorkflowStatus(dbCtx, id, update.EngineStatus)
			if update.SourceID != "" {
				_ = r.storage.UpdateSourceStatus(dbCtx, update.SourceID, update.SourceStatus)
			}
			for sinkID, status := range update.SinkStatuses {
				_ = r.storage.UpdateSinkStatus(dbCtx, sinkID, status)
			}

			// Immediate state changes (Errors, Circuit Breaker) trigger notifications
			isError := strings.Contains(strings.ToLower(update.EngineStatus), "error") ||
				strings.Contains(strings.ToLower(update.EngineStatus), "circuit_breaker_open") ||
				update.DeadLetterCount >= uint64(wf.DLQThreshold) && wf.DLQThreshold > 0

			if isError && r.notificationService != nil {
				// We still fetch the workflow for the latest metadata (name) to
				// ensure notifications are accurate.
				if workflow, err := r.storage.GetWorkflow(dbCtx, id); err == nil {
					if strings.Contains(strings.ToLower(update.EngineStatus), "error") {
						r.notificationService.Notify(dbCtx, "Workflow Error",
							fmt.Sprintf("Workflow '%s' (ID: %s) entered error state: %s",
								workflow.Name, workflow.ID, update.EngineStatus), workflow)
					}
					if strings.Contains(strings.ToLower(update.EngineStatus), "circuit_breaker_open") {
						r.notificationService.Notify(dbCtx, "Circuit Breaker Alert",
							fmt.Sprintf("Circuit breaker opened for a sink in workflow '%s' (ID: %s)",
								workflow.Name, workflow.ID), workflow)
					}
					if workflow.DLQThreshold > 0 && update.DeadLetterCount >= uint64(workflow.DLQThreshold) {
						r.notificationService.Notify(dbCtx, "DLQ Threshold Exceeded",
							fmt.Sprintf("Workflow '%s' (ID: %s) has %d messages in DLQ, exceeding threshold of %d",
								workflow.Name, workflow.ID, update.DeadLetterCount, workflow.DLQThreshold), workflow)
					}
				}
			}

			r.BroadcastStatus(update)
		})

		// Set Checkpoint Handler to persist stateful transformation states
		eng.SetCheckpointHandler(func(ctx context.Context, sourceState map[string]string) error {
			// Persist source state if provided (keys are prefixed with nodeID by multiSource)
			if sourceState != nil {
				for _, node := range wf.Nodes {
					if node.Type != "source" {
						continue
					}
					// Extract only keys belonging to this source node and strip the prefix
					prefix := node.ID + ":"
					perSourceState := make(map[string]string)
					for k, v := range sourceState {
						if strings.HasPrefix(k, prefix) {
							perSourceState[strings.TrimPrefix(k, prefix)] = v
						}
					}
					if len(perSourceState) == 0 {
						continue
					}
					if err := r.storage.UpdateSourceState(ctx, node.RefID, perSourceState); err != nil {
						r.broadcastLog(id, "ERROR", fmt.Sprintf("Failed to persist source state: %v", err))
					} else if r.logger != nil {
						r.logger.Info("Persisted source state during checkpoint", "workflow_id", id, "source_id", node.RefID, "state", perSourceState)
					}
				}
			} else if r.logger != nil {
				r.logger.Debug("No source state to persist during checkpoint", "workflow_id", id)
			}

			r.nodeStatesMu.Lock()
			defer r.nodeStatesMu.Unlock()

			prefix := id + ":"
			for key, state := range r.nodeStates {
				if strings.HasPrefix(key, prefix) {
					nodeID := strings.TrimPrefix(key, prefix)
					if err := r.storage.UpdateNodeState(ctx, id, nodeID, state); err != nil {
						return err
					}
				}
			}
			return nil
		})
	} else {
		eng.SetOnStatusChange(func(update telemetry.StatusUpdate) {
			r.BroadcastStatus(update)
		})
	}
}

// runWorkflowEngine runs the engine in a goroutine and handles cleanup on completion.
func (r *Registry) runWorkflowEngine(eng *pkgengine.Engine, ctx context.Context, cancel context.CancelFunc, done chan struct{}, id string, wf storage.Workflow, ms *multiSource, sinks []hermod.Sink) {
	defer func() {
		if rec := recover(); rec != nil {
			// A panic in a single workflow must never crash the worker or impact
			// other workflows. Recover here, log it, and keep the workflow active
			// so the worker's reconciliation loop restarts it on the next sync.
			r.logger.Error("Workflow engine panicked", "workflow_id", id, "panic", rec, "stack", string(debug.Stack()))
			r.broadcastLog(id, "ERROR", fmt.Sprintf("Workflow panicked: %v", rec))
			if r.storage != nil {
				dbCtx := context.Background()
				if workflow, errGet := r.storage.GetWorkflow(dbCtx, id); errGet == nil {
					workflow.Status = fmt.Sprintf("Error: panic: %v", rec)
					// Keep Active = true so reconciliation restarts it.
					_ = r.storage.UpdateWorkflow(dbCtx, workflow)
				}
			}
		}
		r.mu.Lock()
		delete(r.engines, id)
		r.mu.Unlock()
		if r.optimizer != nil {
			r.optimizer.Unregister(id)
		}
		// Always release source and sink resources, even on panic, to avoid leaks.
		ms.Close()
		for _, snk := range sinks {
			snk.Close()
		}
		close(done)
	}()

	err := eng.Start(ctx)

	// Check if it was cancelled by us
	if ctx.Err() != nil {
		r.logger.Info("Workflow engine stopped (cancelled)", "workflow_id", id, "error", ctx.Err())
		return
	}

	if err != nil {
		r.logger.Error("Workflow failed", "workflow_id", id, "error", err)
		r.broadcastLog(id, "ERROR", fmt.Sprintf("Workflow failed: %v", err))
	} else {
		r.logger.Info("Workflow engine stopped naturally", "workflow_id", id)
		r.broadcastLog(id, "INFO", "Workflow stopped naturally")
	}

	if r.storage != nil {
		dbCtx := context.Background()
		if workflow, errGet := r.storage.GetWorkflow(dbCtx, id); errGet == nil {
			if err != nil {
				workflow.Status = "Error: " + err.Error()
				// Keep Active = true so reconciliation restarts it
				r.logger.Error("Workflow failed, keeping active for reconciliation", "workflow_id", id, "error", err)
			} else {
				// Deactivate ONLY if it was not cancelled (which we already checked)
				// and it's not a persistent workflow that should stay active.
				// For now, we follow the existing logic but with better logging.
				workflow.Active = false
				workflow.Status = "Completed"
				r.logger.Info("Workflow marked as inactive (completed)", "workflow_id", id)

				// Update source and sinks only if we are deactivating
				for _, node := range workflow.Nodes {
					switch node.Type {
					case "source":
						if !r.IsResourceInUse(dbCtx, node.RefID, id, true) {
							_ = r.storage.UpdateSourceStatus(dbCtx, node.RefID, "")
						}
					case "sink":
						if !r.IsResourceInUse(dbCtx, node.RefID, id, false) {
							_ = r.storage.UpdateSinkStatus(dbCtx, node.RefID, "")
						}
					}
				}
			}
			_ = r.storage.UpdateWorkflow(dbCtx, workflow)
		}
	}
	// Source and sink cleanup happens in the deferred function above so that it
	// runs on every exit path, including panics.
}

// --- Workflow Lifecycle ---

func (r *Registry) StopAll() {
	r.mu.Lock()
	ids := make([]string, 0, len(r.engines))
	for id := range r.engines {
		ids = append(ids, id)
	}
	r.mu.Unlock()

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(wfID string) {
			defer wg.Done()
			_ = r.stopEngine(wfID, false)
		}(id)
	}
	wg.Wait()
}

func (r *Registry) StopEngine(id string) error {
	return r.stopEngine(id, true)
}

func (r *Registry) DrainWorkflowDLQ(id string) error {
	r.mu.Lock()
	ae, ok := r.engines[id]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("workflow engine %s not running on this worker", id)
	}

	return ae.engine.DrainDLQ(context.Background())
}

func (r *Registry) StopEngineWithoutUpdate(id string) error {
	return r.stopEngine(id, false)
}

func (r *Registry) stopEngine(id string, updateStorage bool) error {
	r.mu.Lock()
	ae, ok := r.engines[id]
	if !ok {
		r.mu.Unlock()
		return nil // Engine not running, no error
	}

	ae.cancel()
	// Release lock to allow other operations while waiting for engine to stop
	r.mu.Unlock()

	// Wait for engine to gracefully shutdown
	select {
	case <-ae.done:
	case <-time.After(30 * time.Second):
		r.logger.Warn("Engine stop timeout", "workflow_id", id)
		// Attempt a hard stop to ensure the workflow actually halts
		if ae.engine != nil {
			ae.engine.HardStop()
		}
		// Give a short grace period after hard stop
		select {
		case <-ae.done:
		case <-time.After(2 * time.Second):
		}
	}

	if updateStorage && r.storage != nil {
		ctx := context.Background()
		if workflow, err := r.storage.GetWorkflow(ctx, id); err == nil {
			workflow.Active = false
			workflow.Status = ""
			_ = r.storage.UpdateWorkflow(ctx, workflow)

			// Update source and sinks
			for _, node := range workflow.Nodes {
				switch node.Type {
				case "source":
					_ = r.storage.UpdateSourceStatus(ctx, node.RefID, "")
				case "sink":
					_ = r.storage.UpdateSinkStatus(ctx, node.RefID, "")
				}
			}
		}
	}

	r.mu.Lock()
	delete(r.engines, id)
	r.mu.Unlock()

	return nil
}

// --- Rebuild & Resume ---

func (r *Registry) RebuildWorkflow(ctx context.Context, workflowID string, fromOffset int64) error {
	if r.storage == nil {
		return fmt.Errorf("registry storage is not initialized, cannot rebuild workflow %s", workflowID)
	}
	wf, err := r.storage.GetWorkflow(ctx, workflowID)
	if err != nil {
		return err
	}

	// 1. Find Event Store sink
	var eventStoreNode *storage.WorkflowNode
	var eventStoreSink *storage.Sink
	for i, node := range wf.Nodes {
		if node.Type == "sink" {
			snk, err := r.storage.GetSink(ctx, node.RefID)
			if err == nil && snk.Type == "eventstore" {
				eventStoreNode = &wf.Nodes[i]
				eventStoreSink = &snk
				break
			}
		}
	}

	if eventStoreNode == nil {
		return fmt.Errorf("no eventstore sink found in workflow %s", workflowID)
	}

	// 2. Prepare Sinks
	var sinks []hermod.Sink
	sinkNodeToIndex := make(map[string]int)
	nodeMap := make(map[string]*storage.WorkflowNode)
	adj := make(map[string][]string)

	for i := range wf.Nodes {
		nodeMap[wf.Nodes[i].ID] = &wf.Nodes[i]
	}
	for _, edge := range wf.Edges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge.TargetID)
	}

	for _, node := range wf.Nodes {
		if node.Type == "sink" && node.ID != eventStoreNode.ID {
			dbSnk, err := r.storage.GetSink(ctx, node.RefID)
			if err == nil {
				snk, err := r.createSinkInternal(ctx, factory.SinkConfig{ID: dbSnk.ID, Type: dbSnk.Type, Config: dbSnk.Config})
				if err == nil {
					sinkNodeToIndex[node.ID] = len(sinks)
					sinks = append(sinks, snk)
				}
			}
		}
	}
	defer func() {
		for _, s := range sinks {
			s.Close()
		}
	}()

	// 3. Create Event Store source
	srcCfg := factory.SourceConfig{
		ID:     eventStoreSink.ID,
		Type:   "eventstore",
		Config: eventStoreSink.Config,
	}
	if srcCfg.Config == nil {
		srcCfg.Config = make(map[string]string)
	}
	srcCfg.Config["from_offset"] = strconv.FormatInt(fromOffset, 10)

	src, err := r.createSourceInternal(ctx, srcCfg)
	if err != nil {
		return err
	}
	defer src.Close()

	// 4. Replay loop
	for {
		msg, err := src.Read(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "no more events") {
				break
			}
			return err
		}

		// Find source nodes and start traversal
		for _, node := range wf.Nodes {
			if node.Type == "source" {
				for _, targetID := range adj[node.ID] {
					targetNode := nodeMap[targetID]
					if targetNode != nil {
						r.runWorkflowNodeFromReplay(workflowID, targetNode, msg, eventStoreNode.ID, wf, nodeMap, adj, sinks, sinkNodeToIndex)
					}
				}
			}
		}
		msg.Release()
	}
	return nil
}

func (r *Registry) runWorkflowNodeFromReplay(workflowID string, node *storage.WorkflowNode, msg hermod.Message, skipNodeID string, wf storage.Workflow, nodeMap map[string]*storage.WorkflowNode, adj map[string][]string, sinks []hermod.Sink, sinkNodeToIndex map[string]int) {
	if node.ID == skipNodeID {
		return
	}

	// Clone message to avoid side effects between branches
	m := msg.Clone()
	defer m.Release()

	processedMsgs, branch, err := r.runWorkflowNode(workflowID, node, m)
	defer func() {
		for _, pm := range processedMsgs {
			if pm != m {
				pm.Release()
			}
		}
	}()

	if err != nil {
		r.broadcastLog(workflowID, "error", fmt.Sprintf("Node %s error: %v", r.getNodeName(*node), err))
		return
	}

	if len(processedMsgs) == 0 {
		return
	}

	for _, processedMsg := range processedMsgs {
		if node.Type == "sink" {
			idx, ok := sinkNodeToIndex[node.ID]
			if ok && idx < len(sinks) {
				sinks[idx].Write(context.Background(), processedMsg)
			}
			continue
		}

		// Determine next nodes based on branch
		var targets []string
		if branch != "" {
			// Find edges with this label
			for _, edge := range wf.Edges {
				label := edge.SourceHandle
				if l, ok := edge.Config["label"].(string); ok && l != "" {
					label = l
				}
				if edge.SourceID == node.ID && label == branch {
					targets = append(targets, edge.TargetID)
				}
			}
		} else {
			targets = adj[node.ID]
		}

		for _, targetID := range targets {
			targetNode := nodeMap[targetID]
			if targetNode != nil {
				r.runWorkflowNodeFromReplay(workflowID, targetNode, processedMsg, skipNodeID, wf, nodeMap, adj, sinks, sinkNodeToIndex)
			}
		}
	}
}

// resumeFromNode continues traversal starting after startNodeID, forcing a specific branch label if provided.
func (r *Registry) resumeFromNode(workflowID, startNodeID string, msg hermod.Message, wf storage.Workflow, nodeMap map[string]*storage.WorkflowNode, adj map[string][]string, sinks []hermod.Sink, sinkNodeToIndex map[string]int, branch string) {
	var targets []string
	if branch != "" {
		for _, edge := range wf.Edges {
			label := edge.SourceHandle
			if l, ok := edge.Config["label"].(string); ok && l != "" {
				label = l
			}
			if edge.SourceID == startNodeID && label == branch {
				targets = append(targets, edge.TargetID)
			}
		}
	} else {
		targets = adj[startNodeID]
	}
	for _, targetID := range targets {
		if tn := nodeMap[targetID]; tn != nil {
			r.runWorkflowNodeFromReplay(workflowID, tn, msg, startNodeID, wf, nodeMap, adj, sinks, sinkNodeToIndex)
		}
	}
}

// ResumeApproval resumes a halted workflow at an approval node with the specified decision branch ("approved" or "rejected").
func (r *Registry) ResumeApproval(ctx context.Context, app storage.Approval, branch string) error {
	if r.storage == nil {
		return errors.New("registry storage not available")
	}
	wf, err := r.storage.GetWorkflow(ctx, app.WorkflowID)
	if err != nil {
		return err
	}

	// Build adjacency and node map
	nodeMap := make(map[string]*storage.WorkflowNode)
	adj := make(map[string][]string)
	for i := range wf.Nodes {
		nodeMap[wf.Nodes[i].ID] = &wf.Nodes[i]
	}
	for _, e := range wf.Edges {
		adj[e.SourceID] = append(adj[e.SourceID], e.TargetID)
	}

	// Build sinks and index mapping
	var sinks []hermod.Sink
	sinkNodeToIndex := make(map[string]int)
	for i := range wf.Nodes {
		n := wf.Nodes[i]
		if n.Type == "sink" {
			dbSnk, e := r.storage.GetSink(ctx, n.RefID)
			if e != nil {
				for _, s := range sinks {
					_ = s.Close()
				}
				return fmt.Errorf("failed to get sink %s: %w", n.RefID, e)
			}
			snkCfg := factory.SinkConfig{ID: dbSnk.ID, Type: dbSnk.Type, Config: dbSnk.Config}
			s, e := r.createSinkInternal(ctx, snkCfg)
			if e != nil {
				for _, s2 := range sinks {
					_ = s2.Close()
				}
				return e
			}
			sinkNodeToIndex[n.ID] = len(sinks)
			sinks = append(sinks, s)
		}
	}
	defer func() {
		for _, s := range sinks {
			_ = s.Close()
		}
	}()

	// Reconstruct message
	m := message.AcquireMessage()
	m.SetID(app.MessageID)
	m.SetAfter(app.Payload)
	for k, v := range app.Metadata {
		m.SetMetadata(k, v)
	}
	for k, v := range app.Data {
		m.SetData(k, v)
	}
	if len(app.FormData) > 0 {
		m.SetData("_approval_form", app.FormData)
		// Also merge into root for convenience
		for k, v := range app.FormData {
			m.SetData(k, v)
		}
	}

	// Continue traversal from the approval node with forced branch
	r.resumeFromNode(app.WorkflowID, app.NodeID, m, wf, nodeMap, adj, sinks, sinkNodeToIndex, branch)
	message.ReleaseMessage(m)
	return nil
}

// --- Test Workflow ---

func (r *Registry) TestWorkflow(ctx context.Context, wf storage.Workflow, msg hermod.Message) ([]WorkflowStepResult, error) {
	if err := r.ValidateWorkflow(ctx, wf); err != nil {
		return nil, err
	}

	msgToMap := func(m hermod.Message) map[string]any {
		if m == nil {
			return nil
		}
		jb, _ := json.Marshal(m)
		var res map[string]any
		_ = json.Unmarshal(jb, &res)
		return res
	}

	var steps []WorkflowStepResult
	adj := make(map[string][]string)
	inDegree := make(map[string]int)
	for _, edge := range wf.Edges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge.TargetID)
		inDegree[edge.TargetID]++
	}

	// Map edges to labels for easy lookup
	edgeLabels := make(map[string]string)
	for _, edge := range wf.Edges {
		label := edge.SourceHandle
		if l, ok := edge.Config["label"].(string); ok && l != "" {
			label = l
		}
		if label != "" {
			edgeLabels[edge.SourceID+":"+edge.TargetID] = label
		}
	}

	// Find Source nodes
	var sourceNodes []*storage.WorkflowNode
	for i, node := range wf.Nodes {
		if node.Type == "source" {
			sourceNodes = append(sourceNodes, &wf.Nodes[i])
		}
	}

	if len(sourceNodes) == 0 {
		return nil, errors.New("no source node found")
	}

	toRelease := make([]hermod.Message, 0, len(wf.Nodes)*2)
	released := make(map[hermod.Message]bool)
	defer func() {
		for _, m := range toRelease {
			if m != nil && !released[m] {
				m.Release()
				released[m] = true
			}
		}
	}()

	currentMessages := make(map[string]hermod.Message)
	for _, sn := range sourceNodes {
		c := msg.Clone()
		currentMessages[sn.ID] = c
		toRelease = append(toRelease, c)
	}

	receivedCount := make(map[string]int)

	for _, sn := range sourceNodes {
		r.broadcastLiveMessageFromHermod(wf.ID, sn.ID, msg, false, "")
		steps = append(steps, WorkflowStepResult{
			NodeID:   sn.ID,
			NodeType: "source",
			Payload:  msgToMap(msg),
			Metadata: msg.Metadata(),
		})
	}

	visited := make(map[string]bool)
	queue := []string{}
	for _, sn := range sourceNodes {
		queue = append(queue, sn.ID)
	}

	for len(queue) > 0 {
		currID := queue[0]
		queue = queue[1:]

		if visited[currID] {
			continue
		}
		visited[currID] = true

		currMsg := currentMessages[currID]

		// Run current node if it's not the source (already handled)
		currNode := findNodeByID(wf.Nodes, currID)
		if currNode == nil {
			// Defensive: a queued node id may reference a node that no longer
			// exists (e.g. a dangling edge left over after a node was deleted
			// in the editor). Skip it instead of dereferencing a nil node,
			// which would panic and abort the request. Mirrors the guard used
			// by the live engine in discoverWorkflowSinks/resumeFromNode.
			continue
		}
		var currBranch string
		var msgs []hermod.Message
		if currNode.Type != "source" && currMsg == nil {
			// Node reached only through branches that were not taken: it has no
			// input message, so it is skipped. Its outgoing edges are still
			// traversed below to keep downstream join counters consistent.
			steps = append(steps, WorkflowStepResult{
				NodeID:   currID,
				NodeType: currNode.Type,
				Filtered: true,
			})
		} else if currNode.Type != "source" {
			var err error
			msgs, currBranch, err = r.runWorkflowNode(wf.ID, currNode, currMsg)
			for _, m := range msgs {
				if m != currMsg {
					toRelease = append(toRelease, m)
				}
			}
			if err != nil {
				steps = append(steps, WorkflowStepResult{
					NodeID:   currID,
					NodeType: currNode.Type,
					Error:    err.Error(),
				})
			}
			if len(msgs) == 0 {
				steps = append(steps, WorkflowStepResult{
					NodeID:   currID,
					NodeType: currNode.Type,
					Filtered: true,
					Branch:   currBranch,
				})
			} else {
				// Update step with output
				found := false
				currMsg = msgs[0] // Use first message for test result visualization
				for i := range steps {
					if steps[i].NodeID == currID {
						steps[i].Payload = msgToMap(currMsg)
						steps[i].Metadata = currMsg.Metadata()
						steps[i].Branch = currBranch
						found = true
						break
					}
				}
				if !found {
					steps = append(steps, WorkflowStepResult{
						NodeID:   currID,
						NodeType: currNode.Type,
						Payload:  msgToMap(currMsg),
						Metadata: currMsg.Metadata(),
						Branch:   currBranch,
					})
				}
			}
		}

		for _, targetID := range adj[currID] {
			edgeLabel := edgeLabels[currID+":"+targetID]

			match := true
			if currNode.Type == "condition" || currNode.Type == "switch" {
				if edgeLabel != "" && edgeLabel != currBranch {
					match = false
				}
			}

			receivedCount[targetID]++

			if match && currMsg != nil {
				strategy := ""
				targetNode := findNodeByID(wf.Nodes, targetID)
				if targetNode != nil {
					strategy, _ = targetNode.Config["strategy"].(string)
				}
				if currentMessages[targetID] == nil {
					c := currMsg.Clone()
					currentMessages[targetID] = c
					toRelease = append(toRelease, c)
				} else {
					// Merge
					r.mergeData(currentMessages[targetID].Data(), currMsg.Data(), strategy)
					if dm, ok := currentMessages[targetID].(interface{ ClearCachedPayload() }); ok {
						dm.ClearCachedPayload()
					}
				}
			}

			if receivedCount[targetID] == inDegree[targetID] {
				queue = append(queue, targetID)
			}
		}
	}

	return steps, nil
}
