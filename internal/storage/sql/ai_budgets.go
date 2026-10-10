package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// aiBudgetSpec is what the spec column holds: the budget without the vhost
// and bookkeeping, which have columns of their own.
type aiBudgetSpec struct {
	Disabled      bool                    `json:"disabled,omitempty"`
	MonthlyTokens int64                   `json:"monthly_tokens,omitempty"`
	MonthlyCost   float64                 `json:"monthly_cost,omitempty"`
	Currency      string                  `json:"currency,omitempty"`
	Prices        []storage.AIModelPrice  `json:"prices,omitempty"`
	Workflows     []storage.AIWorkflowCap `json:"workflows,omitempty"`
}

func (s *sqlStorage) GetAIBudget(ctx context.Context, vhost string) (storage.AIBudget, error) {
	var spec, by sql.NullString
	var updated sql.NullTime
	err := s.queryRow(ctx, s.queries.get(QueryGetAIBudget), vhost).Scan(&spec, &by, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.AIBudget{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.AIBudget{}, fmt.Errorf("reading the AI budget of vhost %q: %w", vhost, err)
	}
	var sp aiBudgetSpec
	if spec.String != "" {
		if err := json.Unmarshal([]byte(spec.String), &sp); err != nil {
			return storage.AIBudget{}, fmt.Errorf("the AI budget of vhost %q is unreadable: %w", vhost, err)
		}
	}
	return storage.AIBudget{
		VHost: vhost, Disabled: sp.Disabled, MonthlyTokens: sp.MonthlyTokens, MonthlyCost: sp.MonthlyCost,
		Currency: sp.Currency, Prices: sp.Prices, Workflows: sp.Workflows,
		UpdatedBy: by.String, UpdatedAt: updated.Time,
	}, nil
}

// PutAIBudget updates the row if the vhost has one and inserts it otherwise,
// the update-then-insert PutMLModel uses for the same reason.
func (s *sqlStorage) PutAIBudget(ctx context.Context, b storage.AIBudget) error {
	if err := storage.ValidateAIBudget(b); err != nil {
		return err
	}
	raw, err := json.Marshal(aiBudgetSpec{
		Disabled: b.Disabled, MonthlyTokens: b.MonthlyTokens, MonthlyCost: b.MonthlyCost,
		Currency: b.Currency, Prices: b.Prices, Workflows: b.Workflows,
	})
	if err != nil {
		return fmt.Errorf("encoding the AI budget of vhost %q: %w", b.VHost, err)
	}
	now := time.Now().UTC()
	update := func() (bool, error) {
		res, err := s.exec(ctx, s.queries.get(QueryUpdateAIBudget), string(raw), b.UpdatedBy, now, b.VHost)
		if err != nil {
			return false, fmt.Errorf("saving the AI budget of vhost %q: %w", b.VHost, err)
		}
		n, err := res.RowsAffected()
		return n > 0, err
	}
	if updated, err := update(); err != nil || updated {
		return err
	}
	if _, err := s.exec(ctx, s.queries.get(QueryInsertAIBudget), b.VHost, string(raw), b.UpdatedBy, now); err != nil {
		if updated, retryErr := update(); retryErr == nil && updated {
			return nil
		}
		return fmt.Errorf("saving the AI budget of vhost %q: %w", b.VHost, err)
	}
	return nil
}

// AddAIUsage adds to the scope's row with a single UPDATE, so concurrent
// callers on any replica never overwrite each other. The first call of a
// month inserts the row; when two race to insert it, the loser adds to the
// row the winner made.
func (s *sqlStorage) AddAIUsage(ctx context.Context, vhost, period, workflowID string, d storage.AIUsageDelta) (storage.AIUsage, error) {
	id := storage.AIUsageID(vhost, period, workflowID)
	now := time.Now().UTC()
	add := func() (bool, error) {
		var n int64
		err := s.execWithRetry(ctx, func() error {
			res, err := s.exec(ctx, s.queries.get(QueryAddAIUsage), d.InputTokens, d.OutputTokens, d.CostMicros, now, id)
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			return err
		})
		return n > 0, err
	}
	added, err := add()
	if err == nil && !added {
		err = s.execWithRetry(ctx, func() error {
			_, err := s.exec(ctx, s.queries.get(QueryInsertAIUsage), id, vhost, period, workflowID,
				d.InputTokens, d.OutputTokens, d.CostMicros, now)
			return err
		})
		if err != nil {
			if added, retryErr := add(); retryErr == nil && added {
				err = nil
			}
		}
	}
	if err != nil {
		return storage.AIUsage{}, fmt.Errorf("adding to the AI usage of vhost %q: %w", vhost, err)
	}
	return s.GetAIUsage(ctx, vhost, period, workflowID)
}

// aiUsageRow is the counters and marks a usage query reads.
type aiUsageRow struct {
	calls, in, out, cost     sql.NullInt64
	tokensWarned, costWarned sql.NullTime
	updated                  sql.NullTime
}

func (r *aiUsageRow) targets() []any {
	return []any{&r.calls, &r.in, &r.out, &r.cost, &r.tokensWarned, &r.costWarned, &r.updated}
}

func (r *aiUsageRow) usage(vhost, period, workflowID string) storage.AIUsage {
	return storage.AIUsage{
		VHost: vhost, Period: period, WorkflowID: workflowID,
		Calls: r.calls.Int64, InputTokens: r.in.Int64, OutputTokens: r.out.Int64, CostMicros: r.cost.Int64,
		TokensWarned: r.tokensWarned.Valid, CostWarned: r.costWarned.Valid, UpdatedAt: r.updated.Time,
	}
}

func (s *sqlStorage) GetAIUsage(ctx context.Context, vhost, period, workflowID string) (storage.AIUsage, error) {
	var r aiUsageRow
	err := s.queryRow(ctx, s.queries.get(QueryGetAIUsage), storage.AIUsageID(vhost, period, workflowID)).Scan(r.targets()...)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.AIUsage{VHost: vhost, Period: period, WorkflowID: workflowID}, nil
	}
	if err != nil {
		return storage.AIUsage{}, fmt.Errorf("reading the AI usage of vhost %q: %w", vhost, err)
	}
	return r.usage(vhost, period, workflowID), nil
}

