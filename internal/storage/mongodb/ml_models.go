package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
)

const mlModelsCollection = "ml_models"

// mlModelDoc is the stored form. _id is vhost + "/" + name, as for
// vhost_secrets: a model name cannot hold a slash.
type mlModelDoc struct {
	ID             string            `bson:"_id"`
	VHost          string            `bson:"vhost"`
	Name           string            `bson:"name"`
	Description    string            `bson:"description,omitempty"`
	Backend        string            `bson:"backend"`
	URL            string            `bson:"url"`
	RemoteModel    string            `bson:"remote_model,omitempty"`
	RemoteVersion  string            `bson:"remote_version,omitempty"`
	TokenSecret    string            `bson:"token_secret,omitempty"`
	InputName      string            `bson:"input_name,omitempty"`
	Features       []string          `bson:"features,omitempty"`
	FeatureTypes   map[string]string `bson:"feature_types,omitempty"`
	TimeoutMs      int               `bson:"timeout_ms,omitempty"`
	MCPExposed     bool              `bson:"mcp_exposed,omitempty"`
	Monitoring     mlMonDoc          `bson:"monitoring"`
	ServingKeyHash string            `bson:"serving_key_hash,omitempty"`
	UpdatedBy      string            `bson:"updated_by"`
	// PutMLModel sets none of these, so saving a definition leaves them as
	// they are.
	Retrain       *storage.MLRetrainPolicy `bson:"retrain,omitempty"`
	RetrainStatus *storage.MLRetrainStatus `bson:"retrain_status,omitempty"`
	CreatedAt     time.Time                `bson:"created_at"`
	UpdatedAt     time.Time                `bson:"updated_at"`
}

// mlMonDoc is storage.MLMonitoring with stored field names of its own, so a
// renamed Go field never silently drops a stored setting.
type mlMonDoc struct {
	LogSampleRate float64  `bson:"log_sample_rate,omitempty"`
	LogMaskFields []string `bson:"log_mask_fields,omitempty"`
	LogMaskType   string   `bson:"log_mask_type,omitempty"`
	LogRetention  string   `bson:"log_retention,omitempty"`
	DriftWarn     float64  `bson:"drift_warn,omitempty"`
	DriftAlert    float64  `bson:"drift_alert,omitempty"`
}

func monDoc(m storage.MLMonitoring) mlMonDoc {
	return mlMonDoc{
		LogSampleRate: m.LogSampleRate, LogMaskFields: m.LogMaskFields, LogMaskType: m.LogMaskType,
		LogRetention: m.LogRetention, DriftWarn: m.DriftWarn, DriftAlert: m.DriftAlert,
	}
}

func (d mlMonDoc) monitoring() storage.MLMonitoring {
	return storage.MLMonitoring{
		LogSampleRate: d.LogSampleRate, LogMaskFields: d.LogMaskFields, LogMaskType: d.LogMaskType,
		LogRetention: d.LogRetention, DriftWarn: d.DriftWarn, DriftAlert: d.DriftAlert,
	}
}

func mlModelID(vhost, name string) string {
	return vhost + "/" + name
}

func (d mlModelDoc) model() storage.MLModel {
	return storage.MLModel{
		VHost: d.VHost, Name: d.Name, Description: d.Description,
		Backend: inference.Backend(d.Backend), URL: d.URL, RemoteModel: d.RemoteModel, RemoteVersion: d.RemoteVersion,
		TokenSecret: d.TokenSecret, InputName: d.InputName, Features: d.Features, TimeoutMs: d.TimeoutMs,
		FeatureTypes: d.FeatureTypes, MCPExposed: d.MCPExposed,
		ServingKeyHash: d.ServingKeyHash, Serving: d.ServingKeyHash != "", Monitoring: d.Monitoring.monitoring(),
		Retrain: d.Retrain, RetrainStatus: d.RetrainStatus,
		UpdatedBy: d.UpdatedBy, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func (s *mongoStorage) ListMLModels(ctx context.Context, vhost string) ([]storage.MLModel, error) {
	cur, err := s.db.Collection(mlModelsCollection).Find(ctx, bson.M{"vhost": vhost},
		options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("listing models of vhost %q: %w", vhost, err)
	}
	var docs []mlModelDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("listing models of vhost %q: %w", vhost, err)
	}
	out := make([]storage.MLModel, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.model())
	}
	return out, nil
}

