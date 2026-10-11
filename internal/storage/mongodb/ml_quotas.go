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

const mlQuotasCollection = "ml_quotas"

// mlQuotasDoc is the stored form; _id is the vhost's name.
type mlQuotasDoc struct {
	VHost                   string    `bson:"_id"`
	MaxDatasets             *int64    `bson:"max_datasets,omitempty"`
	MaxDatasetRows          *int64    `bson:"max_dataset_rows,omitempty"`
	MaxDatasetBytes         *int64    `bson:"max_dataset_bytes,omitempty"`
	MaxModels               *int64    `bson:"max_models,omitempty"`
	MaxConcurrentTrainings  *int64    `bson:"max_concurrent_trainings,omitempty"`
	MaxPredictionsPerSecond *float64  `bson:"max_predictions_per_second,omitempty"`
	UpdatedBy               string    `bson:"updated_by"`
	UpdatedAt               time.Time `bson:"updated_at"`
}

func (s *mongoStorage) GetMLQuotas(ctx context.Context, vhost string) (storage.MLQuotas, error) {
	var d mlQuotasDoc
	err := s.db.Collection(mlQuotasCollection).FindOne(ctx, bson.M{"_id": vhost}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.MLQuotas{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.MLQuotas{}, fmt.Errorf("reading the ML quotas of vhost %q: %w", vhost, err)
	}
	return storage.MLQuotas{
		VHost: d.VHost, MaxDatasets: d.MaxDatasets, MaxDatasetRows: d.MaxDatasetRows, MaxDatasetBytes: d.MaxDatasetBytes,
		MaxModels: d.MaxModels, MaxConcurrentTrainings: d.MaxConcurrentTrainings, MaxPredictionsPerSecond: d.MaxPredictionsPerSecond,
		UpdatedBy: d.UpdatedBy, UpdatedAt: d.UpdatedAt,
	}, nil
}

// PutMLQuotas replaces the vhost's document, so a quota left out is unset.
func (s *mongoStorage) PutMLQuotas(ctx context.Context, q storage.MLQuotas) error {
	if err := storage.ValidateMLQuotas(q); err != nil {
		return err
	}
	d := mlQuotasDoc{
		VHost: q.VHost, MaxDatasets: q.MaxDatasets, MaxDatasetRows: q.MaxDatasetRows, MaxDatasetBytes: q.MaxDatasetBytes,
		MaxModels: q.MaxModels, MaxConcurrentTrainings: q.MaxConcurrentTrainings, MaxPredictionsPerSecond: q.MaxPredictionsPerSecond,
		UpdatedBy: q.UpdatedBy, UpdatedAt: time.Now().UTC(),
	}
	_, err := s.db.Collection(mlQuotasCollection).ReplaceOne(ctx, bson.M{"_id": q.VHost}, d, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("saving the ML quotas of vhost %q: %w", q.VHost, err)
	}
	return nil
}

func (s *mongoStorage) DeleteMLQuotas(ctx context.Context, vhost string) error {
	if _, err := s.db.Collection(mlQuotasCollection).DeleteOne(ctx, bson.M{"_id": vhost}); err != nil {
		return fmt.Errorf("deleting the ML quotas of vhost %q: %w", vhost, err)
	}
	return nil
}