func (s *sqlStorage) ListAIUsage(ctx context.Context, vhost, period string) ([]storage.AIUsage, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListAIUsage), vhost, period)
	if err != nil {
		return nil, fmt.Errorf("listing the AI usage of vhost %q: %w", vhost, err)
	}
	defer func() { _ = rows.Close() }()

	var out []storage.AIUsage
	for rows.Next() {
		var wf sql.NullString
		var r aiUsageRow
		if err := rows.Scan(append([]any{&wf}, r.targets()...)...); err != nil {
			return nil, fmt.Errorf("listing the AI usage of vhost %q: %w", vhost, err)
		}
		out = append(out, r.usage(vhost, period, wf.String))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the AI usage of vhost %q: %w", vhost, err)
	}
	return out, nil
}

// MarkAIUsageWarned sets the alert's column only where it is still NULL, so
// of any number of callers exactly one changes a row and is told true.
func (s *sqlStorage) MarkAIUsageWarned(ctx context.Context, vhost, period, workflowID, kind string) (bool, error) {
	var key string
	switch kind {
	case storage.AIWarnTokens:
		key = QueryMarkAIUsageTokensWarned
	case storage.AIWarnCost:
		key = QueryMarkAIUsageCostWarned
	default:
		return false, fmt.Errorf("unknown AI budget alert %q", kind)
	}
	var n int64
	err := s.execWithRetry(ctx, func() error {
		res, err := s.exec(ctx, s.queries.get(key), time.Now().UTC(), storage.AIUsageID(vhost, period, workflowID))
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return false, fmt.Errorf("recording the AI budget alert of vhost %q: %w", vhost, err)
	}
	return n > 0, nil
}

func (s *sqlStorage) DeleteAIBudgets(ctx context.Context, vhost string) error {
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteAIBudget), vhost); err != nil {
		return fmt.Errorf("deleting the AI budget of vhost %q: %w", vhost, err)
	}
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteAIUsageOfVHost), vhost); err != nil {
		return fmt.Errorf("deleting the AI usage of vhost %q: %w", vhost, err)
	}
	return nil
}
