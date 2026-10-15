// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package ml

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/gsoultan/hermod/internal/storage"
)

// Feedback on recommendations: what happened to the items an answer listed,
// sent back with its recommendation_id, and counted per model.

const (
	// MaxFeedbackEvents bounds one feedback call.
	MaxFeedbackEvents = 1000
	// DefaultFeedbackDays is the summary's window when the call names none.
	DefaultFeedbackDays = 30
	// MaxFeedbackDays bounds the summary's window.
	MaxFeedbackDays = 365
)

// ErrBadFeedback marks a feedback call that is wrong in itself.
var ErrBadFeedback = errors.New("invalid feedback")

// acceptanceRate is each recommendation model's acceptance rate over the
// last DefaultFeedbackDays days, as the feedback summary computes it. It is
// set when feedback arrives, so it is as fresh as the last feedback.
var acceptanceRate = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "hermod_ml_recommendation_acceptance_rate",
	Help: "Share of a recommendation model's shown items that were accepted over the last 30 days (accepted / shown; accepted / (accepted + dismissed) when nothing was reported shown).",
}, []string{"vhost", "model"})

// FeedbackEvent is one outcome of one recommended item: ID is the item's id
// (a customer or a product) and At when it happened, now when left out.
type FeedbackEvent struct {
	ID      string     `json:"id"`
	Outcome string     `json:"outcome"`
	At      *time.Time `json:"at,omitempty"`
}

// FeedbackRequest is the body of POST .../feedback.
type FeedbackRequest struct {
	RecommendationID string          `json:"recommendation_id"`
	Events           []FeedbackEvent `json:"events"`
}

// FeedbackSummary counts a model's feedback by outcome over Days days.
// AcceptanceRate is accepted / shown, or accepted / (accepted + dismissed)
// when nothing was reported shown, and 0 with neither.
type FeedbackSummary struct {
	Days           int     `json:"days"`
	Shown          int     `json:"shown"`
	Accepted       int     `json:"accepted"`
	Dismissed      int     `json:"dismissed"`
	Visited        int     `json:"visited"`
	Ordered        int     `json:"ordered"`
	AcceptanceRate float64 `json:"acceptance_rate"`
}

func (s *Service) feedbackStore() (storage.MLFeedbackStore, error) {
	fs, ok := s.store().(storage.MLFeedbackStore)
	if !ok {
		return nil, storage.ErrMLModelsUnsupported
	}
	return fs, nil
}

// recommenderModel is the vhost's model name, when it is a recommendation
// model.
func (s *Service) recommenderModel(ctx context.Context, vhost, name string) (storage.MLModel, error) {
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return m, err
	}
	if !m.IsRecommender() {
		return m, fmt.Errorf("model %q: %w", name, ErrNotRecommender)
	}
	return m, nil
}

// RecordFeedback stores what happened to the items of one answer, every
// event or none, and refreshes the model's acceptance-rate gauge.
func (s *Service) RecordFeedback(ctx context.Context, vhost, name string, req FeedbackRequest) error {
	if _, err := s.recommenderModel(ctx, vhost, name); err != nil {
		return err
	}
	switch {
	case req.RecommendationID == "":
		return fmt.Errorf(`%w: give the "recommendation_id" of the answer the events are about`, ErrBadFeedback)
	case len(req.Events) == 0:
		return fmt.Errorf(`%w: "events" lists at least one outcome`, ErrBadFeedback)
	case len(req.Events) > MaxFeedbackEvents:
		return fmt.Errorf("%w: at most %d events per call, not %d", ErrBadFeedback, MaxFeedbackEvents, len(req.Events))
	}
	now := s.now().UTC()
	events := make([]storage.MLFeedbackEvent, len(req.Events))
	for i, e := range req.Events {
		at := now
		if e.At != nil {
			at = e.At.UTC()
		}
		events[i] = storage.MLFeedbackEvent{VHost: vhost, Model: name, RecommendationID: req.RecommendationID,
			ItemID: e.ID, Outcome: e.Outcome, At: at}
		if err := storage.ValidateMLFeedbackEvent(events[i]); err != nil {
			return fmt.Errorf("%w: event %d: %w", ErrBadFeedback, i+1, err)
		}
	}
	fs, err := s.feedbackStore()
	if err != nil {
		return err
	}
	if err := fs.AddMLFeedback(ctx, events); err != nil {
		return err
	}
	if sum, err := s.FeedbackSummary(ctx, vhost, name, DefaultFeedbackDays); err == nil {
		acceptanceRate.WithLabelValues(vhost, name).Set(sum.AcceptanceRate)
	}
	return nil
}

// FeedbackSummary counts a model's feedback over the last days days: 0 means
// DefaultFeedbackDays, at most MaxFeedbackDays.
func (s *Service) FeedbackSummary(ctx context.Context, vhost, name string, days int) (FeedbackSummary, error) {
	if days == 0 {
		days = DefaultFeedbackDays
	}
	if days < 1 || days > MaxFeedbackDays {
		return FeedbackSummary{}, fmt.Errorf("%w: days is between 1 and %d", ErrBadFeedback, MaxFeedbackDays)
	}
	if _, err := s.recommenderModel(ctx, vhost, name); err != nil {
		return FeedbackSummary{}, err
	}
	fs, err := s.feedbackStore()
	if err != nil {
		return FeedbackSummary{}, err
	}
	counts, err := fs.CountMLFeedback(ctx, vhost, name, s.now().UTC().AddDate(0, 0, -days))
	if err != nil {
		return FeedbackSummary{}, err
	}
	out := FeedbackSummary{Days: days, Shown: counts["shown"], Accepted: counts["accepted"], Dismissed: counts["dismissed"],
		Visited: counts["visited"], Ordered: counts["ordered"]}
	switch {
	case out.Shown > 0:
		out.AcceptanceRate = float64(out.Accepted) / float64(out.Shown)
	case out.Accepted+out.Dismissed > 0:
		out.AcceptanceRate = float64(out.Accepted) / float64(out.Accepted+out.Dismissed)
	}
	return out, nil
}
