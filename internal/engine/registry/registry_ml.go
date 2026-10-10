package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/ml/monitor"
	"github.com/gsoultan/hermod/internal/notification"
	"github.com/gsoultan/hermod/internal/storage"
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
// built per use rather than cached against a store that may be replaced. The
// monitor is not: it is the one that holds the drift windows.
func (r *Registry) MLService() *ml.Service {
	svc := ml.NewService(func() any { return r.store() }, r.vhostSecrets(), nil).
		WithLogStore(func() any { return r.logStore() }).
		WithMonitor(r.mlMonitor())
	if r.mlWorker != nil {
		svc.WithWorker(r.mlWorker)
	}
	return svc
}

// mlMonitor is this process's prediction monitor, started on first use and
// stopped with the registry. Prediction logs go to the log store, as message
// traces do; a drift alert goes out through the notification channels.
func (r *Registry) mlMonitor() *monitor.Monitor {
	r.mlMonOnce.Do(func() {
		r.mlMon = monitor.New(monitor.ConfigFromEnv(), monitor.Deps{
			Logs: func() any { return r.logStore() },
			Stats: func(ctx context.Context, vhost, model, version string) (map[string]worker.FeatureStats, error) {
				return r.MLService().VersionStats(ctx, vhost, model, version)
			},
			Notify: r.notifyMLDrift,
			Logger: r.Logger(),
		})
		go r.mlMon.Run(r.ctx)
	})
	return r.mlMon
}

// MLPredict is what the Predict transformer calls: the vhost's model, with
// the rows it was given. The workflow is the caller the prediction log names.
func (r *Registry) MLPredict(ctx context.Context, vhost, model string, rows []inference.Row) ([]inference.Row, error) {
	workflowID, _ := ctx.Value(hermod.WorkflowIDKey).(string)
	return r.MLService().Predict(ml.WithCaller(ctx, storage.MLCallerWorkflow, workflowID), vhost, model, rows)
}

// notifyMLDrift alerts that a model's inputs drifted past its alert
// threshold. It is called at most once per judged window.
func (r *Registry) notifyMLDrift(ctx context.Context, rep monitor.Report) {
	var drifted []string
	for _, f := range rep.Features {
		if f.Status == monitor.StatusAlert {
			drifted = append(drifted, fmt.Sprintf("%s (PSI %.2f)", f.Feature, f.PSI))
		}
	}
	name := rep.VHost + "/" + rep.Model
	msg := fmt.Sprintf("Version %s of model %s: %s drifted past the alert threshold %.2f over the last %d predictions. "+
		"The model is seeing inputs unlike the rows it was trained on; check the source data, or train it again.",
		rep.Version, name, strings.Join(drifted, ", "), rep.Alert, rep.Rows)
	r.notifyAt(ctx, notification.LevelWarn, "ML model drift: "+name, msg,
		storage.Workflow{Name: "ML model " + name, VHost: rep.VHost})
}

// purgeMLPredictionLogs applies each model's prediction log retention, and
// storage.MaxMLLogRetention to every row, so the log of a model or vhost that
// is gone does not outlive it.
func (r *Registry) purgeMLPredictionLogs(ctx context.Context, store, logStore storage.Storage) {
	logs, ok := logStore.(storage.MLPredictionLogStore)
	if !ok {
		return
	}
	now := time.Now()
	if err := logs.PurgeMLPredictionLogs(ctx, "", "", now.Add(-storage.MaxMLLogRetention)); err != nil {
		r.logger.Error("Registry: purging ML prediction logs failed", "error", err)
		return
	}
	models, ok := store.(storage.MLModelStore)
	if !ok {
		return
	}
	vhosts := []string{"default"}
	list, _, err := store.ListVHosts(ctx, storage.CommonFilter{Limit: workflowPageForRetention})
	if err != nil {
		r.logger.Error("Registry: listing vhosts for ML prediction log retention failed", "error", err)
	}
	for _, v := range list {
		if v.Name != "default" {
			vhosts = append(vhosts, v.Name)
		}
	}
	for _, vhost := range vhosts {
		ms, err := models.ListMLModels(ctx, vhost)
		if err != nil {
			continue
		}
		for _, m := range ms {
			if err := logs.PurgeMLPredictionLogs(ctx, vhost, m.Name, now.Add(-m.Monitoring.Retention())); err != nil {
				r.logger.Error("Registry: purging ML prediction logs failed", "vhost", vhost, "model", m.Name, "error", err)
			}
		}
	}
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