func (s *mongoStorage) GetMLModel(ctx context.Context, vhost, name string) (storage.MLModel, error) {
	var d mlModelDoc
	err := s.db.Collection(mlModelsCollection).FindOne(ctx, bson.M{"_id": mlModelID(vhost, name)}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.MLModel{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.MLModel{}, fmt.Errorf("reading model %q of vhost %q: %w", name, vhost, err)
	}
	return d.model(), nil
}

// PutMLModel upserts the definition. serving_key_hash is in neither $set nor
// $setOnInsert, so an edit never changes it.
func (s *mongoStorage) PutMLModel(ctx context.Context, m storage.MLModel) error {
	if err := storage.ValidateMLModel(m); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err := s.db.Collection(mlModelsCollection).UpdateOne(ctx,
		bson.M{"_id": mlModelID(m.VHost, m.Name)},
		bson.M{
			"$set": bson.M{
				"description": m.Description, "backend": string(m.Backend), "url": m.URL,
				"remote_model": m.RemoteModel, "remote_version": m.RemoteVersion, "token_secret": m.TokenSecret,
				"input_name": m.InputName, "features": m.Features, "timeout_ms": m.TimeoutMs,
				"feature_types": m.FeatureTypes, "mcp_exposed": m.MCPExposed,
				"monitoring": monDoc(m.Monitoring), "updated_by": m.UpdatedBy, "updated_at": now,
			},
			"$setOnInsert": bson.M{"vhost": m.VHost, "name": m.Name, "created_at": now},
		},
		options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("saving model %q of vhost %q: %w", m.Name, m.VHost, err)
	}
	return nil
}

func (s *mongoStorage) SetMLModelServingKey(ctx context.Context, vhost, name, hash string) error {
	res, err := s.db.Collection(mlModelsCollection).UpdateOne(ctx,
		bson.M{"_id": mlModelID(vhost, name)}, bson.M{"$set": bson.M{"serving_key_hash": hash}})
	if err != nil {
		return fmt.Errorf("setting the serving key of model %q of vhost %q: %w", name, vhost, err)
	}
	if res.MatchedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *mongoStorage) DeleteMLModel(ctx context.Context, vhost, name string) error {
	res, err := s.db.Collection(mlModelsCollection).DeleteOne(ctx, bson.M{"_id": mlModelID(vhost, name)})
	if err != nil {
		return fmt.Errorf("deleting model %q of vhost %q: %w", name, vhost, err)
	}
	if res.DeletedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *mongoStorage) DeleteMLModels(ctx context.Context, vhost string) error {
	if _, err := s.db.Collection(mlModelsCollection).DeleteMany(ctx, bson.M{"vhost": vhost}); err != nil {
		return fmt.Errorf("deleting the models of vhost %q: %w", vhost, err)
	}
	return nil
}

const mlPredictionLogsCollection = "ml_prediction_logs"

// mlPredictionLogDoc is one logged prediction. Inputs and outputs are JSON
// text, as in the SQL store, so a row reads back as the same Go values
// whatever shapes the model was sent.
type mlPredictionLogDoc struct {
	VHost      string    `bson:"vhost"`
	Model      string    `bson:"model"`
	Version    string    `bson:"version,omitempty"`
	Timestamp  time.Time `bson:"timestamp"`
	Inputs     string    `bson:"inputs"`
	Outputs    string    `bson:"outputs"`
	LatencyMs  float64   `bson:"latency_ms"`
	CallerKind string    `bson:"caller_kind"`
	CallerID   string    `bson:"caller_id,omitempty"`
}

func (s *mongoStorage) InsertMLPredictionLogs(ctx context.Context, logs []storage.MLPredictionLog) error {
	if len(logs) == 0 {
		return nil
	}
	docs := make([]any, 0, len(logs))
	for _, l := range logs {
		inputs, err := json.Marshal(l.Inputs)
		if err != nil {
			return fmt.Errorf("encoding a logged prediction of model %q: %w", l.Model, err)
		}
		outputs, err := json.Marshal(l.Outputs)
		if err != nil {
			return fmt.Errorf("encoding a logged prediction of model %q: %w", l.Model, err)
		}
		docs = append(docs, mlPredictionLogDoc{
			VHost: l.VHost, Model: l.Model, Version: l.Version, Timestamp: l.Timestamp.UTC(),
			Inputs: string(inputs), Outputs: string(outputs), LatencyMs: l.LatencyMs,
			CallerKind: l.CallerKind, CallerID: l.CallerID,
		})
	}
	if _, err := s.db.Collection(mlPredictionLogsCollection).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false)); err != nil {
		return fmt.Errorf("writing prediction logs: %w", err)
	}
	return nil
}

