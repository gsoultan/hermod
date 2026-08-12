package reliability

import (
	"context"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
)

type CircuitBreakerExecutor struct{}

func init() {
	interfaces.RegisterNodeExecutor("circuit_breaker", &CircuitBreakerExecutor{})
}

func (e *CircuitBreakerExecutor) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	state := e.getCBState(nctx, node.ID)
	threshold, _ := node.Config["failure_threshold"].(float64)
	if threshold == 0 {
		threshold = 5
	}

	if state.Status != "OPEN" && state.Failures >= int(threshold) {
		state.Status = "OPEN"
		state.LastFailure = time.Now()
		e.setCBState(nctx, node.ID, state)
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

func (e *CircuitBreakerExecutor) getCBState(nctx interfaces.NodeContext, nodeID string) cbState {
	if val, ok := nctx.GetNodeState("cb_" + nodeID); ok {
		if s, ok := val.(cbState); ok {
			return s
		}
	}
	return cbState{Status: "CLOSED"}
}

func (e *CircuitBreakerExecutor) setCBState(nctx interfaces.NodeContext, nodeID string, state cbState) {
	nctx.SetNodeState("cb_"+nodeID, state)
}

// RecordFailure counts a downstream failure against the breaker.
//
// Without this the breaker could not open at all: Execute read a failure count
// that nothing ever incremented, so every message took the success branch
// however broken the downstream was. A control that cannot fire is worse than
// no control, because someone believes it is there.
func (e *CircuitBreakerExecutor) RecordFailure(nctx interfaces.NodeContext, nodeID string) {
	state := e.getCBState(nctx, nodeID)
	state.Failures++
	state.LastFailure = time.Now()
	e.setCBState(nctx, nodeID, state)
}

// RecordSuccess clears the count.
//
// Consecutive failures are what indicate a broken downstream. Counting them for
// all time would trip a healthy system that had one bad hour last week, and
// leave it tripped.
func (e *CircuitBreakerExecutor) RecordSuccess(nctx interfaces.NodeContext, nodeID string) {
	state := e.getCBState(nctx, nodeID)
	if state.Failures == 0 && state.Status == "CLOSED" {
		return
	}
	e.setCBState(nctx, nodeID, cbState{Status: "CLOSED"})
}
