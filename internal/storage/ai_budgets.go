package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// AIBudget is a vhost's spending policy for language model calls: a kill
// switch, a monthly budget for the whole vhost, and monthly caps for single
// workflows. Months are calendar months in UTC. A zero limit is no limit.
//
// Costs are worked out from the prices set here, in whatever currency the
// vhost bills in; Hermod holds no price list of its own, since model ids and
// their prices are configuration that changes faster than a release.
type AIBudget struct {
	VHost string `json:"vhost"`

	// Disabled is the kill switch: while it is on, no AI node or agent in
	// the vhost may call a model.
	Disabled bool `json:"disabled"`

	MonthlyTokens int64   `json:"monthly_tokens,omitempty"`
	MonthlyCost   float64 `json:"monthly_cost,omitempty"`
	// Currency labels the costs (for example USD). It is not converted.
	Currency string `json:"currency,omitempty"`

	// Prices are per million tokens. The model "*" prices every model the
	// list does not name; a cost limit needs it, so no call goes uncounted.
	Prices []AIModelPrice `json:"prices,omitempty"`

	Workflows []AIWorkflowCap `json:"workflows,omitempty"`

	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// AIModelPrice is what one model costs per million tokens.
type AIModelPrice struct {
	Model            string  `json:"model"`
	InputPerMillion  float64 `json:"input_per_million"`
	OutputPerMillion float64 `json:"output_per_million"`
}

// AIWorkflowCap is one workflow's monthly limit, within the vhost's.
type AIWorkflowCap struct {
	WorkflowID    string  `json:"workflow_id"`
	MonthlyTokens int64   `json:"monthly_tokens,omitempty"`
	MonthlyCost   float64 `json:"monthly_cost,omitempty"`
}

// AnyModel is the price entry for models the price list does not name.
const AnyModel = "*"

// Bounds on a budget, which is read on every model call.
const (
	maxAIPrices       = 256
	maxAIWorkflowCaps = 1024
	maxAIModelLen     = 128
	maxAICurrencyLen  = 8
)

// ErrAIBudgetsUnsupported is returned when the configured backend cannot
// store AI budgets.
var ErrAIBudgetsUnsupported = errors.New("this storage backend cannot hold AI budgets")

// HasCostLimit reports whether the vhost or any workflow cap limits cost.
func (b AIBudget) HasCostLimit() bool {
	if b.MonthlyCost > 0 {
		return true
	}
	for _, w := range b.Workflows {
		if w.MonthlyCost > 0 {
			return true
		}
	}
	return false
}

// Limited reports whether the budget can refuse a call at all.
func (b AIBudget) Limited() bool {
	if b.Disabled || b.MonthlyTokens > 0 || b.HasCostLimit() {
		return true
	}
	for _, w := range b.Workflows {
		if w.MonthlyTokens > 0 {
			return true
		}
	}
	return false
}

// WorkflowCap returns the cap set for workflowID, if there is one.
func (b AIBudget) WorkflowCap(workflowID string) (AIWorkflowCap, bool) {
	if workflowID == "" {
		return AIWorkflowCap{}, false
	}
	for _, w := range b.Workflows {
		if w.WorkflowID == workflowID {
			return w, true
		}
	}
	return AIWorkflowCap{}, false
}

// Price returns the price of model: its own entry, else the "*" entry.
func (b AIBudget) Price(model string) (AIModelPrice, bool) {
	var fallback *AIModelPrice
	for i := range b.Prices {
		switch b.Prices[i].Model {
		case model:
			return b.Prices[i], true
		case AnyModel:
			fallback = &b.Prices[i]
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return AIModelPrice{}, false
}

// CostMicros is what a call to model with these token counts cost, in
// millionths of the budget's currency. A model with no price costs 0.
func (b AIBudget) CostMicros(model string, inputTokens, outputTokens int64) int64 {
	p, ok := b.Price(model)
	if !ok {
		return 0
	}
	// Per million tokens, in millionths: the two millions cancel.
	return int64(math.Round(float64(inputTokens)*p.InputPerMillion + float64(outputTokens)*p.OutputPerMillion))
}

// CostToMicros converts a cost limit to the unit usage is counted in.
func CostToMicros(cost float64) int64 {
	return int64(math.Round(cost * 1e6))
}

// ValidateAIBudget reports what is wrong with a budget before it is saved.
func ValidateAIBudget(b AIBudget) error {
	if b.VHost == "" || b.VHost == "all" {
		return errors.New("an AI budget belongs to one vhost: name it")
	}
	if b.MonthlyTokens < 0 || !validAmount(b.MonthlyCost) {
		return errors.New("monthly_tokens and monthly_cost must not be negative")
	}
	if len(b.Currency) > maxAICurrencyLen {
		return fmt.Errorf("currency must be at most %d characters", maxAICurrencyLen)
	}
	if err := validatePrices(b.Prices); err != nil {
		return err
	}
	if err := validateCaps(b.Workflows); err != nil {
		return err
	}
	// A cost limit with an unpriced model would count that model's calls as
	// free and let them through for ever. Requiring the "*" price means
	// every call is priced.
	if _, priced := b.Price(AnyModel); b.HasCostLimit() && !priced {
		return fmt.Errorf("a cost limit needs a %q price for the models the price list does not name", AnyModel)
	}
	return nil
}

func validatePrices(prices []AIModelPrice) error {
	if len(prices) > maxAIPrices {
		return fmt.Errorf("a budget may price at most %d models", maxAIPrices)
	}
	seen := make(map[string]bool, len(prices))
	for _, p := range prices {
		if p.Model == "" || len(p.Model) > maxAIModelLen {
			return fmt.Errorf("a price needs a model id of at most %d characters (%q prices every other model)", maxAIModelLen, AnyModel)
		}
		if seen[p.Model] {
			return fmt.Errorf("model %q is priced twice", p.Model)
		}
		seen[p.Model] = true
		if !validAmount(p.InputPerMillion) || !validAmount(p.OutputPerMillion) {
			return fmt.Errorf("the prices of model %q must not be negative", p.Model)
		}
	}
	return nil
}

func validateCaps(caps []AIWorkflowCap) error {
	if len(caps) > maxAIWorkflowCaps {
		return fmt.Errorf("a budget may cap at most %d workflows", maxAIWorkflowCaps)
	}
	capped := make(map[string]bool, len(caps))
	for _, w := range caps {
		if w.WorkflowID == "" {
			return errors.New("a workflow cap needs a workflow_id")
		}
		if capped[w.WorkflowID] {
			return fmt.Errorf("workflow %q is capped twice", w.WorkflowID)
		}
		capped[w.WorkflowID] = true
		if w.MonthlyTokens < 0 || !validAmount(w.MonthlyCost) {
			return fmt.Errorf("the caps of workflow %q must not be negative", w.WorkflowID)
		}
	}
	return nil
}

func validAmount(v float64) bool {
	return v >= 0 && !math.IsInf(v, 0) && !math.IsNaN(v)
}

// AIUsage is what one scope spent in one month: the vhost as a whole when
// WorkflowID is empty, one workflow otherwise.
type AIUsage struct {
	VHost        string `json:"vhost"`
	Period       string `json:"period"`
	WorkflowID   string `json:"workflow_id,omitempty"`
	Calls        int64  `json:"calls"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CostMicros   int64  `json:"cost_micros"`
	// TokensWarned and CostWarned record that the 80% alert went out.
	TokensWarned bool      `json:"tokens_warned,omitempty"`
	CostWarned   bool      `json:"cost_warned,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitzero"`
}

// Tokens is input and output together, which is what a token limit counts.
func (u AIUsage) Tokens() int64 { return u.InputTokens + u.OutputTokens }

// AIUsageDelta is one call's addition to a usage row.
type AIUsageDelta struct {
	InputTokens  int64
	OutputTokens int64
	CostMicros   int64
}

// Which 80% alert MarkAIUsageWarned records.
const (
	AIWarnTokens = "tokens"
	AIWarnCost   = "cost"
)

// AIUsagePeriod is the usage month t falls in: its calendar month in UTC.
func AIUsagePeriod(t time.Time) string {
	return t.UTC().Format("2006-01")
}

// AIUsageID is a usage row's key. The vhost's length is in it, so no vhost
// and workflow pair can spell another's key whatever they contain.
func AIUsageID(vhost, period, workflowID string) string {
	return fmt.Sprintf("%s/%d/%s/%s", period, len(vhost), vhost, workflowID)
}

// AIBudgetStore is implemented by a storage backend that can hold AI budgets
// and usage. Like MLModelStore it is separate from Storage; callers find it
// with a type assertion.
type AIBudgetStore interface {
	// GetAIBudget returns the vhost's budget, or ErrNotFound.
	GetAIBudget(ctx context.Context, vhost string) (AIBudget, error)
	// PutAIBudget creates or replaces the vhost's budget.
	PutAIBudget(ctx context.Context, b AIBudget) error
	// AddAIUsage adds d to a scope's usage for the period atomically, creating
	// the row on first use, and returns the totals after the addition.
	AddAIUsage(ctx context.Context, vhost, period, workflowID string, d AIUsageDelta) (AIUsage, error)
	// GetAIUsage returns a scope's usage for the period; zero when it has none.
	GetAIUsage(ctx context.Context, vhost, period, workflowID string) (AIUsage, error)
	// ListAIUsage returns every scope of the vhost with usage in the period,
	// the vhost's own row (no workflow) first and then by workflow id.
	ListAIUsage(ctx context.Context, vhost, period string) ([]AIUsage, error)
	// MarkAIUsageWarned records that kind's 80% alert went out for a scope
	// and period. It reports true to exactly one caller however many race,
	// replicas included, so the alert is raised once.
	MarkAIUsageWarned(ctx context.Context, vhost, period, workflowID, kind string) (bool, error)
	// DeleteAIBudgets removes the vhost's budget and all of its usage.
	DeleteAIBudgets(ctx context.Context, vhost string) error
}
