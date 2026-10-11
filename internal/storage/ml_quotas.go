package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// MLQuotas are the machine-learning limits one vhost has been given. A nil
// field is not set for the vhost, so the server-wide default applies (see
// internal/ml); zero means no limit; a positive value is the limit.
type MLQuotas struct {
	VHost string `json:"vhost"`

	// MaxDatasets bounds how many datasets the vhost holds.
	MaxDatasets *int64 `json:"max_datasets"`
	// MaxDatasetRows bounds the rows one dataset holds.
	MaxDatasetRows *int64 `json:"max_dataset_rows"`
	// MaxDatasetBytes bounds what one fill of a dataset sends to the ML
	// worker: the file as uploaded, or the rows of a query as JSON.
	MaxDatasetBytes *int64 `json:"max_dataset_bytes"`
	// MaxModels bounds how many models the vhost registers or trains.
	MaxModels *int64 `json:"max_models"`
	// MaxConcurrentTrainings bounds the vhost's trainings running at once.
	MaxConcurrentTrainings *int64 `json:"max_concurrent_trainings"`
	// MaxPredictionsPerSecond bounds the rows the vhost's models are sent per
	// second.
	MaxPredictionsPerSecond *float64 `json:"max_predictions_per_second"`

	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ErrMLQuotasUnsupported is returned when the configured backend cannot store
// quotas.
var ErrMLQuotasUnsupported = errors.New("this storage backend cannot hold ML quotas")

// ValidateMLQuotas reports what is wrong with a vhost's quotas before they
// are saved.
func ValidateMLQuotas(q MLQuotas) error {
	if q.VHost == "" {
		return errors.New("quotas belong to one vhost: name it")
	}
	for name, v := range map[string]*int64{
		"max_datasets": q.MaxDatasets, "max_dataset_rows": q.MaxDatasetRows, "max_dataset_bytes": q.MaxDatasetBytes,
		"max_models": q.MaxModels, "max_concurrent_trainings": q.MaxConcurrentTrainings,
	} {
		if v != nil && *v < 0 {
			return fmt.Errorf("%s cannot be negative; 0 means no limit", name)
		}
	}
	if p := q.MaxPredictionsPerSecond; p != nil && (*p < 0 || math.IsNaN(*p) || math.IsInf(*p, 0)) {
		return errors.New("max_predictions_per_second must be a number, 0 or more; 0 means no limit")
	}
	return nil
}

// MLQuotaStore is implemented by a storage backend that can hold quotas per
// vhost. Like MLModelStore it is separate from Storage; callers find it with
// a type assertion.
type MLQuotaStore interface {
	// GetMLQuotas returns the vhost's quotas, or ErrNotFound when none are set.
	GetMLQuotas(ctx context.Context, vhost string) (MLQuotas, error)
	// PutMLQuotas replaces the vhost's quotas.
	PutMLQuotas(ctx context.Context, q MLQuotas) error
	// DeleteMLQuotas removes the vhost's quotas; none is not an error.
	DeleteMLQuotas(ctx context.Context, vhost string) error
}
