// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package mongodb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/gsoultan/hermod/internal/storage"
)

const (
	mlSchedulesCollection = "ml_schedules"
	mlFeedbackCollection  = "ml_feedback"
)

// mlScheduleID is the document key, as the SQL store's row key.
func mlScheduleID(vhost, kind, name string) string {
	return vhost + "/" + kind + "/" + name
}

// mlScheduleDoc is the stored form. Owner and OwnerUntil claim a run.
type mlScheduleDoc struct {
	ID        string    `bson:"_id"`
	VHost     string    `bson:"vhost"`
	Kind      string    `bson:"kind"`
	Name      string    `bson:"name"`
	Schedule  string    `bson:"schedule"`
	Spec      string    `bson:"spec"`
	LastRun   time.Time `bson:"last_run,omitempty"`
	LastRows  int       `bson:"last_rows,omitempty"`
	LastError string    `bson:"last_error,omitempty"`
	UpdatedBy string    `bson:"updated_by"`
	UpdatedAt time.Time `bson:"updated_at"`
}

func (d mlScheduleDoc) schedule() storage.MLSchedule {
	sc := storage.MLSchedule{VHost: d.VHost, Kind: d.Kind, Name: d.Name, Schedule: d.Schedule,
		LastRun: d.LastRun, LastRows: d.LastRows, LastError: d.LastError, UpdatedBy: d.UpdatedBy, UpdatedAt: d.UpdatedAt}
	if d.Spec != "" {
		sc.Spec = []byte(d.Spec)
	}
	return sc
}

func (s *mongoStorage) ListMLSchedules(ctx context.Context) ([]storage.MLSchedule, error) {
	cur, err := s.db.Collection(mlSchedulesCollection).Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("listing ML schedules: %w", err)
	}
	var docs []mlScheduleDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("listing ML schedules: %w", err)
	}
	out := make([]storage.MLSchedule, len(docs))
	for i, d := range docs {
		out[i] = d.schedule()
	}
	return out, nil
}

func (s *mongoStorage) GetMLSchedule(ctx context.Context, vhost, kind, name string) (storage.MLSchedule, error) {
	var d mlScheduleDoc
	err := s.db.Collection(mlSchedulesCollection).FindOne(ctx, bson.M{"_id": mlScheduleID(vhost, kind, name)}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.MLSchedule{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.MLSchedule{}, fmt.Errorf("reading the %s schedule of %q in vhost %q: %w", kind, name, vhost, err)
	}
	return d.schedule(), nil
}

// PutMLSchedule sets the definition's fields only, so the last run and any
// claim are kept.
func (s *mongoStorage) PutMLSchedule(ctx context.Context, sc storage.MLSchedule) error {
	if err := storage.ValidateMLSchedule(sc); err != nil {
		return err
	}
	set := bson.M{"vhost": sc.VHost, "kind": sc.Kind, "name": sc.Name, "schedule": sc.Schedule, "spec": string(sc.Spec),
		"updated_by": sc.UpdatedBy, "updated_at": time.Now().UTC()}
	_, err := s.db.Collection(mlSchedulesCollection).UpdateOne(ctx, bson.M{"_id": mlScheduleID(sc.VHost, sc.Kind, sc.Name)},
		bson.M{"$set": set}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("saving the %s schedule of %q in vhost %q: %w", sc.Kind, sc.Name, sc.VHost, err)
	}
	return nil
}

func (s *mongoStorage) DeleteMLSchedule(ctx context.Context, vhost, kind, name string) error {
	res, err := s.db.Collection(mlSchedulesCollection).DeleteOne(ctx, bson.M{"_id": mlScheduleID(vhost, kind, name)})
	if err != nil {
		return fmt.Errorf("deleting the %s schedule of %q in vhost %q: %w", kind, name, vhost, err)
	}
	if res.DeletedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *mongoStorage) SetMLScheduleRun(ctx context.Context, vhost, kind, name string, at time.Time, rows int, runErr string) error {
	_, err := s.db.Collection(mlSchedulesCollection).UpdateOne(ctx, bson.M{"_id": mlScheduleID(vhost, kind, name)},
		bson.M{"$set": bson.M{"last_run": at.UTC(), "last_rows": rows, "last_error": runErr}})
	if err != nil {
		return fmt.Errorf("recording the run of the %s schedule of %q in vhost %q: %w", kind, name, vhost, err)
	}
	return nil
}

// ClaimMLSchedule is ClaimMLModelTraining on a schedule's document.
func (s *mongoStorage) ClaimMLSchedule(ctx context.Context, vhost, kind, name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	filter := bson.M{
		"_id": mlScheduleID(vhost, kind, name),
		"$or": []bson.M{
			{"owner": bson.M{"$exists": false}},
			{"owner": nil},
			{"owner_until": bson.M{"$exists": false}},
			{"owner_until": nil},
			{"owner_until": bson.M{"$lt": now}},
			{"owner": owner},
		},
	}
	err := s.db.Collection(mlSchedulesCollection).FindOneAndUpdate(ctx, filter,
		bson.M{"$set": bson.M{"owner": owner, "owner_until": now.Add(ttl)}}).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claiming the %s schedule of %q in vhost %q: %w", kind, name, vhost, err)
	}
	return true, nil
}

