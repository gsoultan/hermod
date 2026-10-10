package storage

import (
	"context"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// MLGoLive says whether a newly trained version is put live: "always",
// "never" (the zero value), or "if" one of its metrics is within Min and Max.
// internal/ml decides with it; it is here so a retrain policy can hold one.
type MLGoLive struct {
	Mode string `json:"mode"`
	// Metric is the metric "if" checks; empty means "score", which is
	// accuracy for a classifier and R² for a regression.
	Metric string   `json:"metric,omitempty"`
	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`
}

// MLRetrainPolicy is when a model Hermod trained trains again by itself, and
// how: on a cron schedule, after NewRows rows were added to its dataset since
// it last trained, or either, whichever comes first.
type MLRetrainPolicy struct {
	// Schedule is a five-field cron expression, or a descriptor such as
	// "@daily". Empty means no schedule.
	Schedule string `json:"schedule,omitempty"`
	// NewRows trains again once the dataset holds this many rows more than
	// when the model last trained. Zero means no row trigger.
	NewRows int `json:"new_rows,omitempty"`

	Spec   worker.TrainSpec `json:"spec"`
	GoLive MLGoLive         `json:"go_live"`

	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MLRetrainStatus is what a model's last retraining did, and what its next
// one counts new rows from.
type MLRetrainStatus struct {
	// At is when the last retraining ran; zero until one has. Trigger is
	// "schedule" or "new_rows".
	At      time.Time `json:"at"`
	Trigger string    `json:"trigger,omitempty"`
	// Version is the version it made, Live whether that version went live and
	// Reason why, in words. Error is set instead when it failed.
	Version string `json:"version,omitempty"`
	Live    bool   `json:"live"`
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`

	// DatasetRows is how many rows the dataset held when the model last
	// trained, by any trigger, and TrainedAt when that was.
	DatasetRows int       `json:"dataset_rows"`
	TrainedAt   time.Time `json:"trained_at"`
}

// MLRetrainStore is implemented by a storage backend that keeps retrain
// policies with its models. Like MLModelStore, callers find it with a type
// assertion.
type MLRetrainStore interface {
	// ListRetrainingMLModels returns every model, of every vhost, that has a
	// retrain policy.
	ListRetrainingMLModels(ctx context.Context) ([]MLModel, error)
	// SetMLModelRetrain stores the model's retrain policy, or clears it when
	// p is nil. It returns ErrNotFound for no such model.
	SetMLModelRetrain(ctx context.Context, vhost, name string, p *MLRetrainPolicy) error
	// SetMLModelRetrainStatus stores what the model's last training did. It
	// returns ErrNotFound for no such model.
	SetMLModelRetrainStatus(ctx context.Context, vhost, name string, st MLRetrainStatus) error
	// ClaimMLModelTraining marks the model as being trained by owner for ttl,
	// and reports whether it could: false while another owner's claim has not
	// expired. The owner may claim again to extend its own. It returns
	// ErrNotFound for no such model.
	ClaimMLModelTraining(ctx context.Context, vhost, name, owner string, ttl time.Duration) (bool, error)
	// ReleaseMLModelTraining ends owner's claim; anyone else's is left alone.
	ReleaseMLModelTraining(ctx context.Context, vhost, name, owner string) error
}
