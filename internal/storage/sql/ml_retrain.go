package sql

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

func (s *sqlStorage) ListRetrainingMLModels(ctx context.Context) ([]storage.MLModel, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListRetrainingMLModels))
	if err != nil {
		return nil, fmt.Errorf("listing models that retrain: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []storage.MLModel
	for rows.Next() {
		var m storage.MLModel
		var r mlModelRow
		if err := rows.Scan(append([]any{&m.VHost, &m.Name}, r.targets()...)...); err != nil {
			return nil, fmt.Errorf("listing models that retrain: %w", err)
		}
		if err := r.fill(&m); err != nil {
			return nil, err
		}
		// An empty string is how a dialect that cannot tell it from NULL may
		// hand back a cleared policy.
		if m.Retrain != nil {
			out = append(out, m)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing models that retrain: %w", err)
	}
	return out, nil
}

func (s *sqlStorage) SetMLModelRetrain(ctx context.Context, vhost, name string, p *storage.MLRetrainPolicy) error {
	var value any // NULL clears the policy
	if p != nil {
		raw, err := json.Marshal(p)
		if err != nil {
			return fmt.Errorf("encoding the retrain policy of model %q: %w", name, err)
		}
		value = string(raw)
	}
	return s.setMLModelColumn(ctx, QuerySetMLModelRetrain, vhost, name, value, "retrain policy")
}

func (s *sqlStorage) SetMLModelScoring(ctx context.Context, vhost, name, scoring string) error {
	if err := storage.ValidateMLScoring(scoring); err != nil {
		return err
	}
	var value any // NULL is the default, the worker
	if scoring != "" {
		value = scoring
	}
	return s.setMLModelColumn(ctx, QuerySetMLModelScoring, vhost, name, value, "scoring")
}

func (s *sqlStorage) SetMLModelRetrainStatus(ctx context.Context, vhost, name string, st storage.MLRetrainStatus) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("encoding the retrain status of model %q: %w", name, err)
	}
	return s.setMLModelColumn(ctx, QuerySetMLModelRetrainStatus, vhost, name, string(raw), "retrain status")
}

func (s *sqlStorage) setMLModelColumn(ctx context.Context, query, vhost, name string, value any, what string) error {
	res, err := s.exec(ctx, s.queries.get(query), value, mlModelID(vhost, name))
	if err != nil {
		return fmt.Errorf("saving the %s of model %q of vhost %q: %w", what, name, vhost, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// ClaimMLModelTraining is AcquireWorkflowLease on a model. A claim that takes
// no row is either held by someone else or for a model that does not exist;
// the second is told apart with a read, so the caller can say which.
func (s *sqlStorage) ClaimMLModelTraining(ctx context.Context, vhost, name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	res, err := s.exec(ctx, s.queries.get(QueryClaimMLModelTraining),
		owner, now.Add(ttl), mlModelID(vhost, name), now, owner)
	if err != nil {
		return false, fmt.Errorf("claiming model %q of vhost %q for training: %w", name, vhost, err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return true, nil
	}
	if _, err := s.GetMLModel(ctx, vhost, name); err != nil {
		return false, err
	}
	return false, nil
}

func (s *sqlStorage) ReleaseMLModelTraining(ctx context.Context, vhost, name, owner string) error {
	if _, err := s.exec(ctx, s.queries.get(QueryReleaseMLModelTraining), mlModelID(vhost, name), owner); err != nil {
		return fmt.Errorf("releasing model %q of vhost %q after training: %w", name, vhost, err)
	}
	return nil
}
