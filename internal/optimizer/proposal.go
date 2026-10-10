package optimizer

import "context"

// Suggestion is a fix the gate would make. It is not applied: a Proposer
// stores it for a person to approve, and only an approval changes the
// workflow, through the same path as an edit in the editor.
type Suggestion struct {
	WorkflowID string
	NodeID     string
	Action     CorrectionAction
	Reason     string
}

// Proposer keeps a suggestion for approval.
type Proposer interface {
	Propose(ctx context.Context, s Suggestion) error
}

// MappingAdvisor suggests a field mapping for a node whose validation keeps
// failing, from a sample of what reaches it.
type MappingAdvisor interface {
	SuggestMapping(ctx context.Context, workflowID, nodeID string, sample map[string]any) (string, error)
}

// SetProposer sets where suggestions go. Without one they are only
// announced to the operator.
func (g *SelfCorrectionGate) SetProposer(p Proposer) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.proposer = p
}

// SetProposer sets where the optimizer's self-correction gate sends fixes.
func (o *Optimizer) SetProposer(p Proposer) { o.gate.SetProposer(p) }

// SetMappingAdvisor sets who the self-correction gate asks for mappings.
func (o *Optimizer) SetMappingAdvisor(a MappingAdvisor) { o.gate.SetMappingAdvisor(a) }

// SetMappingAdvisor sets who is asked for a mapping. Without one no model is
// called and the operator is only told that validation is failing.
func (g *SelfCorrectionGate) SetMappingAdvisor(a MappingAdvisor) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.advisor = a
}