func (s *mongoStorage) ReleaseMLSchedule(ctx context.Context, vhost, kind, name, owner string) error {
	_, err := s.db.Collection(mlSchedulesCollection).UpdateOne(ctx,
		bson.M{"_id": mlScheduleID(vhost, kind, name), "owner": owner},
		bson.M{"$unset": bson.M{"owner": "", "owner_until": ""}})
	if err != nil {
		return fmt.Errorf("releasing the %s schedule of %q in vhost %q: %w", kind, name, vhost, err)
	}
	return nil
}

func (s *mongoStorage) DeleteMLSchedules(ctx context.Context, vhost string) error {
	if _, err := s.db.Collection(mlSchedulesCollection).DeleteMany(ctx, bson.M{"vhost": vhost}); err != nil {
		return fmt.Errorf("deleting the ML schedules of vhost %q: %w", vhost, err)
	}
	return nil
}

// mlFeedbackDoc is one stored feedback event.
type mlFeedbackDoc struct {
	VHost            string    `bson:"vhost"`
	Model            string    `bson:"model"`
	RecommendationID string    `bson:"recommendation_id"`
	ItemID           string    `bson:"item_id"`
	Outcome          string    `bson:"outcome"`
	CreatedAt        time.Time `bson:"created_at"`
}

// AddMLFeedback checks every event before inserting any, so a bad batch
// stores none.
func (s *mongoStorage) AddMLFeedback(ctx context.Context, events []storage.MLFeedbackEvent) error {
	docs := make([]any, 0, len(events))
	for _, e := range events {
		if err := storage.ValidateMLFeedbackEvent(e); err != nil {
			return err
		}
		docs = append(docs, mlFeedbackDoc{VHost: e.VHost, Model: e.Model, RecommendationID: e.RecommendationID,
			ItemID: e.ItemID, Outcome: e.Outcome, CreatedAt: e.At.UTC()})
	}
	if len(docs) == 0 {
		return nil
	}
	if _, err := s.db.Collection(mlFeedbackCollection).InsertMany(ctx, docs); err != nil {
		return fmt.Errorf("saving feedback: %w", err)
	}
	return nil
}

func (s *mongoStorage) CountMLFeedback(ctx context.Context, vhost, model string, since time.Time) (map[string]int, error) {
	pipeline := []bson.M{
		{"$match": bson.M{"vhost": vhost, "model": model, "created_at": bson.M{"$gte": since.UTC()}}},
		{"$group": bson.M{"_id": "$outcome", "n": bson.M{"$sum": 1}}},
	}
	cur, err := s.db.Collection(mlFeedbackCollection).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("counting feedback on model %q of vhost %q: %w", model, vhost, err)
	}
	var rows []struct {
		Outcome string `bson:"_id"`
		N       int    `bson:"n"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("counting feedback on model %q of vhost %q: %w", model, vhost, err)
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Outcome] = r.N
	}
	return out, nil
}

func (s *mongoStorage) DeleteMLFeedback(ctx context.Context, vhost, model string) error {
	filter := bson.M{"vhost": vhost}
	if model != "" {
		filter["model"] = model
	}
	if _, err := s.db.Collection(mlFeedbackCollection).DeleteMany(ctx, filter); err != nil {
		return fmt.Errorf("deleting feedback of vhost %q: %w", vhost, err)
	}
	return nil
}
