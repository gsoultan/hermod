// Package aibudget enforces a vhost's AI spending policy: the kill switch,
// the monthly token and cost budget of the vhost, and the monthly caps of
// single workflows.
//
// Service is an llm.Budget. The registry installs it with genai.SetBudget, so
// every model call an AI node or the agent makes is checked before it is sent
// and counted after it returns, against usage kept in the metadata store and
// so shared by every replica. A worker without a database asks the control
// plane instead (Remote).
//
// Limits fail closed: a call over a limit, or one whose budget cannot be read,
// is refused with an *llm.BudgetError, which the node returns as its error.
// The check comes before the call and the usage is only known after it, so the
// call that crosses a limit completes, and a budget can be overshot by the
// calls in flight when it ran out.
package aibudget

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/notification"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
	"github.com/gsoultan/hermod/pkg/engine/telemetry/aimetrics"
	"github.com/gsoultan/hermod/pkg/llm"
)

// Alert levels, as the notification service files them.
const (
	LevelWarn  = notification.LevelWarn
	LevelError = notification.LevelError
)

// DefaultVHost is the vhost a call with none is counted against, as a
// workflow with no vhost belongs to it.
const DefaultVHost = "default"

// cacheTTL is how long a replica uses a budget it has read before reading it
// again: how long a change saved on another replica can take to apply here.
// A save on this replica applies at once (Invalidate).
const cacheTTL = 5 * time.Second

// recordTimeout bounds counting one call. It runs on a context detached from
// the call's, which a finished or cancelled node may already have ended: the
// tokens were spent either way.
const recordTimeout = 5 * time.Second

// blockLogInterval bounds the log line for refused calls to one per vhost and
// limit per interval; the metric counts every one.
const blockLogInterval = time.Minute

// maxBlockKeys bounds the memory of when each refusal was last logged.
const maxBlockKeys = 4096

// warnRatio is when the alert goes out: at 80% of a limit.
const (
	warnNumerator   = 4
	warnDenominator = 5
)

// Notifier raises an alert; notification.Service is one.
type Notifier interface {
	NotifyLevel(ctx context.Context, level, title, message string, wf storage.Workflow)
}

// Remote is a worker's storage, which reaches the budget through the control
// plane's API rather than a database.
type Remote interface {
	CheckAIBudget(ctx context.Context, vhost, workflowID string) error
	RecordAIUsage(ctx context.Context, vhost, workflowID string, rec llm.CallRecord) error
}

type cachedBudget struct {
	budget  storage.AIBudget
	found   bool
	expires time.Time
}

// Service checks and counts model calls against the vhosts' budgets.
type Service struct {
	store  func() any
	notify Notifier
	logger hermod.Logger
	now    func() time.Time

	mu        sync.Mutex
	cache     map[string]cachedBudget
	lastBlock map[string]time.Time
}

var _ llm.Budget = (*Service)(nil)

// NewService reads budgets from whatever store returns at the time of each
// call, since the registry's store can be replaced while it runs. notify and
// logger may be nil.
func NewService(store func() any, notify Notifier, logger hermod.Logger) *Service {
	return &Service{
		store: store, notify: notify, logger: logger, now: time.Now,
		cache: map[string]cachedBudget{}, lastBlock: map[string]time.Time{},
	}
}

func normalise(vhost string) string {
	if vhost == "" {
		return DefaultVHost
	}
	return vhost
}

// Allow implements llm.Budget for the scope the call's context carries.
func (s *Service) Allow(ctx context.Context) error {
	scope := llm.ScopeFrom(ctx)
	return s.Check(ctx, scope.VHost, scope.WorkflowID)
}

// Record implements llm.Budget for the scope the call's context carries.
func (s *Service) Record(ctx context.Context, rec llm.CallRecord) {
	scope := llm.ScopeFrom(ctx)
	s.RecordUsage(ctx, scope.VHost, scope.WorkflowID, rec)
}