func (s *mongoStorage) ListMLPredictionLogs(ctx context.Context, vhost, model string, limit int) ([]storage.MLPredictionLog, error) {
	cur, err := s.db.Collection(mlPredictionLogsCollection).Find(ctx, bson.M{"vhost": vhost, "model": model},
		options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}).SetLimit(int64(storage.MLPredictionLogLimit(limit))))
	if err != nil {
		return nil, fmt.Errorf("reading the prediction log of model %q of vhost %q: %w", model, vhost, err)
	}
	var docs []mlPredictionLogDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("reading the prediction log of model %q of vhost %q: %w", model, vhost, err)
	}
	out := make([]storage.MLPredictionLog, 0, len(docs))
	for _, d := range docs {
		l := storage.MLPredictionLog{
			VHost: d.VHost, Model: d.Model, Version: d.Version, Timestamp: d.Timestamp,
			LatencyMs: d.LatencyMs, CallerKind: d.CallerKind, CallerID: d.CallerID,
		}
		if err := json.Unmarshal([]byte(d.Inputs), &l.Inputs); err != nil {
			return nil, fmt.Errorf("a logged prediction is unreadable: %w", err)
		}
		if err := json.Unmarshal([]byte(d.Outputs), &l.Outputs); err != nil {
			return nil, fmt.Errorf("a logged prediction is unreadable: %w", err)
		}
		out = append(out, l)
	}
	return out, nil
}

// predictionLogScope is the filter for one model, one vhost (model empty) or
// every vhost (both empty).
func predictionLogScope(vhost, model string) bson.M {
	filter := bson.M{}
	if vhost != "" {
		filter["vhost"] = vhost
		if model != "" {
			filter["model"] = model
		}
	}
	return filter
}

func (s *mongoStorage) PurgeMLPredictionLogs(ctx context.Context, vhost, model string, before time.Time) error {
	filter := predictionLogScope(vhost, model)
	filter["timestamp"] = bson.M{"$lt": before.UTC()}
	if _, err := s.db.Collection(mlPredictionLogsCollection).DeleteMany(ctx, filter); err != nil {
		return fmt.Errorf("purging prediction logs: %w", err)
	}
	return nil
}

func (s *mongoStorage) DeleteMLPredictionLogs(ctx context.Context, vhost, model string) error {
	if vhost == "" {
		return errors.New("deleting prediction logs needs a vhost")
	}
	if _, err := s.db.Collection(mlPredictionLogsCollection).DeleteMany(ctx, predictionLogScope(vhost, model)); err != nil {
		return fmt.Errorf("deleting the prediction logs of vhost %q: %w", vhost, err)
	}
	return nil
}
