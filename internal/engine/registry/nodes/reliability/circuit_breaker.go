package reliability

import (
	"context"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
)

type CircuitBreakerExecutor struct{}

func init() {
	registry.RegisterNodeExecutor("circuit_breaker", &CircuitBreakerExecutor{})
}

func (e *CircuitBreakerExecutor) Execute(ctx context.Context, nctx registry.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	state := e.getCBState(nctx, node.ID)
	threshold, _ := node.Config["failure_threshold"].(float64)
	if threshold == 0 {
		threshold = 5
	}

	if state.Status == "OPEN" {
		if time.Since(state.LastFailure) > 30*time.Second {
			state.Status = "HALF_OPEN"
			e.setCBState(nctx, node.ID, state)
		} else {
			return []hermod.Message{msg}, "failure", nil
		}
	}

	return []hermod.Message{msg}, "success", nil
}

type cbState struct {
	Status      string
	Failures    int
	LastFailure time.Time
}

func (e *CircuitBreakerExecutor) getCBState(nctx registry.NodeContext, nodeID string) cbState {
	if val, ok := nctx.GetNodeState("cb_" + nodeID); ok {
		if s, ok := val.(cbState); ok {
			return s
		}
	}
	return cbState{Status: "CLOSED"}
}

func (e *CircuitBreakerExecutor) setCBState(nctx registry.NodeContext, nodeID string, state cbState) {
	nctx.SetNodeState("cb_"+nodeID, state)
}
