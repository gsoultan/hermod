// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration
// +build integration

package mongodb

import (
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// The same contract the SQL store keeps (internal/storage/sql/ml_recommend_test.go),
// against a live MongoDB.
func TestMongoMLSchedulesAndFeedback(t *testing.T) {
	s, _ := newTraceMongo(t)
	ss, ok := s.(storage.MLScheduleStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.MLScheduleStore")
	}
	fs, ok := s.(storage.MLFeedbackStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.MLFeedbackStore")
	}
	ctx := t.Context()
	sc := storage.MLSchedule{VHost: "a", Kind: storage.MLScheduleModelBuild, Name: "plan", Schedule: "@daily", Spec: []byte(`{"for":"all_salesmen"}`)}
	if err := ss.PutMLSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	if err := ss.SetMLScheduleRun(ctx, "a", sc.Kind, "plan", at, 7, ""); err != nil {
		t.Fatal(err)
	}
	sc.Schedule = "@hourly"
	if err := ss.PutMLSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	got, err := ss.GetMLSchedule(ctx, "a", sc.Kind, "plan")
	if err != nil || got.Schedule != "@hourly" || got.LastRows != 7 || !got.LastRun.Equal(at) || string(got.Spec) != string(sc.Spec) {
		t.Fatalf("round trip %+v, %v", got, err)
	}
	if ok, _ := ss.ClaimMLSchedule(ctx, "a", sc.Kind, "plan", "s1", time.Minute); !ok {
		t.Fatal("first claim refused")
	}
	if ok, _ := ss.ClaimMLSchedule(ctx, "a", sc.Kind, "plan", "s2", time.Minute); ok {
		t.Fatal("second claim allowed")
	}
	_ = ss.ReleaseMLSchedule(ctx, "a", sc.Kind, "plan", "s1")
	if ok, _ := ss.ClaimMLSchedule(ctx, "a", sc.Kind, "plan", "s2", time.Minute); !ok {
		t.Fatal("claim after release refused")
	}
	if err := ss.DeleteMLSchedule(ctx, "a", sc.Kind, "plan"); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.GetMLSchedule(ctx, "a", sc.Kind, "plan"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}

	now := time.Now().UTC()
	if err := fs.AddMLFeedback(ctx, []storage.MLFeedbackEvent{
		{VHost: "a", Model: "m", RecommendationID: "rec_1", ItemID: "c1", Outcome: "shown", At: now},
		{VHost: "a", Model: "m", RecommendationID: "rec_1", ItemID: "c1", Outcome: "accepted", At: now},
	}); err != nil {
		t.Fatal(err)
	}
	counts, err := fs.CountMLFeedback(ctx, "a", "m", now.Add(-time.Hour))
	if err != nil || counts["shown"] != 1 || counts["accepted"] != 1 {
		t.Fatalf("counts %v, %v", counts, err)
	}
	if err := fs.DeleteMLFeedback(ctx, "a", ""); err != nil {
		t.Fatal(err)
	}
}
