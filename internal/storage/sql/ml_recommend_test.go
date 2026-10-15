// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sql

import (
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

func TestMLSchedulesRoundTripAndClaim(t *testing.T) {
	s := newRotationStorage(t)
	var st storage.Storage = s
	ss, ok := st.(storage.MLScheduleStore)
	if !ok {
		t.Fatal("the SQL store does not implement MLScheduleStore")
	}
	ctx := t.Context()
	if _, err := ss.GetMLSchedule(ctx, "a", storage.MLScheduleDatasetRefresh, "orders"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("no schedule: %v", err)
	}
	sc := storage.MLSchedule{VHost: "a", Kind: storage.MLScheduleDatasetRefresh, Name: "orders", Schedule: "@daily",
		Spec: []byte(`{"source_id":"s1","query":"SELECT 1"}`), UpdatedBy: "root"}
	if err := ss.PutMLSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	if err := ss.SetMLScheduleRun(ctx, "a", storage.MLScheduleDatasetRefresh, "orders", at, 42, "boom"); err != nil {
		t.Fatal(err)
	}
	// Saving the definition again keeps the last run.
	sc.Schedule = "0 3 * * *"
	if err := ss.PutMLSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	got, err := ss.GetMLSchedule(ctx, "a", storage.MLScheduleDatasetRefresh, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if got.Schedule != "0 3 * * *" || string(got.Spec) != string(sc.Spec) || !got.LastRun.Equal(at) || got.LastRows != 42 ||
		got.LastError != "boom" || got.UpdatedBy != "root" {
		t.Fatalf("round trip %+v", got)
	}
	// Another vhost's schedule of the same name is its own.
	if err := ss.PutMLSchedule(ctx, storage.MLSchedule{VHost: "b", Kind: storage.MLScheduleModelBuild, Name: "orders", Schedule: "@hourly"}); err != nil {
		t.Fatal(err)
	}
	all, err := ss.ListMLSchedules(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("list = %+v, %v", all, err)
	}

	ok1, err := ss.ClaimMLSchedule(ctx, "a", storage.MLScheduleDatasetRefresh, "orders", "server-1", time.Minute)
	if err != nil || !ok1 {
		t.Fatalf("first claim %v %v", ok1, err)
	}
	if ok2, _ := ss.ClaimMLSchedule(ctx, "a", storage.MLScheduleDatasetRefresh, "orders", "server-2", time.Minute); ok2 {
		t.Fatal("a second server claimed a held schedule")
	}
	if err := ss.ReleaseMLSchedule(ctx, "a", storage.MLScheduleDatasetRefresh, "orders", "server-1"); err != nil {
		t.Fatal(err)
	}
	if ok2, _ := ss.ClaimMLSchedule(ctx, "a", storage.MLScheduleDatasetRefresh, "orders", "server-2", time.Minute); !ok2 {
		t.Fatal("a released schedule could not be claimed")
	}
	if ok, _ := ss.ClaimMLSchedule(ctx, "a", storage.MLScheduleDatasetRefresh, "nothing", "server-2", time.Minute); ok {
		t.Fatal("claimed a schedule that does not exist")
	}

	if err := ss.DeleteMLSchedule(ctx, "a", storage.MLScheduleDatasetRefresh, "orders"); err != nil {
		t.Fatal(err)
	}
	if err := ss.DeleteMLSchedule(ctx, "a", storage.MLScheduleDatasetRefresh, "orders"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := ss.DeleteMLSchedules(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if all, _ := ss.ListMLSchedules(ctx); len(all) != 0 {
		t.Fatalf("left %+v", all)
	}
	if err := ss.PutMLSchedule(ctx, storage.MLSchedule{VHost: "a", Kind: "teleport", Name: "x", Schedule: "@daily"}); err == nil {
		t.Fatal("saved a schedule of an unknown kind")
	}
}

func TestMLFeedbackCountsByOutcome(t *testing.T) {
	s := newRotationStorage(t)
	var st storage.Storage = s
	fs, ok := st.(storage.MLFeedbackStore)
	if !ok {
		t.Fatal("the SQL store does not implement MLFeedbackStore")
	}
	ctx := t.Context()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	ev := func(model, outcome string, daysAgo int) storage.MLFeedbackEvent {
		return storage.MLFeedbackEvent{VHost: "a", Model: model, RecommendationID: "rec_1", ItemID: "c1", Outcome: outcome,
			At: now.AddDate(0, 0, -daysAgo)}
	}
	if err := fs.AddMLFeedback(ctx, []storage.MLFeedbackEvent{ev("m", "shown", 1), ev("m", "shown", 2), ev("m", "accepted", 1),
		ev("m", "shown", 40), ev("other", "shown", 1)}); err != nil {
		t.Fatal(err)
	}
	got, err := fs.CountMLFeedback(ctx, "a", "m", now.AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if got["shown"] != 2 || got["accepted"] != 1 || len(got) != 2 {
		t.Fatalf("counts %v", got)
	}
	// One bad event stores none.
	if err := fs.AddMLFeedback(ctx, []storage.MLFeedbackEvent{ev("m", "shown", 0), ev("m", "liked", 0)}); err == nil {
		t.Fatal("stored an unknown outcome")
	}
	if got, _ := fs.CountMLFeedback(ctx, "a", "m", now.AddDate(0, 0, -30)); got["shown"] != 2 {
		t.Fatalf("a refused batch was partly stored: %v", got)
	}
	if err := fs.DeleteMLFeedback(ctx, "a", "m"); err != nil {
		t.Fatal(err)
	}
	if got, _ := fs.CountMLFeedback(ctx, "a", "other", now.AddDate(0, 0, -30)); got["shown"] != 1 {
		t.Fatalf("deleting one model's feedback touched another's: %v", got)
	}
	if err := fs.DeleteMLFeedback(ctx, "a", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := fs.CountMLFeedback(ctx, "a", "other", now.AddDate(0, 0, -30)); len(got) != 0 {
		t.Fatalf("vhost delete left %v", got)
	}
}
