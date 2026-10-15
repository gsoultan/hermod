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
	"fmt"
	"slices"
	"time"
)

// The kinds of MLSchedule.
const (
	// MLScheduleDatasetRefresh refills a dataset from a database query.
	MLScheduleDatasetRefresh = "dataset_refresh"
	// MLScheduleModelBuild writes a recommendation model's answers for every
	// salesman, customer or product to a database table.
	MLScheduleModelBuild = "model_build"
)

// MLSchedule is something Hermod does by itself on a cron schedule for one
// dataset or model of a vhost: a dataset refresh or a model's build. Spec is
// the kind's own definition (internal/ml reads it); LastRun, LastRows and
// LastError are what its latest run did.
type MLSchedule struct {
	VHost    string          `json:"vhost"`
	Kind     string          `json:"kind"`
	Name     string          `json:"name"`
	Schedule string          `json:"schedule"`
	Spec     json.RawMessage `json:"spec"`

	LastRun   time.Time `json:"last_run"`
	LastRows  int       `json:"last_rows"`
	LastError string    `json:"last_error,omitempty"`

	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// maxMLScheduleSpec bounds a schedule's definition: a query and a target.
const maxMLScheduleSpec = 64 << 10

// ValidateMLSchedule reports what is wrong with a schedule before it is
// saved. The cron expression itself is checked by its user.
func ValidateMLSchedule(s MLSchedule) error {
	switch {
	case s.VHost == "":
		return errors.New("a schedule belongs to one vhost: name it")
	case s.Kind != MLScheduleDatasetRefresh && s.Kind != MLScheduleModelBuild:
		return fmt.Errorf("schedule kind %q is not %q or %q", s.Kind, MLScheduleDatasetRefresh, MLScheduleModelBuild)
	case s.Name == "" || len(s.Name) > 128:
		return errors.New("a schedule names its dataset or model")
	case s.Schedule == "":
		return errors.New("a schedule needs a cron expression")
	case len(s.Spec) > maxMLScheduleSpec:
		return fmt.Errorf("the schedule's definition is larger than %d KB", maxMLScheduleSpec>>10)
	}
	return nil
}

// MLScheduleStore is implemented by a storage backend that keeps ML
// schedules. Like MLModelStore, callers find it with a type assertion.
type MLScheduleStore interface {
	// ListMLSchedules returns every schedule of every vhost.
	ListMLSchedules(ctx context.Context) ([]MLSchedule, error)
	// GetMLSchedule returns one schedule, or ErrNotFound.
	GetMLSchedule(ctx context.Context, vhost, kind, name string) (MLSchedule, error)
	// PutMLSchedule saves a schedule's definition, keeping what its last run
	// did. UpdatedAt is set here.
	PutMLSchedule(ctx context.Context, s MLSchedule) error
	// DeleteMLSchedule removes a schedule, or returns ErrNotFound.
	DeleteMLSchedule(ctx context.Context, vhost, kind, name string) error
	// SetMLScheduleRun records what a run did. A schedule removed meanwhile
	// is not an error.
	SetMLScheduleRun(ctx context.Context, vhost, kind, name string, at time.Time, rows int, runErr string) error
	// ClaimMLSchedule marks the schedule as run by owner for ttl, and
	// reports whether it could: false while another owner's claim has not
	// expired, or for no such schedule. The owner may claim again.
	ClaimMLSchedule(ctx context.Context, vhost, kind, name, owner string, ttl time.Duration) (bool, error)
	// ReleaseMLSchedule ends owner's claim; anyone else's is left alone.
	ReleaseMLSchedule(ctx context.Context, vhost, kind, name, owner string) error
	// DeleteMLSchedules removes every schedule of a vhost.
	DeleteMLSchedules(ctx context.Context, vhost string) error
}

// The outcomes of a recommendation that feedback reports.
var MLFeedbackOutcomes = []string{"shown", "accepted", "dismissed", "visited", "ordered"}

// MLFeedbackEvent is what happened to one recommended item: shown to
// someone, accepted, dismissed, visited or ordered. RecommendationID is the
// answer it was in.
type MLFeedbackEvent struct {
	VHost            string    `json:"vhost"`
	Model            string    `json:"model"`
	RecommendationID string    `json:"recommendation_id"`
	ItemID           string    `json:"id"`
	Outcome          string    `json:"outcome"`
	At               time.Time `json:"at"`
}

// ValidateMLFeedbackEvent reports what is wrong with one event.
func ValidateMLFeedbackEvent(e MLFeedbackEvent) error {
	switch {
	case e.VHost == "" || e.Model == "":
		return errors.New("feedback belongs to one model of one vhost")
	case e.RecommendationID == "" || len(e.RecommendationID) > 128:
		return errors.New("feedback names the recommendation_id it is about")
	case e.ItemID == "" || len(e.ItemID) > 512:
		return errors.New("each feedback event names the id of the recommended item")
	case !slices.Contains(MLFeedbackOutcomes, e.Outcome):
		return fmt.Errorf("outcome %q is not one of shown, accepted, dismissed, visited or ordered", e.Outcome)
	case e.At.IsZero():
		return errors.New("each feedback event has a time")
	}
	return nil
}

// MLFeedbackStore is implemented by a storage backend that keeps feedback
// on recommendations. Like MLModelStore, callers find it with a type
// assertion.
type MLFeedbackStore interface {
	// AddMLFeedback stores events, every one or none.
	AddMLFeedback(ctx context.Context, events []MLFeedbackEvent) error
	// CountMLFeedback counts a model's events at or after since, by outcome.
	CountMLFeedback(ctx context.Context, vhost, model string, since time.Time) (map[string]int, error)
	// DeleteMLFeedback removes a model's feedback, or the whole vhost's when
	// model is empty.
	DeleteMLFeedback(ctx context.Context, vhost, model string) error
}
