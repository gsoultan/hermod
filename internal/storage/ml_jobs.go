// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// The kinds of an ML job: what it runs.
const (
	MLJobPredict   = "predict"
	MLJobRecommend = "recommend"
	MLJobTrain     = "train"
)

// The states of an ML job. A job starts queued, is running while a Hermod
// holds its claim, and ends succeeded, failed or cancelled.
const (
	MLJobQueued    = "queued"
	MLJobRunning   = "running"
	MLJobSucceeded = "succeeded"
	MLJobFailed    = "failed"
	MLJobCancelled = "cancelled"
)

// The states of a job's callback. A job without a callback URL has none.
const (
	MLCallbackPending   = "pending"
	MLCallbackDelivered = "delivered"
	MLCallbackFailed    = "failed"
)

// MLJobResultStored is a job's ResultRef when its result was too large to
// keep on the job and is kept apart; GetMLJobResult reads it.
const MLJobResultStored = "stored"

// ErrMLJobsUnsupported is returned when the configured backend cannot store
// ML jobs.
var ErrMLJobsUnsupported = errors.New("this storage backend cannot hold ML jobs")

// ErrMLJobClaimLost is returned to a runner that no longer holds a job's
// claim: it expired and another Hermod took the job, or the job was removed.
var ErrMLJobClaimLost = errors.New("the claim on this ML job was lost")

// MLJobTerminal reports whether a job in status has finished.
func MLJobTerminal(status string) bool {
	return status == MLJobSucceeded || status == MLJobFailed || status == MLJobCancelled
}

// MLJob is one asynchronous call of a model: a prediction, recommendations
// or a training, queued by one Hermod and run by whichever claims it.
type MLJob struct {
	ID    string `json:"id"`
	VHost string `json:"vhost"`
	Model string `json:"model"`
	Kind  string `json:"kind"`

	Status string `json:"status"`
	// Progress is how far along the job is, 0 to 1, and ProgressMessage
	// says so in words.
	Progress        float64 `json:"progress"`
	ProgressMessage string  `json:"progress_message,omitempty"`

	// Request is what the job runs, as JSON; its shape depends on Kind.
	// Listings leave it out.
	Request json.RawMessage `json:"request,omitempty"`
	// Result is the answer, as JSON, when it is small enough to keep here.
	// ResultRef is MLJobResultStored when it is kept apart instead.
	// Listings leave both out.
	Result    json.RawMessage `json:"result,omitempty"`
	ResultRef string          `json:"result_ref,omitempty"`
	Error     string          `json:"error,omitempty"`

	// CallbackURL is told when the job ends. CallbackStatus is pending until
	// it has been, delivered or failed after; empty without a URL.
	CallbackURL       string    `json:"callback_url,omitempty"`
	CallbackStatus    string    `json:"callback_status,omitempty"`
	CallbackAttempts  int       `json:"callback_attempts,omitempty"`
	CallbackNextAt    time.Time `json:"-"`
	CallbackLastError string    `json:"callback_error,omitempty"`

	// CallerKind is who submitted it: one of the MLCaller kinds. A job
	// submitted with a serving key (MLCallerREST or MLCallerGRPC) is the
	// only kind that key may read. CreatedBy is the user, when there is one.
	CallerKind string `json:"caller_kind"`
	CreatedBy  string `json:"created_by,omitempty"`

	// CancelRequested asks the Hermod running the job to stop it.
	CancelRequested bool `json:"cancel_requested,omitempty"`
	// Attempts is how many times the job was claimed to run. More than one
	// means a Hermod running it stopped and another took it over.
	Attempts int `json:"attempts"`
	// ClaimedBy holds the job, while it runs or its callback is sent, until
	// ClaimUntil; a claim not renewed by then may be taken over.
	ClaimedBy  string    `json:"-"`
	ClaimUntil time.Time `json:"-"`

	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// ExpiresAt is when the job and its result are removed.
	ExpiresAt time.Time `json:"expires_at"`
}

// MLJobFilter selects a vhost's jobs. Model and Status are optional. Limit
// is the page size and Offset where the page starts.
type MLJobFilter struct {
	VHost  string
	Model  string
	Status string
	Limit  int
	Offset int
}

// MaxMLJobPage bounds one page of a job listing.
const MaxMLJobPage = 200

// MLJobFinish is how a job ended, as the runner holding it records it.
type MLJobFinish struct {
	Status    string
	Result    json.RawMessage
	ResultRef string
	Error     string
	At        time.Time
}

// MLJobStore is implemented by a storage backend that can hold ML jobs.
// Like MLModelStore it is separate from Storage; callers find it with a type
// assertion.
type MLJobStore interface {
	// CreateMLJob stores a new job as it is given.
	CreateMLJob(ctx context.Context, j MLJob) error
	// GetMLJob returns one job of the vhost, with its request and result, or
	// ErrNotFound.
	GetMLJob(ctx context.Context, vhost, id string) (MLJob, error)
	// ListMLJobs returns one page of the vhost's jobs, newest first, without
	// their requests and results, and how many match in all.
	ListMLJobs(ctx context.Context, f MLJobFilter) ([]MLJob, int, error)
	// ClaimMLJob takes the oldest job that is queued, or running under a
	// claim that expired before now, for owner until now+ttl, marks it
	// running and counts the attempt. It reports false when there is none.
	ClaimMLJob(ctx context.Context, owner string, ttl time.Duration, now time.Time) (MLJob, bool, error)
	// UpdateMLJobProgress records how far owner's running job is and renews
	// its claim until until. It reports whether cancelling the job was
	// asked for, and ErrMLJobClaimLost when owner no longer holds it.
	UpdateMLJobProgress(ctx context.Context, id, owner string, progress float64, message string, until time.Time) (bool, error)
	// FinishMLJob records how owner's job ended and releases it. A job with
	// a callback URL is then due for its callback. It returns
	// ErrMLJobClaimLost when owner no longer holds it.
	FinishMLJob(ctx context.Context, id, owner string, f MLJobFinish) error
	// CancelMLJob cancels a queued job at once, or asks the Hermod running a
	// running one to stop it, and returns the job. A finished job is
	// returned as it is. It returns ErrNotFound for no such job.
	CancelMLJob(ctx context.Context, vhost, id string, now time.Time) (MLJob, error)
	// ClaimMLJobCallback takes, for owner until now+ttl, a finished job whose
	// callback is pending and due. It reports false when there is none.
	ClaimMLJobCallback(ctx context.Context, owner string, ttl time.Duration, now time.Time) (MLJob, bool, error)
	// RecordMLJobCallback records one delivery attempt of owner's callback
	// and releases the job: status is pending again (with next, when to try
	// next), delivered or failed.
	RecordMLJobCallback(ctx context.Context, id, owner, status string, attempts int, next time.Time, lastError string) error
	// PutMLJobResult keeps a result too large for the job itself.
	PutMLJobResult(ctx context.Context, vhost, id string, result json.RawMessage) error
	// GetMLJobResult reads a result PutMLJobResult kept, or ErrNotFound.
	GetMLJobResult(ctx context.Context, vhost, id string) (json.RawMessage, error)
	// DeleteExpiredMLJobs removes every job, and its result, that expired
	// before now, and returns how many jobs went.
	DeleteExpiredMLJobs(ctx context.Context, now time.Time) (int, error)
	// DeleteMLJobs removes every job, and result, the vhost holds.
	DeleteMLJobs(ctx context.Context, vhost string) error
}
