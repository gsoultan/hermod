package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/config"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
	"github.com/gsoultan/hermod/pkg/infra/compression"
	"github.com/gsoultan/hermod/pkg/infra/schema"
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
		dbSrc, err := r.GetSourceConfig(ctx, sn.RefID)
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
		sources:    subSources,
		msgChan:    make(chan hermod.Message, 100),
		errChan:    make(chan error, len(subSources)),
		workflowID: wf.ID,
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
			dbSnk, err := r.GetSinkConfig(ctx, node.RefID)
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
	// Sharding splits a sink's queue, so each shard has to know which messages
	// belong together. Left unset it used to fall through to the message ID,
	// which for CDC is the LSN -- unique per change -- so turning sharding on
	// scattered exactly the changes it was meant to keep in order. The row key
	// the source stamps is the right default; naming a metadata field stays
	// available for sources that carry their own.
	if psc.ShardCount > 1 && psc.ShardKeyMeta == "" {
		psc.ShardKeyMeta = hermod.MetaOrderingKey
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
		parts := strings.SplitSeq(val, ",")
		for p := range parts {
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
	if r.store() == nil {
		return fmt.Errorf("registry storage is not initialized, cannot start workflow %s", id)
	}
	if err := r.ValidateWorkflow(ctx, wf); err != nil {
		return fmt.Errorf("workflow validation failed: %w", err)
	}

	// Load node states for stateful transformations
	nodeStates, err := r.store().GetNodeStates(ctx, id)
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
		dbDls, err := r.GetSinkConfig(ctx, wf.DeadLetterSinkID)
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
	r.prepareWorkflowNodes(context.Background(), wf.Nodes)
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
		dbSrc, _ := r.GetSourceConfig(ctx, sn.RefID)

		val := dbSrc.Config["reconnect_intervals"]
		if val == "" {
			val = dbSrc.Config["reconnect_interval"]
		}

		if val != "" {
			parts := strings.SplitSeq(val, ",")
			for part := range parts {
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
	dbLogger := r.setupWorkflowCallbacks(eng, id, wf)

	r.engines[id] = &activeEngine{
		engine:          eng,
		dbLogger:        dbLogger,
		cancel:          cancel,
		done:            done,
		srcConfigs:      srcConfigs,
		snkConfigs:      snkConfigs,
		isWorkflow:      true,
		workflow:        wf,
		baseProcessed:   wf.TotalProcessed,
		baseErrors:      wf.TotalErrors,
		baseLag:         wf.TotalLag,
		startTime:       time.Now(),
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
	// A message enters at exactly one source, so a node fed by several *source*
	// nodes must not treat its siblings' edges as co-requisites — it would never
	// fire and the message would be acknowledged and dropped. Precompute the
	// in-degree restricted to the subgraph each source can reach, once per
	// workflow, so the router pays a map lookup rather than a graph walk per
	// message. Fan-out that converges *within* one traversal (switch branches
	// rejoining) is still counted, so joins keep their barrier.
	entryIDs := make([]string, 0, len(sourceNodes))
	for _, sn := range sourceNodes {
		entryIDs = append(entryIDs, sn.ID)
	}
	inDegreeByEntry := traversal.ReachableInDegreeByEntry(adj, entryIDs)

	eng.SetRouter(func(ctx context.Context, msg hermod.Message) ([]pkgengine.RoutedMessage, error) {
		// Stamp the workflow id onto every message as it enters the workflow.
		// Downstream trace recording (doApplyTransformation) and PII discovery
		// stats (recordPIIDiscoveries) read "_hermod_workflow_id" from message
		// metadata; without this stamp those per-node trace steps and stats are
		// always skipped, leaving the message trace empty of node detail.
		msg.SetMetadata("_hermod_workflow_id", id)

		sourceNodeID, _ := hermod.MetadataValue(msg, "_source_node_id")
		if sourceNodeID == "" && len(sourceNodes) > 0 {
			sourceNodeID = sourceNodes[0].ID
		}

		// Record an ingestion trace step at the source node so the message
		// trace always shows "message received" even before any transform runs.
		r.recordSourceIngestTrace(ctx, id, sourceNodeID, msg)

		effectiveInDegree := inDegree
		if reachable, ok := inDegreeByEntry[sourceNodeID]; ok {
			effectiveInDegree = reachable
		}

		t := traversal.Acquire(r, eng, id, nodeMap, adj, nodeIndex, edgeLabels, edgeBreakpoints, effectiveInDegree, sinkNodeToIndex)
		msg.Retain()
		t.CurrentMessages[nodeIndex[sourceNodeID]] = msg

		t.Traverse(ctx, sourceNodeID)

		// Clone the routed messages slice before releasing the traversal back to
		// the pool. This avoids a data race where a subsequent message reuse
		// clears the slice while a writer is still iterating over it.
		routed := make([]pkgengine.RoutedMessage, len(t.Routed))
		copy(routed, t.Routed)
		// A sink node that writes inline routes nothing, so an empty target list
		// here does not mean the message went nowhere. Mark it so the engine
		// acknowledges the source instead of pinning it.
		if t.InlineDelivered.Load() && !t.InlineFailed.Load() {
			msg.SetMetadata(pkgengine.MetaDeliveredInline, "true")
		}
		// A failing node parked this message — possibly as a clone, past a
		// fan-out — so carry the marker back onto the original. Without it the
		// engine sees an empty target list, cannot tell the message is already
		// preserved, and parks a second copy of the same event.
		if t.DeadLettered.Load() {
			msg.SetMetadata(pkgengine.MetaDeadLettered, "true")
		}
		traversal.Release(t)

		return routed, nil
	})
}

func (r *Registry) setupSchemaValidation(eng *pkgengine.Engine, ctx context.Context, id string, wf storage.Workflow) {
	var v schema.Validator
	var err error

	if after, ok := strings.CutPrefix(wf.Schema, "registry:"); ok {
		schemaName := after
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

// setupWorkflowCallbacks wires the supervisor, the log fan-out and the status
// listener onto a workflow engine. It returns the log fan-out so the caller can
// close it when the engine stops.
func (r *Registry) setupWorkflowCallbacks(eng *pkgengine.Engine, id string, wf storage.Workflow) *DatabaseLogger {
	eng.SetOnStall(func(reason string) { r.superviseStall(id, wf, reason) })

	var dbLogger *DatabaseLogger
	if r.store() != nil {
		dbLogger = NewDatabaseLogger(context.Background(), r, id, r.logger)
		eng.SetLogger(dbLogger)
		// dlqAlerted latches the DLQ threshold notification for the lifetime of
		// this engine. Once the count is over the line it stays over it, so
		// without the latch every later status change — a sink flapping, a
		// source reconnecting — would send the same alert again.
		var dlqAlerted atomic.Bool
		// The engine notifies on every status write rather than on every
		// status change, and checkHealth writes one per sink per second. The
		// notifications have to keep coming — they are the only thing pushing
		// per-workflow status to the UI — so the gate absorbs the redundancy
		// at the storage boundary instead.
		gate := newStatusWriteGate()
		eng.SetOnStatusChange(func(update telemetry.StatusUpdate) {
			// Ensure every broadcast carries the workflow ID so real-time UI
			// consumers can reliably associate the update with this workflow.
			if update.WorkflowID == "" {
				update.WorkflowID = id
			}

			r.applyStatusUpdate(context.Background(), id, gate, update, &dlqAlerted)

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
						if after, ok := strings.CutPrefix(k, prefix); ok {
							perSourceState[after] = v
						}
					}
					if len(perSourceState) == 0 {
						continue
					}
					if err := r.store().UpdateSourceState(ctx, node.RefID, perSourceState); err != nil {
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
				if after, ok := strings.CutPrefix(key, prefix); ok {
					nodeID := after
					if err := r.store().UpdateNodeState(ctx, id, nodeID, state); err != nil {
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
	return dbLogger
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
			if s := r.store(); s != nil {
				dbCtx := context.Background()
				if workflow, errGet := s.GetWorkflow(dbCtx, id); errGet == nil {
					workflow.Status = fmt.Sprintf("Error: panic: %v", rec)
					// Keep Active = true so reconciliation restarts it.
					_ = s.UpdateWorkflow(dbCtx, workflow)
				}
			}
		}
		r.mu.Lock()
		ae := r.engines[id]
		delete(r.engines, id)
		r.mu.Unlock()
		// Flush and stop this run's log fan-out. Without it every restart —
		// including every automatic one the supervisor performs — leaves a
		// flusher goroutine behind for the life of the worker.
		if ae != nil && ae.dbLogger != nil {
			ae.dbLogger.Close()
		}
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

	if s := r.store(); s != nil {
		dbCtx := context.Background()
		if workflow, errGet := s.GetWorkflow(dbCtx, id); errGet == nil {
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
							_ = s.UpdateSourceStatus(dbCtx, node.RefID, "")
						}
					case "sink":
						if !r.IsResourceInUse(dbCtx, node.RefID, id, false) {
							_ = s.UpdateSinkStatus(dbCtx, node.RefID, "")
						}
					}
				}
			}
			_ = s.UpdateWorkflow(dbCtx, workflow)
		}
	}
	// Source and sink cleanup happens in the deferred function above so that it
	// runs on every exit path, including panics.
}

// --- Workflow Lifecycle ---

func (r *Registry) StopAll() {
	// Before the engines go, while the count still means something. StopAll is
	// the shutdown path for the signal handler, the worker's own drain and the
	// edge binary alike, so it is the one place that sees every way a worker
	// goes down.
	r.mu.RLock()
	running := len(r.engines)
	r.mu.RUnlock()
	r.NotifyWorkerShutdown(context.Background(), running)

	r.mu.Lock()
	ids := make([]string, 0, len(r.engines))
	for id := range r.engines {
		ids = append(ids, id)
	}
	r.mu.Unlock()

	var wg sync.WaitGroup
	// Bound the overall shutdown from the shared budget rather than a local
	// constant, so this can never outlive the process-wide deadline (or the
	// orchestrator's grace period) that contains it.
	ctx, cancel := context.WithTimeout(context.Background(), config.Shutdown().PerEngine)
	defer cancel()

	for _, id := range ids {
		wg.Go(func() {
			_ = r.stopEngine(ctx, id, false)
		})
	}
	wg.Wait()

	// The shutdown alert is dispatched asynchronously, and the process exits
	// straight after StopAll returns. Without this flush the one notification
	// that says a worker went down loses the race every time. Bounded so an
	// unreachable channel cannot hold a rolling deploy open.
	if r.notificationService != nil {
		if !r.notificationService.WaitFor(shutdownNotifyFlush) {
			r.logger.Warn("Shutdown notifications did not finish before the flush deadline",
				"deadline", shutdownNotifyFlush.String())
		}
	}
}

// shutdownNotifyFlush bounds how long StopAll waits for alerts to leave.
const shutdownNotifyFlush = 5 * time.Second

// StopEngine stops a workflow on an operator's instruction. Unlike
// StopEngineWithoutUpdate — which the supervisor and the worker's own
// reconciliation use — this marks the end of a stall episode, so the workflow's
// automatic-restart budget starts fresh the next time it runs.
func (r *Registry) StopEngine(ctx context.Context, id string) error {
	r.onManualStop(id)
	err := r.stopEngine(ctx, id, true)
	// Only the operator-initiated path alerts. StopEngineWithoutUpdate — the
	// supervisor's rebuild and the worker's lease reconciliation — stops
	// engines constantly as a normal part of running, and alerting there would
	// report a healthy failover as an outage.
	if err == nil {
		r.notifyWorkflowStopped(ctx, id)
	}
	return err
}

// classifySinkStatuses splits a status update's per-sink map into the sinks
// whose breaker is open and the sinks that are unreachable.
//
// Both lists are sorted so the notification text is stable: the map's iteration
// order is random, and an alert whose wording changes every five minutes reads
// as a new incident each time it is re-sent.
func classifySinkStatuses(statuses map[string]string) (breakerOpen, unreachable []string) {
	for id, st := range statuses {
		switch s := strings.ToLower(st); {
		case strings.Contains(s, "circuit_breaker_open"):
			breakerOpen = append(breakerOpen, id)
		case strings.Contains(s, "reconnecting"):
			unreachable = append(unreachable, id)
		}
	}
	slices.Sort(breakerOpen)
	slices.Sort(unreachable)
	return breakerOpen, unreachable
}

// notifyOnStatusChange turns an engine status update into operator
// notifications. It is the body of the callback the registry installs on every
// engine (see setupWorkflowCallbacks) and is called from nowhere else.
//
// dlqAlerted latches the dead-letter alert for the lifetime of one engine.
// Once the count is past the threshold it stays past it, so without the latch
// every later status change — a sink flapping, a source reconnecting — would
// send the same alert again.
func (r *Registry) notifyOnStatusChange(ctx context.Context, id string, update telemetry.StatusUpdate, dlqAlerted *atomic.Bool) {
	if r.notificationService == nil {
		return
	}

	status := strings.ToLower(update.EngineStatus)
	isEngineError := strings.Contains(status, "error")
	isStalled := strings.Contains(status, "stalled")

	// The breaker and an unreachable sink are reported per sink, not on the
	// engine, so both have to be read out of SinkStatuses.
	//
	// isBreakerOpen used to test EngineStatus for "circuit_breaker_open", which
	// the engine never writes there: writer.go's recordFailure calls
	// setSinkStatus, and the only writers of the engine's own status are
	// setStatus and SetEngineStatusUnless, neither of which passes that string.
	// The alert was therefore unreachable, and its test passed because it
	// hand-built a status update the engine cannot produce.
	breakerSinks, unreachableSinks := classifySinkStatuses(update.SinkStatuses)

	// A sink can also be reported unreachable on the engine's status alone,
	// which is what checkHealth writes on the first failing ping.
	if len(unreachableSinks) == 0 && strings.HasPrefix(status, "reconnecting:sink:") {
		unreachableSinks = []string{strings.TrimPrefix(update.EngineStatus, "reconnecting:sink:")}
	}

	// The DLQ arm used to gate on the workflow as it was when this engine
	// started, while the branch below re-read the threshold from storage, so an
	// edited threshold could open a gate the inner check then closed, or vice
	// versa. The engine owns the comparison now: it holds the live threshold
	// and reports a status change on the message that crosses it, so any
	// non-zero count here is worth looking at.
	if !isEngineError && !isStalled &&
		len(breakerSinks) == 0 && len(unreachableSinks) == 0 &&
		update.DeadLetterCount == 0 {
		return
	}

	// Re-read for the latest metadata (name, threshold) so the notification is
	// accurate rather than describing the workflow as it was at start-up.
	workflow, err := r.store().GetWorkflow(ctx, id)
	if err != nil {
		return
	}

	if isEngineError {
		r.notificationService.Notify(ctx, "Workflow Error",
			fmt.Sprintf("Workflow '%s' (ID: %s) entered error state: %s",
				workflow.Name, workflow.ID, update.EngineStatus), workflow)
	}
	if len(breakerSinks) > 0 {
		r.notificationService.Notify(ctx, "Circuit Breaker Alert",
			fmt.Sprintf("Circuit breaker opened for sink(s) %s in workflow '%s' (ID: %s); writes to them are being refused",
				strings.Join(breakerSinks, ", "), workflow.Name, workflow.ID), workflow)
	}
	// Naming the sink is the point: "reconnecting" on its own sends an operator
	// to the UI to find out which destination is down.
	if len(unreachableSinks) > 0 {
		r.notificationService.Notify(ctx, "Sink Unreachable",
			fmt.Sprintf("Sink(s) %s in workflow '%s' (ID: %s) failed their health check; the workflow keeps running and retries, it does not stop",
				strings.Join(unreachableSinks, ", "), workflow.Name, workflow.ID), workflow)
	}
	// A stalled engine still reports itself up and its sinks reachable: the
	// watchdog sets this when nothing completes while work is outstanding, which
	// is what a wedged sink looks like when it still answers Ping.
	if isStalled {
		r.notificationService.Notify(ctx, "Workflow Stalled",
			fmt.Sprintf("Workflow '%s' (ID: %s) has stopped making progress while work is outstanding; automatic restart will be attempted",
				workflow.Name, workflow.ID), workflow)
	}
	if workflow.DLQThreshold > 0 &&
		update.DeadLetterCount >= uint64(workflow.DLQThreshold) &&
		dlqAlerted.CompareAndSwap(false, true) {
		// "since it started", not "in the DLQ": the count is the engine's own,
		// reset on every restart, and the queue itself may hold far more from
		// previous runs.
		r.notificationService.Notify(ctx, "DLQ Threshold Exceeded",
			fmt.Sprintf("Workflow '%s' (ID: %s) has dead-lettered %d messages since it started, exceeding the threshold of %d",
				workflow.Name, workflow.ID, update.DeadLetterCount, workflow.DLQThreshold), workflow)
	}
}

func (r *Registry) DrainWorkflowDLQ(ctx context.Context, id string) error {
	r.mu.RLock()
	ae, ok := r.engines[id]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("workflow engine %s not running on this worker", id)
	}

	return ae.engine.DrainDLQ(ctx)
}

func (r *Registry) StopEngineWithoutUpdate(ctx context.Context, id string) error {
	return r.stopEngine(ctx, id, false)
}

func (r *Registry) stopEngine(ctx context.Context, id string, updateStorage bool) error {
	r.mu.Lock()
	ae, ok := r.engines[id]
	if !ok {
		r.mu.Unlock()
		return nil // Engine not running, no error
	}

	ae.cancel()
	// Release lock to allow other operations while waiting for engine to stop
	r.mu.Unlock()

	// From here the engine is coming down whatever else happens, so the entry
	// saying it runs on this worker has to go on every path out — including the
	// early returns below when the caller's context is already done.
	//
	// It did not, and that is a failover wedging itself: the worker losing its
	// lease has a cancelled context, so its stop returned immediately and left
	// the entry behind. IsEngineRunning then answered true for an engine that
	// was gone, the worker taking over tried to stop it before starting its own,
	// hit the same early return, and retried every sync interval forever. The
	// workflow reported itself running, received nothing, and could not be
	// restarted without bouncing the process.
	//
	// Only this engine is retired. If a takeover has already registered a
	// replacement under the same id, removing it by key alone would stop the
	// workflow that had just been correctly started.
	defer func() {
		r.mu.Lock()
		if current, ok := r.engines[id]; ok && current == ae {
			delete(r.engines, id)
		}
		r.mu.Unlock()
	}()

	// Wait for engine to gracefully shutdown
	select {
	case <-ae.done:
	case <-ctx.Done():
		r.logger.Warn("Engine stop canceled by context", "workflow_id", id)
		return ctx.Err()
	case <-time.After(30 * time.Second):
		r.logger.Warn("Engine stop timeout", "workflow_id", id)
		// Attempt a hard stop to ensure the workflow actually halts
		if ae.engine != nil {
			ae.engine.HardStop()
		}
		// Give a short grace period after hard stop
		select {
		case <-ae.done:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	if s := r.store(); updateStorage && s != nil {
		if workflow, err := s.GetWorkflow(ctx, id); err == nil {
			workflow.Active = false
			workflow.Status = ""
			_ = s.UpdateWorkflow(ctx, workflow)

			// Update source and sinks
			for _, node := range workflow.Nodes {
				switch node.Type {
				case "source":
					_ = s.UpdateSourceStatus(ctx, node.RefID, "")
				case "sink":
					_ = s.UpdateSinkStatus(ctx, node.RefID, "")
				}
			}
		}
	}

	return nil
}

// --- Rebuild & Resume ---

func (r *Registry) RebuildWorkflow(ctx context.Context, workflowID string, fromOffset int64) error {
	if r.store() == nil {
		return fmt.Errorf("registry storage is not initialized, cannot rebuild workflow %s", workflowID)
	}
	wf, err := r.store().GetWorkflow(ctx, workflowID)
	if err != nil {
		return err
	}

	// 1. Find Event Store sink
	var eventStoreNode *storage.WorkflowNode
	var eventStoreSink *storage.Sink
	for i, node := range wf.Nodes {
		if node.Type == "sink" {
			snk, err := r.GetSinkConfig(ctx, node.RefID)
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
			dbSnk, err := r.GetSinkConfig(ctx, node.RefID)
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
						r.runWorkflowNodeFromReplay(workflowID, targetNode, msg, eventStoreNode.ID, r.liveEngine(workflowID), wf, nodeMap, adj, sinks, sinkNodeToIndex)
					}
				}
			}
		}
		msg.Release()
	}
	return nil
}

func (r *Registry) runWorkflowNodeFromReplay(workflowID string, node *storage.WorkflowNode, msg hermod.Message, skipNodeID string, eng *pkgengine.Engine, wf storage.Workflow, nodeMap map[string]*storage.WorkflowNode, adj map[string][]string, sinks []hermod.Sink, sinkNodeToIndex map[string]int) {
	if node.ID == skipNodeID {
		return
	}

	// Clone message to avoid side effects between branches
	m := msg.Clone()
	defer m.Release()

	processedMsgs, branch, err := r.RunWorkflowNode(workflowID, node, m)
	defer func() {
		for _, pm := range processedMsgs {
			if pm != m {
				pm.Release()
			}
		}
	}()

	if err != nil {
		r.broadcastLog(workflowID, "error", fmt.Sprintf("Node %s error: %v", r.getNodeName(*node), err))
		r.replayLost(workflowID, node, eng, m, err)
		return
	}

	if len(processedMsgs) == 0 {
		return
	}

	for _, processedMsg := range processedMsgs {
		if node.Type == "sink" {
			r.replayWriteToSink(workflowID, node, eng, processedMsg, sinks, sinkNodeToIndex)
			continue
		}

		for _, targetID := range replayTargets(wf, adj, node.ID, branch) {
			if targetNode := nodeMap[targetID]; targetNode != nil {
				r.runWorkflowNodeFromReplay(workflowID, targetNode, processedMsg, skipNodeID, eng, wf, nodeMap, adj, sinks, sinkNodeToIndex)
			}
		}
	}
}

// replayWriteToSink delivers a resumed message to its sink node.
//
// The resumed message has no source left to leave unacknowledged — the suspended
// row is deleted as soon as the resume returns — so a discarded write error here
// is the message gone, with nothing logged. A wait node exists to hold a message
// until a destination is ready, which makes this the moment that destination is
// most likely still down.
func (r *Registry) replayWriteToSink(workflowID string, node *storage.WorkflowNode, eng *pkgengine.Engine, msg hermod.Message, sinks []hermod.Sink, sinkNodeToIndex map[string]int) {
	idx, ok := sinkNodeToIndex[node.ID]
	if !ok || idx >= len(sinks) {
		return
	}
	if err := sinks[idx].Write(context.Background(), msg); err != nil {
		r.broadcastLog(workflowID, "error", fmt.Sprintf(
			"Node %s could not write a resumed message: %v", r.getNodeName(*node), err))
		r.replayLost(workflowID, node, eng, msg, err)
	}
}

// replayTargets returns the nodes a replayed message flows to from nodeID,
// honouring a forced branch label when the node chose one.
func replayTargets(wf storage.Workflow, adj map[string][]string, nodeID, branch string) []string {
	if branch == "" {
		return adj[nodeID]
	}
	var targets []string
	for _, edge := range wf.Edges {
		label := edge.SourceHandle
		if l, ok := edge.Config["label"].(string); ok && l != "" {
			label = l
		}
		if edge.SourceID == nodeID && label == branch {
			targets = append(targets, edge.TargetID)
		}
	}
	return targets
}

// replayLost handles a resumed message that could not be delivered.
//
// The live pipeline answers this by not acknowledging the source, so the message
// comes back. A resumed message has no source left to hold it — the suspended row
// is deleted as soon as the resume returns — so the dead-letter sink is the only
// place it can survive. Where there is no engine (the approval and event-store
// paths build their own sinks and can run with the workflow stopped) there is no
// dead-letter sink either, and the loss is at least stated rather than silent.
func (r *Registry) replayLost(workflowID string, node *storage.WorkflowNode, eng *pkgengine.Engine, msg hermod.Message, cause error) {
	if eng != nil && eng.DeadLetterOrphanedMessage(context.Background(), node.ID, msg, cause) {
		return
	}
	r.broadcastLog(workflowID, "error", fmt.Sprintf(
		"Node %s failed on a resumed message and there is no dead-letter sink, so the message is lost: %v",
		r.getNodeName(*node), cause))
	if r.logger != nil {
		r.logger.Error("Resumed message lost: no dead-letter sink",
			"workflow_id", workflowID, "node_id", node.ID, "error", cause)
	}
}

// resumeFromNode continues traversal starting after startNodeID, forcing a specific branch label if provided.
func (r *Registry) resumeFromNode(workflowID, startNodeID string, msg hermod.Message, eng *pkgengine.Engine, wf storage.Workflow, nodeMap map[string]*storage.WorkflowNode, adj map[string][]string, sinks []hermod.Sink, sinkNodeToIndex map[string]int, branch string) {
	for _, targetID := range replayTargets(wf, adj, startNodeID, branch) {
		if tn := nodeMap[targetID]; tn != nil {
			r.runWorkflowNodeFromReplay(workflowID, tn, msg, startNodeID, eng, wf, nodeMap, adj, sinks, sinkNodeToIndex)
		}
	}
}

// ResumeApproval resumes a halted workflow at an approval node with the specified decision branch ("approved" or "rejected").
func (r *Registry) ResumeApproval(ctx context.Context, app storage.Approval, branch string) error {
	if r.store() == nil {
		return errors.New("registry storage not available")
	}
	wf, err := r.store().GetWorkflow(ctx, app.WorkflowID)
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
			dbSnk, e := r.GetSinkConfig(ctx, n.RefID)
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
	r.resumeFromNode(app.WorkflowID, app.NodeID, m, r.liveEngine(app.WorkflowID), wf, nodeMap, adj, sinks, sinkNodeToIndex, branch)
	// See resumeSuspendedMessage: honour the refcount rather than forcing the
	// message back into the pool under a possible second owner.
	m.Release()
	return nil
}

// --- Test Workflow ---

// SimulationInput is what a simulation feeds its source nodes.
type SimulationInput struct {
	// Message seeds every source node PerSource does not name. Nil leaves such
	// a source unseeded, and everything downstream of it unreached.
	Message hermod.Message
	// PerSource seeds source nodes, by node ID, with their own sample.
	PerSource map[string]hermod.Message
	// Partial previews a workflow that is still being built; see
	// SimulateWorkflow.
	Partial bool
}

// seedFor is the message a source node starts with: its own sample when it has
// one, the shared message otherwise. Seeding every source with one message is
// what put a refreshed branch's columns on every other branch.
func (in SimulationInput) seedFor(sourceNodeID string) hermod.Message {
	if m := in.PerSource[sourceNodeID]; m != nil {
		return m
	}
	return in.Message
}

// TestWorkflow runs msg through a workflow the engine could start, seeding
// every source node with it. It is what the editor's Test button calls.
func (r *Registry) TestWorkflow(ctx context.Context, wf storage.Workflow, msg hermod.Message) ([]WorkflowStepResult, error) {
	return r.SimulateWorkflow(ctx, wf, SimulationInput{Message: msg})
}

// SimulateWorkflow runs sample messages through a workflow without starting an
// engine, and reports what every node emitted.
//
// Partial previews a workflow that is still being built. It keeps the checks
// that make the workflow a graph — a source, no cycle, no edge to nowhere — and
// drops the ones about whether it could run: a reachable sink, configured
// references, the dead-letter settings. None of those change what a node emits,
// and a workflow is missing its sink exactly while its nodes are being set up,
// which is when the editor needs the preview.
func (r *Registry) SimulateWorkflow(ctx context.Context, wf storage.Workflow, in SimulationInput) ([]WorkflowStepResult, error) {
	if err := r.validateForSimulation(ctx, wf, in.Partial); err != nil {
		return nil, err
	}
	r.prepareWorkflowNodes(ctx, wf.Nodes)

	sim := newSimulation(r, wf)
	defer sim.releaseAll()
	if err := sim.seed(in); err != nil {
		return nil, err
	}
	for len(sim.queue) > 0 {
		id := sim.queue[0]
		sim.queue = sim.queue[1:]
		sim.visit(id)
	}
	return sim.steps, nil
}

func (r *Registry) validateForSimulation(ctx context.Context, wf storage.Workflow, partial bool) error {
	if !partial {
		return r.ValidateWorkflow(ctx, wf)
	}
	_, err := r.checkWorkflowGraph(wf)
	return err
}

// simulation is one SimulateWorkflow run: the graph, the message waiting at
// each node, and the steps reported so far.
type simulation struct {
	r          *Registry
	wfID       string
	nodes      map[string]*storage.WorkflowNode
	sources    []*storage.WorkflowNode
	adj        map[string][]string
	inDegree   map[string]int
	edgeLabels map[string]string
	waiting    map[string]hermod.Message
	received   map[string]int
	visited    map[string]bool
	queue      []string
	steps      []WorkflowStepResult
	// owned is every message the run created, released when it ends.
	owned []hermod.Message
}

func newSimulation(r *Registry, wf storage.Workflow) *simulation {
	s := &simulation{
		r:          r,
		wfID:       wf.ID,
		nodes:      make(map[string]*storage.WorkflowNode, len(wf.Nodes)),
		adj:        make(map[string][]string),
		inDegree:   make(map[string]int),
		edgeLabels: make(map[string]string),
		waiting:    make(map[string]hermod.Message),
		received:   make(map[string]int),
		visited:    make(map[string]bool),
		owned:      make([]hermod.Message, 0, len(wf.Nodes)*2),
	}
	for i := range wf.Nodes {
		s.nodes[wf.Nodes[i].ID] = &wf.Nodes[i]
		if wf.Nodes[i].Type == "source" {
			s.sources = append(s.sources, &wf.Nodes[i])
		}
	}
	for _, edge := range wf.Edges {
		s.adj[edge.SourceID] = append(s.adj[edge.SourceID], edge.TargetID)
		s.inDegree[edge.TargetID]++
		if label := edgeLabel(edge); label != "" {
			s.edgeLabels[edge.SourceID+":"+edge.TargetID] = label
		}
	}
	return s
}

// edgeLabel is the branch an edge carries: its configured label, else its
// source handle.
func edgeLabel(edge storage.WorkflowEdge) string {
	if l, ok := edge.Config["label"].(string); ok && l != "" {
		return l
	}
	return edge.SourceHandle
}

// own records a message the run created so it is released when the run ends.
func (s *simulation) own(m hermod.Message) hermod.Message {
	s.owned = append(s.owned, m)
	return m
}

func (s *simulation) releaseAll() {
	released := make(map[hermod.Message]bool, len(s.owned))
	for _, m := range s.owned {
		if m != nil && !released[m] {
			m.Release()
			released[m] = true
		}
	}
}

// seed hands each source node its starting message, reports it as that
// node's step, and queues every source.
func (s *simulation) seed(in SimulationInput) error {
	if len(s.sources) == 0 {
		return errors.New("no source node found")
	}
	for _, sn := range s.sources {
		s.queue = append(s.queue, sn.ID)
		seed := in.seedFor(sn.ID)
		if seed == nil {
			// Nothing of its own to send: its branch stays unreached rather than
			// being handed another source's sample.
			continue
		}
		s.waiting[sn.ID] = s.own(seed.Clone())
		s.r.broadcastLiveMessageFromHermod(s.wfID, sn.ID, seed, false, "")
		s.steps = append(s.steps, WorkflowStepResult{
			NodeID:   sn.ID,
			NodeType: "source",
			Payload:  seed.ToMap(),
			Metadata: seed.Metadata(),
		})
	}
	return nil
}

// visit runs a node once every edge into it has been walked, and passes what
// it emitted along its own edges.
func (s *simulation) visit(id string) {
	if s.visited[id] {
		return
	}
	s.visited[id] = true

	node := s.nodes[id]
	if node == nil {
		// Defensive: a queued node id may reference a node that no longer
		// exists (e.g. a dangling edge left over after a node was deleted
		// in the editor). Skip it instead of dereferencing a nil node,
		// which would panic and abort the request. Mirrors the guard used
		// by the live engine in discoverWorkflowSinks/resumeFromNode.
		return
	}
	out, branch := s.run(id, node)
	s.forward(id, node, out, branch)
}

// run executes a node on the message waiting for it and records its step. It
// returns what the node emitted, nil when it emitted nothing, and the branch
// it took. A source's step was recorded when it was seeded.
func (s *simulation) run(id string, node *storage.WorkflowNode) (hermod.Message, string) {
	in := s.waiting[id]
	if node.Type == "source" {
		return in, ""
	}
	if in == nil {
		// Node reached only through branches that were not taken: it has no
		// input message, so it is skipped. Its outgoing edges are still
		// traversed to keep downstream join counters consistent.
		s.steps = append(s.steps, WorkflowStepResult{NodeID: id, NodeType: node.Type, Filtered: true})
		return nil, ""
	}

	msgs, branch, err := s.r.RunWorkflowNode(s.wfID, node, in)
	for _, m := range msgs {
		if m != in {
			s.own(m)
		}
	}
	if err != nil {
		s.steps = append(s.steps, WorkflowStepResult{NodeID: id, NodeType: node.Type, Error: err.Error()})
	}
	if len(msgs) == 0 {
		s.steps = append(s.steps, WorkflowStepResult{NodeID: id, NodeType: node.Type, Filtered: true, Branch: branch})
		return nil, branch
	}
	// The first message stands for the node in the result.
	s.recordOutput(id, node, msgs[0], branch)
	return msgs[0], branch
}

// recordOutput puts a node's output on its step: the error step it already
// has, when it failed and still emitted, or a new one.
func (s *simulation) recordOutput(id string, node *storage.WorkflowNode, out hermod.Message, branch string) {
	for i := range s.steps {
		if s.steps[i].NodeID == id {
			s.steps[i].Payload = out.ToMap()
			s.steps[i].Metadata = out.Metadata()
			s.steps[i].Branch = branch
			return
		}
	}
	s.steps = append(s.steps, WorkflowStepResult{
		NodeID:   id,
		NodeType: node.Type,
		Payload:  out.ToMap(),
		Metadata: out.Metadata(),
		Branch:   branch,
	})
}

// forward passes a node's output along each edge on the branch it took, and
// queues a target once every edge into it has been walked — taken or not, so
// a join is not left waiting for a branch that was never going to arrive.
func (s *simulation) forward(id string, node *storage.WorkflowNode, out hermod.Message, branch string) {
	routes := node.Type == "condition" || node.Type == "switch"
	for _, target := range s.adj[id] {
		label := s.edgeLabels[id+":"+target]
		taken := !routes || label == "" || label == branch
		s.received[target]++
		if taken && out != nil {
			s.deliver(target, out)
		}
		if s.received[target] == s.inDegree[target] {
			s.queue = append(s.queue, target)
		}
	}
}

// deliver hands target a copy of msg, merging it into the message already
// waiting there when target joins several branches.
func (s *simulation) deliver(target string, msg hermod.Message) {
	waiting := s.waiting[target]
	if waiting == nil {
		s.waiting[target] = s.own(msg.Clone())
		return
	}
	strategy := ""
	if node := s.nodes[target]; node != nil {
		strategy, _ = node.Config["strategy"].(string)
	}
	s.r.mergeData(waiting.DataRef(), msg.Data(), strategy)
	if dm, ok := waiting.(interface{ ClearCachedPayload() }); ok {
		dm.ClearCachedPayload()
	}
}

func (r *Registry) prepareWorkflowNodes(ctx context.Context, nodes []storage.WorkflowNode) {
	for i := range nodes {
		node := &nodes[i]
		if node.Type == "sink" && node.RefID != "" {
			// The sequential flag is only settable on the sink entity in the
			// current UI, but the executor reads it from the node. Carry it
			// across so the feature is reachable; see resolveSinkNodeSequential.
			//
			// Bounded: this runs on the workflow-start path, and an unresponsive
			// metadata store must not hang startup indefinitely.
			lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			snk, err := r.store().GetSink(lookupCtx, node.RefID)
			cancel()
			if err == nil {
				resolveSinkNodeSequential(node, snk.Config)
			}
		}
		if node.Type == "transformation" {
			transType, _ := node.Config["transType"].(string)
			if transType == "pipeline" {
				stepsStr, _ := node.Config["steps"].(string)
				var steps []map[string]any
				if err := json.Unmarshal([]byte(stepsStr), &steps); err == nil {
					for j := range steps {
						step := steps[j]
						st, _ := step["transType"].(string)
						if t, ok := transformer.Get(st); ok {
							if pt, ok := t.(transformer.PreparedTransformer); ok {
								if prepared, err := pt.Prepare(step); err == nil {
									steps[j] = prepared
								}
							}
						}
					}
					node.Config["_parsed_steps"] = steps
				}
			} else {
				if t, ok := transformer.Get(transType); ok {
					if pt, ok := t.(transformer.PreparedTransformer); ok {
						if prepared, err := pt.Prepare(node.Config); err == nil {
							node.Config = prepared
						}
					}
				}
			}
		}
	}
}