// Check returns nil when a call by workflowID in vhost may go ahead, and an
// *llm.BudgetError when it may not.
func (s *Service) Check(ctx context.Context, vhost, workflowID string) error {
	vhost = normalise(vhost)
	var err error
	local := false
	switch st := s.store().(type) {
	case storage.AIBudgetStore:
		err, local = s.checkLocal(ctx, st, vhost, workflowID), true
	case Remote:
		err = st.CheckAIBudget(ctx, vhost, workflowID)
	default:
		return nil
	}
	var be *llm.BudgetError
	if errors.As(err, &be) {
		s.blocked(ctx, be, local)
	}
	return err
}

func (s *Service) checkLocal(ctx context.Context, st storage.AIBudgetStore, vhost, workflowID string) error {
	b, found, err := s.budget(ctx, st, vhost)
	if err != nil {
		return &llm.BudgetError{Limit: llm.LimitUnavailable, VHost: vhost, WorkflowID: workflowID, Cause: err}
	}
	if !found || !b.Limited() {
		return nil
	}
	if b.Disabled {
		return &llm.BudgetError{Limit: llm.LimitDisabled, VHost: vhost, WorkflowID: workflowID}
	}
	period := storage.AIUsagePeriod(s.now())
	if b.MonthlyTokens > 0 || b.MonthlyCost > 0 {
		u, err := st.GetAIUsage(ctx, vhost, period, "")
		if err != nil {
			return &llm.BudgetError{Limit: llm.LimitUnavailable, VHost: vhost, WorkflowID: workflowID, Cause: err}
		}
		if err := over(u, b.MonthlyTokens, b.MonthlyCost, llm.LimitVHostTokens, llm.LimitVHostCost); err != nil {
			err.VHost, err.WorkflowID = vhost, workflowID
			return err
		}
	}
	if c, ok := b.WorkflowCap(workflowID); ok && (c.MonthlyTokens > 0 || c.MonthlyCost > 0) {
		u, err := st.GetAIUsage(ctx, vhost, period, workflowID)
		if err != nil {
			return &llm.BudgetError{Limit: llm.LimitUnavailable, VHost: vhost, WorkflowID: workflowID, Cause: err}
		}
		if err := over(u, c.MonthlyTokens, c.MonthlyCost, llm.LimitWorkflowTokens, llm.LimitWorkflowCost); err != nil {
			err.VHost, err.WorkflowID = vhost, workflowID
			return err
		}
	}
	return nil
}

// over reports which of the two limits u has reached, if either.
func over(u storage.AIUsage, tokens int64, cost float64, tokenLimit, costLimit llm.Limit) *llm.BudgetError {
	if tokens > 0 && u.Tokens() >= tokens {
		return &llm.BudgetError{Limit: tokenLimit, Used: u.Tokens(), Max: tokens}
	}
	if maxCost := storage.CostToMicros(cost); maxCost > 0 && u.CostMicros >= maxCost {
		return &llm.BudgetError{Limit: costLimit, Used: u.CostMicros, Max: maxCost}
	}
	return nil
}

// RecordUsage counts a finished call against vhost and, when one is named,
// workflowID, priced with the vhost's prices, and raises the 80% alerts.
func (s *Service) RecordUsage(ctx context.Context, vhost, workflowID string, rec llm.CallRecord) {
	vhost = normalise(vhost)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	switch st := s.store().(type) {
	case storage.AIBudgetStore:
		s.recordLocal(ctx, st, vhost, workflowID, rec)
	case Remote:
		if err := st.RecordAIUsage(ctx, vhost, workflowID, rec); err != nil {
			s.logError("AI usage could not be sent to the control plane; this call is not counted", vhost, workflowID, err)
		}
	}
}

