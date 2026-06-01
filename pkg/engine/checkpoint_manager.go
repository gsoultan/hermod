package engine

import (
	"context"
	"sync"

	"github.com/user/hermod"
)

type CheckpointManager struct {
	engine       *Engine
	handler      func(ctx context.Context, sourceState map[string]string) error
	mu           sync.Mutex
	inCheckpoint bool
}

func NewCheckpointManager(e *Engine, handler func(ctx context.Context, sourceState map[string]string) error) *CheckpointManager {
	return &CheckpointManager{
		engine:  e,
		handler: handler,
	}
}

func (m *CheckpointManager) Checkpoint(ctx context.Context) error {
	if m.handler == nil {
		return nil
	}

	m.mu.Lock()
	if m.inCheckpoint {
		m.mu.Unlock()
		return nil
	}
	m.inCheckpoint = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.inCheckpoint = false
		m.mu.Unlock()
	}()

	m.engine.logger.Info("Starting checkpoint", "workflow_id", m.engine.workflowID)

	// Wait for all in-flight messages to be processed and acknowledged by sinks
	m.engine.inFlightWg.Wait()

	// Collect source state if stateful
	var sourceState map[string]string
	if stateful, ok := m.engine.source.(hermod.Stateful); ok {
		sourceState = stateful.GetState()
	}

	// Call the checkpoint handler (e.g. to save node states in Registry)
	if err := m.handler(ctx, sourceState); err != nil {
		m.engine.logger.Error("Checkpoint failed", "workflow_id", m.engine.workflowID, "error", err)
		return err
	}

	m.engine.logger.Info("Checkpoint completed successfully", "workflow_id", m.engine.workflowID)
	return nil
}

func (m *CheckpointManager) IsInCheckpoint() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inCheckpoint
}
