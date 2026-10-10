package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// A Collect Dataset sink's batch when its config names none: up to this many
// rows per append to the worker, or whatever has arrived after this long.
const (
	mlDatasetBatchSize    = 500
	mlDatasetBatchTimeout = 5 * time.Second
)

// createMLDatasetSink builds a Collect Dataset sink on the registry's ML
// worker, so it reaches the same worker training does.
func (r *Registry) createMLDatasetSink(cfg factory.SinkConfig) (hermod.Sink, error) {
	w, err := r.MLService().Worker()
	if err != nil {
		return nil, fmt.Errorf("ml_dataset sink: %w", err)
	}
	return factory.CreateMLDatasetSink(cfg, w)
}

// MLService is the model registry and inference path over whatever storage
// and secrets the registry holds right now. It is cheap to build, so it is
// built per use rather than cached against a store that may be replaced.
func (r *Registry) MLService() *ml.Service {
	svc := ml.NewService(func() any { return r.store() }, r.vhostSecrets(), nil)
	if r.mlWorker != nil {
		svc.WithWorker(r.mlWorker)
	}
	return svc
}

// MLPredict is what the Predict transformer calls: the vhost's model, with
// the rows it was given.
func (r *Registry) MLPredict(ctx context.Context, vhost, model string, rows []inference.Row) ([]inference.Row, error) {
	return r.MLService().Predict(ctx, vhost, model, rows)
}

// MLDatasetFromQuery replaces a vhost's dataset with what a query on one of
// the vhost's database sources returns. A source of another vhost is refused:
// its credentials are not this vhost's to use.
func (r *Registry) MLDatasetFromQuery(ctx context.Context, vhost, dataset, sourceID, query string, maxRows int) (int, error) {
	src, err := r.GetSourceConfig(ctx, sourceID)
	if err != nil {
		return 0, fmt.Errorf("source %q: %w", sourceID, err)
	}
	srcVHost := src.VHost
	if srcVHost == "" {
		srcVHost = "default"
	}
	if srcVHost != vhost {
		return 0, fmt.Errorf("source %q does not belong to vhost %q", sourceID, vhost)
	}
	svc := r.MLService()
	if _, err := svc.Worker(); err != nil {
		return 0, err
	}
	db, err := r.GetOrOpenDB(src)
	if err != nil {
		return 0, fmt.Errorf("opening source %q: %w", sourceID, err)
	}
	return svc.DatasetFromQuery(ctx, vhost, dataset, db, query, maxRows)
}

// trainRequest is what the Train Model node sends.
type trainRequest struct {
	worker.TrainSpec
	GoLive   ml.GoLive `json:"goLive"`
	SourceID string    `json:"sourceId"`
	Query    string    `json:"query"`
	MaxRows  int       `json:"maxRows"`
}

// MLTrain is what the Train Model transformer calls: refill the dataset from
// its source when the node names one, then train and register the model.
func (r *Registry) MLTrain(ctx context.Context, vhost, model string, req map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var tr trainRequest
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("reading the training request: %w", err)
	}
	if tr.SourceID != "" {
		if _, err := r.MLDatasetFromQuery(ctx, vhost, tr.Dataset, tr.SourceID, tr.Query, tr.MaxRows); err != nil {
			return nil, fmt.Errorf("refilling dataset %q: %w", tr.Dataset, err)
		}
	}
	res, err := r.MLService().Train(ctx, vhost, model, tr.TrainSpec, tr.GoLive, "workflow")
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"model": model, "version": res.Version.Version, "live": res.Live, "reason": res.Reason,
		"task": res.Version.Task, "algorithm": res.Version.Algorithm, "dataset": res.Version.Dataset,
		"rows": map[string]any{"train": res.Version.Rows.Train, "test": res.Version.Rows.Test},
	}
	metrics := make(map[string]any, len(res.Version.Metrics))
	for k, v := range res.Version.Metrics {
		metrics[k] = v
	}
	out["metrics"] = metrics
	return out, nil
}