func (s *Service) recordLocal(ctx context.Context, st storage.AIBudgetStore, vhost, workflowID string, rec llm.CallRecord) {
	// An unreadable budget still counts the tokens; it only cannot price them.
	b, _, err := s.budget(ctx, st, vhost)
	if err != nil {
		s.logError("AI budget unreadable while counting a call; its cost is not counted", vhost, workflowID, err)
	}
	d := storage.AIUsageDelta{
		InputTokens:  rec.Usage.InputTokens,
		OutputTokens: rec.Usage.OutputTokens,
		CostMicros:   b.CostMicros(rec.Model, rec.Usage.InputTokens, rec.Usage.OutputTokens),
	}
	period := storage.AIUsagePeriod(s.now())
	u, err := st.AddAIUsage(ctx, vhost, period, "", d)
	if err != nil {
		s.logError("AI usage could not be counted", vhost, workflowID, err)
	} else {
		s.warn(ctx, st, b, u, b.MonthlyTokens, b.MonthlyCost)
	}
	if workflowID == "" {
		return
	}
	wu, err := st.AddAIUsage(ctx, vhost, period, workflowID, d)
	if err != nil {
		s.logError("AI usage of a workflow could not be counted", vhost, workflowID, err)
		return
	}
	if c, ok := b.WorkflowCap(workflowID); ok {
		s.warn(ctx, st, b, wu, c.MonthlyTokens, c.MonthlyCost)
	}
}

// warn raises the 80% alert for u's scope once per limit and month. The
// store's mark decides which caller raises it, so replicas do not repeat it.
func (s *Service) warn(ctx context.Context, st storage.AIBudgetStore, b storage.AIBudget, u storage.AIUsage, tokens int64, cost float64) {
	type check struct {
		kind        string
		used, limit int64
		warned      bool
	}
	for _, c := range []check{
		{storage.AIWarnTokens, u.Tokens(), tokens, u.TokensWarned},
		{storage.AIWarnCost, u.CostMicros, storage.CostToMicros(cost), u.CostWarned},
	} {
		if c.limit <= 0 || c.warned || c.used*warnDenominator < c.limit*warnNumerator {
			continue
		}
		first, err := st.MarkAIUsageWarned(ctx, u.VHost, u.Period, u.WorkflowID, c.kind)
		if err != nil {
			s.logError("the AI budget alert could not be recorded", u.VHost, u.WorkflowID, err)
			continue
		}
		if !first {
			continue
		}
		scope, who := "vhost", "vhost "+u.VHost
		if u.WorkflowID != "" {
			scope, who = "workflow", "workflow "+u.WorkflowID+" in vhost "+u.VHost
		}
		telemetry.AIBudgetWarnings.WithLabelValues(aimetrics.Label(u.VHost), scope, c.kind).Inc()
		title := "AI budget at 80%: " + who
		message := fmt.Sprintf("%s has used %s of its monthly %s limit of %s for %s. At 100%% its AI calls are refused.",
			who, amount(c.kind, c.used, b.Currency), c.kind, amount(c.kind, c.limit, b.Currency), u.Period)
		if s.logger != nil {
			s.logger.Warn("AI budget at 80%", "vhost", u.VHost, "workflow_id", u.WorkflowID, "kind", c.kind,
				"used", c.used, "limit", c.limit, "period", u.Period)
		}
		if s.notify != nil {
			s.notify.NotifyLevel(ctx, LevelWarn, title, message, storage.Workflow{ID: u.WorkflowID, Name: u.WorkflowID, VHost: u.VHost})
		}
	}
}

func amount(kind string, v int64, currency string) string {
	if kind == storage.AIWarnCost {
		s := fmt.Sprintf("%.2f", float64(v)/1e6)
		if currency != "" {
			s += " " + currency
		}
		return s
	}
	return fmt.Sprintf("%d tokens", v)
}

// blocked counts a refused call, and logs and alerts at most once per vhost
// and limit per blockLogInterval: a busy workflow over its budget refuses
// every message, and one line says as much as a thousand. A worker's refusal
// is alerted by the control plane that made it (local is false), not twice.
func (s *Service) blocked(ctx context.Context, be *llm.BudgetError, local bool) {
	telemetry.AIBudgetBlocked.WithLabelValues(aimetrics.Label(be.VHost), string(be.Limit)).Inc()
	key := be.VHost + "\x00" + be.WorkflowID + "\x00" + string(be.Limit)
	now := s.now()
	s.mu.Lock()
	last, seen := s.lastBlock[key]
	if seen && now.Sub(last) < blockLogInterval {
		s.mu.Unlock()
		return
	}
	if len(s.lastBlock) >= maxBlockKeys {
		clear(s.lastBlock)
	}
	s.lastBlock[key] = now
	s.mu.Unlock()

	if s.logger != nil {
		s.logger.Warn("AI call refused by the vhost's AI budget", "vhost", be.VHost, "workflow_id", be.WorkflowID,
			"limit", string(be.Limit), "error", be.Error())
	}
	// The kill switch is an operator's choice, not news; only a spent budget
	// or an unreadable one is worth an alert.
	if local && s.notify != nil && be.Limit != llm.LimitDisabled {
		s.notify.NotifyLevel(ctx, LevelError, "AI budget reached: vhost "+be.VHost, be.Error(),
			storage.Workflow{ID: be.WorkflowID, Name: be.WorkflowID, VHost: be.VHost})
	}
}

