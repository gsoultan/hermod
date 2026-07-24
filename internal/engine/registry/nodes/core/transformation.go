package core

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
)

func init() {
	interfaces.RegisterNodeExecutor("transformation", &TransformationNode{})
}

// TransformationNode handles data transformations.
type TransformationNode struct{}

// Execute runs the configured transformation or pipeline.
func (n *TransformationNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	transType, _ := node.Config["transType"].(string)
	if transType == "pipeline" {
		return n.runPipeline(ctx, nctx, node, msg)
	}
	if transType == "parallel_pipeline" {
		return n.runParallelPipeline(ctx, nctx, node, msg)
	}

	// Optimization: Avoid cloning here as the message is already either a clone
	// or owned by this traversal path. ApplyTransformation will handle its own
	// internal logic.
	tctx := context.WithValue(ctx, hermod.NodeIDKey, node.ID)
	res, err := nctx.ApplyTransformation(tctx, msg, transType, node.Config)
	if err != nil {
		nctx.BroadcastLiveMessage(workflowID, node.ID, msg, true, err.Error())
		return nil, "", err
	}
	if res == nil {
		return nil, "", nil
	}
	return []hermod.Message{res}, "", nil
}

func (n *TransformationNode) runPipeline(ctx context.Context, nctx interfaces.NodeContext, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	var steps []map[string]any
	if cached, ok := node.Config["_parsed_steps"].([]map[string]any); ok {
		steps = cached
	} else {
		stepsStr, _ := node.Config["steps"].(string)
		_ = json.Unmarshal([]byte(stepsStr), &steps)
	}

	current := msg
	pctx := nctx.ContextWithPipelineSnapshot(ctx)
	for _, step := range steps {
		st, _ := step["transType"].(string)
		var err error
		current, err = nctx.ApplyTransformation(pctx, current, st, step)
		if err != nil {
			return nil, "", err
		}
		if current == nil {
			return nil, "", nil
		}
	}
	return []hermod.Message{current}, "", nil
}

func (n *TransformationNode) runParallelPipeline(ctx context.Context, nctx interfaces.NodeContext, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	var steps []map[string]any
	if cached, ok := node.Config["_parsed_steps"].([]map[string]any); ok {
		steps = cached
	} else {
		stepsStr, _ := node.Config["steps"].(string)
		_ = json.Unmarshal([]byte(stepsStr), &steps)
	}

	if len(steps) == 0 {
		return []hermod.Message{msg}, "", nil
	}

	results := make([]hermod.Message, len(steps))
	errs := make([]error, len(steps))
	var wg sync.WaitGroup

	for i, step := range steps {
		wg.Add(1)
		go func(i int, step map[string]any) {
			defer wg.Done()
			// Each parallel step gets its own clone of the message
			mClone := msg.Clone()
			defer mClone.Release()

			st, _ := step["transType"].(string)
			res, err := nctx.ApplyTransformation(ctx, mClone, st, step)
			if err != nil {
				errs[i] = err
				return
			}
			if res != nil {
				res.Retain()
				results[i] = res
			}
		}(i, step)
	}

	wg.Wait()

	// Check for errors
	for _, err := range errs {
		if err != nil {
			// Release all gathered messages
			for _, r := range results {
				if r != nil {
					r.Release()
				}
			}
			return nil, "", err
		}
	}

	// Merge all results into a single message
	// We use the first non-nil result as the base, then merge others.
	var merged hermod.Message
	for _, r := range results {
		if r == nil {
			continue
		}
		if merged == nil {
			merged = r
		} else {
			// Basic merge of data maps
			for k, v := range r.Data() {
				merged.SetData(k, v)
			}
			r.Release()
		}
	}

	if merged == nil {
		return nil, "", nil
	}

	return []hermod.Message{merged}, "", nil
}
