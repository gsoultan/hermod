package llm

import (
	"context"
	"fmt"
)

// Scope says on whose behalf a call is made: the vhost whose workflow runs it
// and, when a workflow node makes it, that workflow. A Budget reads it from
// the call's context to decide whose spend the call counts against.
//
// The engine sets it, never a message's data or metadata, so a payload cannot
// move its cost onto another vhost or workflow.
type Scope struct {
	VHost      string
	WorkflowID string
}

type scopeKey struct{}

// WithScope returns ctx carrying s.
func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

// ScopeFrom returns the scope ctx carries, or the zero Scope.
func ScopeFrom(ctx context.Context) Scope {
	s, _ := ctx.Value(scopeKey{}).(Scope)
	return s
}

// Limit names what refused a call.
type Limit string

const (
	// LimitDisabled is the vhost's AI kill switch.
	LimitDisabled Limit = "ai_disabled"
	// LimitVHostTokens and LimitVHostCost are the vhost's monthly budget.
	LimitVHostTokens Limit = "vhost_tokens"
	LimitVHostCost   Limit = "vhost_cost"
	// LimitWorkflowTokens and LimitWorkflowCost are one workflow's monthly cap.
	LimitWorkflowTokens Limit = "workflow_tokens"
	LimitWorkflowCost   Limit = "workflow_cost"
	// LimitUnavailable means the budget could not be read. Budgets fail
	// closed: a call that cannot be checked is not made.
	LimitUnavailable Limit = "budget_unavailable"
)

// BudgetError is a call refused by a spending limit. It matches
// ErrBudgetExceeded under errors.Is, so everything that already handles a
// spent budget (an AI node's error branch, the agent, the metrics) handles
// this one the same way.
type BudgetError struct {
	Limit      Limit
	VHost      string
	WorkflowID string
	// Used and Max are tokens for the token limits and micro-units of the
	// vhost's currency for the cost limits. Both are zero for the kill switch
	// and for an unreadable budget.
	Used int64
	Max  int64
	// Cause is why the budget could not be read, for LimitUnavailable.
	Cause error
}

func (e *BudgetError) Error() string {
	who := "vhost " + e.VHost
	if e.Limit == LimitWorkflowTokens || e.Limit == LimitWorkflowCost {
		who = "workflow " + e.WorkflowID + " in " + who
	}
	switch e.Limit {
	case LimitDisabled:
		return "AI budget: AI calls are switched off for " + who
	case LimitUnavailable:
		return fmt.Sprintf("AI budget: the budget of %s could not be checked, so the call was not made: %v", who, e.Cause)
	case LimitVHostCost, LimitWorkflowCost:
		return fmt.Sprintf("AI budget: monthly cost limit of %s reached (%.2f of %.2f)", who, float64(e.Used)/1e6, float64(e.Max)/1e6)
	default:
		return fmt.Sprintf("AI budget: monthly token limit of %s reached (%d of %d tokens)", who, e.Used, e.Max)
	}
}

// Unwrap makes a BudgetError match ErrBudgetExceeded.
func (e *BudgetError) Unwrap() error { return ErrBudgetExceeded }
