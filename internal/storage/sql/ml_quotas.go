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

// mlQuotasSpec is what the spec column holds: the quotas without the vhost
// and bookkeeping, which have columns of their own.
type mlQuotasSpec struct {
	MaxDatasets             *int64   `json:"max_datasets,omitempty"`
	MaxDatasetRows          *int64   `json:"max_dataset_rows,omitempty"`
	MaxDatasetBytes         *int64   `json:"max_dataset_bytes,omitempty"`
	MaxModels               *int64   `json:"max_models,omitempty"`
	MaxConcurrentTrainings  *int64   `json:"max_concurrent_trainings,omitempty"`
	MaxPredictionsPerSecond *float64 `json:"max_predictions_per_second,omitempty"`
}

func (s *sqlStorage) GetMLQuotas(ctx context.Context, vhost string) (storage.MLQuotas, error) {
	var spec, by sql.NullString
	var updated sql.NullTime
	err := s.queryRow(ctx, s.queries.get(QueryGetMLQuotas), vhost).Scan(&spec, &by, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.MLQuotas{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.MLQuotas{}, fmt.Errorf("reading the ML quotas of vhost %q: %w", vhost, err)
	}
	var sp mlQuotasSpec
	if spec.String != "" {
		if err := json.Unmarshal([]byte(spec.String), &sp); err != nil {
			return storage.MLQuotas{}, fmt.Errorf("the ML quotas of vhost %q are unreadable: %w", vhost, err)
		}
	}
	return storage.MLQuotas{
		VHost: vhost, MaxDatasets: sp.MaxDatasets, MaxDatasetRows: sp.MaxDatasetRows, MaxDatasetBytes: sp.MaxDatasetBytes,
		MaxModels: sp.MaxModels, MaxConcurrentTrainings: sp.MaxConcurrentTrainings, MaxPredictionsPerSecond: sp.MaxPredictionsPerSecond,
		UpdatedBy: by.String, UpdatedAt: updated.Time,
	}, nil
}

// PutMLQuotas updates the vhost's row and inserts it when there is none, as
// PutMLModel does.
func (s *sqlStorage) PutMLQuotas(ctx context.Context, q storage.MLQuotas) error {
	if err := storage.ValidateMLQuotas(q); err != nil {
		return err
	}
	spec, err := json.Marshal(mlQuotasSpec{
		MaxDatasets: q.MaxDatasets, MaxDatasetRows: q.MaxDatasetRows, MaxDatasetBytes: q.MaxDatasetBytes,
		MaxModels: q.MaxModels, MaxConcurrentTrainings: q.MaxConcurrentTrainings, MaxPredictionsPerSecond: q.MaxPredictionsPerSecond,
	})
	if err != nil {
		return fmt.Errorf("encoding the ML quotas of vhost %q: %w", q.VHost, err)
	}
	now := time.Now().UTC()
	update := func() (bool, error) {
		res, err := s.exec(ctx, s.queries.get(QueryUpdateMLQuotas), string(spec), q.UpdatedBy, now, q.VHost)
		if err != nil {
			return false, fmt.Errorf("saving the ML quotas of vhost %q: %w", q.VHost, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("saving the ML quotas of vhost %q: %w", q.VHost, err)
		}
		return n > 0, nil
	}
	updated, err := update()
	if err != nil || updated {
		return err
	}
	if _, err := s.exec(ctx, s.queries.get(QueryInsertMLQuotas), q.VHost, string(spec), q.UpdatedBy, now); err != nil {
		// Another writer inserted the row first: update it instead.
		if updated, retryErr := update(); retryErr == nil && updated {
			return nil
		}
		return fmt.Errorf("saving the ML quotas of vhost %q: %w", q.VHost, err)
	}
	return nil
}

func (s *sqlStorage) DeleteMLQuotas(ctx context.Context, vhost string) error {
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteMLQuotas), vhost); err != nil {
		return fmt.Errorf("deleting the ML quotas of vhost %q: %w", vhost, err)
	}
	return nil
}
