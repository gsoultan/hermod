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

func (s *mongoStorage) ListRetrainingMLModels(ctx context.Context) ([]storage.MLModel, error) {
	cur, err := s.db.Collection(mlModelsCollection).Find(ctx,
		bson.M{"retrain": bson.M{"$exists": true, "$ne": nil}},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("listing models that retrain: %w", err)
	}
	var docs []mlModelDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("listing models that retrain: %w", err)
	}
	out := make([]storage.MLModel, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.model())
	}
	return out, nil
}

func (s *mongoStorage) SetMLModelRetrain(ctx context.Context, vhost, name string, p *storage.MLRetrainPolicy) error {
	update := bson.M{"$unset": bson.M{"retrain": ""}}
	if p != nil {
		update = bson.M{"$set": bson.M{"retrain": p}}
	}
	return s.updateMLModel(ctx, vhost, name, update, "retrain policy")
}

func (s *mongoStorage) SetMLModelScoring(ctx context.Context, vhost, name, scoring string) error {
	if err := storage.ValidateMLScoring(scoring); err != nil {
		return err
	}
	update := bson.M{"$unset": bson.M{"scoring": ""}}
	if scoring != "" {
		update = bson.M{"$set": bson.M{"scoring": scoring}}
	}
	return s.updateMLModel(ctx, vhost, name, update, "scoring")
}

func (s *mongoStorage) SetMLModelRetrainStatus(ctx context.Context, vhost, name string, st storage.MLRetrainStatus) error {
	return s.updateMLModel(ctx, vhost, name, bson.M{"$set": bson.M{"retrain_status": st}}, "retrain status")
}

func (s *mongoStorage) updateMLModel(ctx context.Context, vhost, name string, update bson.M, what string) error {
	res, err := s.db.Collection(mlModelsCollection).UpdateOne(ctx, bson.M{"_id": mlModelID(vhost, name)}, update)
	if err != nil {
		return fmt.Errorf("saving the %s of model %q of vhost %q: %w", what, name, vhost, err)
	}
	if res.MatchedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// ClaimMLModelTraining is AcquireWorkflowLease on a model: it takes an
// unclaimed or expired claim, or renews the owner's own.
func (s *mongoStorage) ClaimMLModelTraining(ctx context.Context, vhost, name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	filter := bson.M{
		"_id": mlModelID(vhost, name),
		"$or": []bson.M{
			{"training_owner": bson.M{"$exists": false}},
			{"training_owner": nil},
			{"training_until": bson.M{"$exists": false}},
			{"training_until": nil},
			{"training_until": bson.M{"$lt": now}},
			{"training_owner": owner},
		},
	}
	update := bson.M{"$set": bson.M{"training_owner": owner, "training_until": now.Add(ttl)}}
	err := s.db.Collection(mlModelsCollection).FindOneAndUpdate(ctx, filter, update).Err()
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return false, fmt.Errorf("claiming model %q of vhost %q for training: %w", name, vhost, err)
	}
	// Held by someone else, or no such model: a read tells them apart.
	if _, err := s.GetMLModel(ctx, vhost, name); err != nil {
		return false, err
	}
	return false, nil
}

func (s *mongoStorage) ReleaseMLModelTraining(ctx context.Context, vhost, name, owner string) error {
	_, err := s.db.Collection(mlModelsCollection).UpdateOne(ctx,
		bson.M{"_id": mlModelID(vhost, name), "training_owner": owner},
		bson.M{"$unset": bson.M{"training_owner": "", "training_until": ""}})
	if err != nil {
		return fmt.Errorf("releasing model %q of vhost %q after training: %w", name, vhost, err)
	}
	return nil
}