func (s *Service) logError(msg, vhost, workflowID string, err error) {
	if s.logger != nil {
		s.logger.Error(msg, "vhost", vhost, "workflow_id", workflowID, "error", err.Error())
	}
}

// budget returns the vhost's budget, from the cache while it is fresh.
// found is false when the vhost has none.
func (s *Service) budget(ctx context.Context, st storage.AIBudgetStore, vhost string) (storage.AIBudget, bool, error) {
	now := s.now()
	s.mu.Lock()
	c, ok := s.cache[vhost]
	s.mu.Unlock()
	if ok && now.Before(c.expires) {
		return c.budget, c.found, nil
	}
	b, err := st.GetAIBudget(ctx, vhost)
	found := err == nil
	if errors.Is(err, storage.ErrNotFound) {
		err = nil
	}
	if err != nil {
		return storage.AIBudget{}, false, err
	}
	s.mu.Lock()
	s.cache[vhost] = cachedBudget{budget: b, found: found, expires: now.Add(cacheTTL)}
	s.mu.Unlock()
	return b, found, nil
}

// Invalidate drops this replica's cached budget for vhost, so a save applies
// to the next call here rather than when the cache expires.
func (s *Service) Invalidate(vhost string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, normalise(vhost))
}

// Period is the usage month now: the current calendar month in UTC.
func (s *Service) Period() string {
	return storage.AIUsagePeriod(s.now())
}

// Report is a vhost's budget and what it has spent this month.
type Report struct {
	VHost  string           `json:"vhost"`
	Period string           `json:"period"`
	Budget storage.AIBudget `json:"budget"`
	// Usage is the vhost's total; Workflows the workflows that spent anything.
	Usage     storage.AIUsage   `json:"usage"`
	Workflows []storage.AIUsage `json:"workflows"`
}

// Report reads the vhost's budget and this month's usage.
func (s *Service) Report(ctx context.Context, vhost string) (Report, error) {
	vhost = normalise(vhost)
	st, ok := s.store().(storage.AIBudgetStore)
	if !ok {
		return Report{}, storage.ErrAIBudgetsUnsupported
	}
	period := storage.AIUsagePeriod(s.now())
	r := Report{VHost: vhost, Period: period, Budget: storage.AIBudget{VHost: vhost},
		Usage: storage.AIUsage{VHost: vhost, Period: period}, Workflows: []storage.AIUsage{}}
	b, err := st.GetAIBudget(ctx, vhost)
	switch {
	case err == nil:
		r.Budget = b
	case !errors.Is(err, storage.ErrNotFound):
		return Report{}, err
	}
	list, err := st.ListAIUsage(ctx, vhost, period)
	if err != nil {
		return Report{}, err
	}
	for _, u := range list {
		if u.WorkflowID == "" {
			r.Usage = u
		} else {
			r.Workflows = append(r.Workflows, u)
		}
	}
	return r, nil
}

// Save validates and stores a vhost's budget, and applies it on this replica
// at once.
func (s *Service) Save(ctx context.Context, b storage.AIBudget) error {
	st, ok := s.store().(storage.AIBudgetStore)
	if !ok {
		return storage.ErrAIBudgetsUnsupported
	}
	b.VHost = normalise(b.VHost)
	if err := st.PutAIBudget(ctx, b); err != nil {
		return err
	}
	s.Invalidate(b.VHost)
	return nil
}
