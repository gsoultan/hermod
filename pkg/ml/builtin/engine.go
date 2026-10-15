// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

// Package builtin is Hermod's built-in ML engine: datasets, training,
// versions and predictions in Hermod's own process, in pure Go, with no
// hermod-ml worker to run.
//
// It speaks the worker's contract. Engine is an http.Handler that answers the
// worker's routes (ml/worker/hermod_ml/app.py) with the same JSON, and
// Client returns a pkg/ml/worker client that reaches it without a network,
// so everything Hermod builds on the worker (jobs, versions and going live,
// serving keys, quotas, retraining, the Collect Dataset sink, monitoring)
// works on it unchanged. A prediction does not go through JSON at all: the
// client's Local hands Predict to the ML service directly.
//
// It trains the common cases: linear (ridge and logistic regression),
// random_forest, gradient_boosting and mlp (a small neural network) for
// classification and regression, auto among them, and recommendations
// (als and item_similarity). Everything else the worker trains
// (extra_trees, hist_gradient_boosting, xgboost, the PyTorch and Keras
// networks, custom scripts and recommendation scenarios) needs the worker,
// and is refused here with a message that says so.
package builtin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/builtin/learn"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// Config is how the engine runs.
type Config struct {
	// Blobs keeps the datasets and model versions. Hermod gives the engine
	// its metadata store, so every replica shares them; without one they
	// are kept in Dir.
	Blobs Blobs
	// Dir keeps the datasets and model versions when Blobs is nil.
	Dir string
	// MaxTrainings is how many trainings run at once; another is refused as
	// busy, as the worker refuses one.
	MaxTrainings int
	// MaxTrainRows is the most rows a training reads; a larger dataset is
	// sampled down to it, with a warning on the version.
	MaxTrainRows int
	// Workers bounds the goroutines one training uses (a forest's trees).
	Workers int
	// MaxUploadBytes bounds a request body: an uploaded file, or rows.
	MaxUploadBytes int64
}

// Defaults of the configuration.
const (
	DefaultMaxTrainRows   = 1_000_000
	DefaultMaxUploadBytes = 200 << 20
)

// ConfigFromEnv reads the engine's settings: HERMOD_ML_BUILTIN (false turns
// the engine off; on otherwise), HERMOD_ML_BUILTIN_DIR (none by default:
// Hermod keeps the engine's data in its metadata store, and uses the
// directory only with a store that cannot), HERMOD_ML_BUILTIN_MAX_TRAININGS
// (default 1), HERMOD_ML_BUILTIN_MAX_ROWS (default 1,000,000) and
// HERMOD_ML_BUILTIN_THREADS (default: the CPUs Go may use). on is false when
// the engine is turned off.
func ConfigFromEnv() (cfg Config, on bool) {
	if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("HERMOD_ML_BUILTIN"))); err == nil && !v {
		return Config{}, false
	}
	cfg = Config{Dir: strings.TrimSpace(os.Getenv("HERMOD_ML_BUILTIN_DIR"))}
	cfg.MaxTrainings = envInt("HERMOD_ML_BUILTIN_MAX_TRAININGS")
	cfg.MaxTrainRows = envInt("HERMOD_ML_BUILTIN_MAX_ROWS")
	cfg.Workers = envInt("HERMOD_ML_BUILTIN_THREADS")
	return cfg, true
}

func envInt(name string) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// Engine is the built-in engine. It is safe for concurrent use.
type Engine struct {
	cfg      Config
	owned    bool
	blobs    Blobs
	datasets *datasetStore
	models   *modelStore
	slots    chan struct{}
	mux      *http.ServeMux
	now      func() time.Time

	cacheMu sync.Mutex
	cache   map[string]*loaded
	order   []string

	// synced is what each worker last got of each dataset (Route).
	synced syncState
}

// New returns an engine over cfg.Blobs, or else over cfg.Dir, which it
// creates.
func New(cfg Config) (*Engine, error) {
	if cfg.Blobs == nil && cfg.Dir == "" {
		return nil, errors.New("the built-in ML engine needs a store or a directory")
	}
	if cfg.MaxTrainings <= 0 {
		cfg.MaxTrainings = 1
	}
	if cfg.MaxTrainRows <= 0 {
		cfg.MaxTrainRows = DefaultMaxTrainRows
	}
	if cfg.Workers <= 0 {
		cfg.Workers = runtime.GOMAXPROCS(0)
	}
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = DefaultMaxUploadBytes
	}
	blobs, owned := cfg.Blobs, false
	if blobs == nil {
		var err error
		if blobs, err = DirBlobs(cfg.Dir); err != nil {
			return nil, fmt.Errorf("opening the built-in ML engine's directory: %w", err)
		}
		owned = true
	}
	e := &Engine{
		cfg:      cfg,
		owned:    owned,
		blobs:    blobs,
		datasets: &datasetStore{blobs: blobs},
		models:   &modelStore{blobs: blobs},
		slots:    make(chan struct{}, cfg.MaxTrainings),
		now:      time.Now,
		cache:    map[string]*loaded{},
	}
	e.routes()
	return e, nil
}

// Close releases the directory the engine opened, if it opened one. The
// engine must not be used after.
func (e *Engine) Close() error {
	if e.owned {
		return CloseBlobs(e.blobs)
	}
	return nil
}

// Client is a worker client that reaches this engine in-process.
func (e *Engine) Client() *worker.Client { return worker.NewLocal(e) }

// ServeHTTP answers the worker's routes.
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) { e.mux.ServeHTTP(w, r) }

// The tasks the engine trains, and its algorithms. Auto and every built-in
// algorithm are available; the worker's others are listed as unavailable,
// with why.
var (
	tasks      = []string{"auto", "classification", "regression", worker.TaskRecommendation}
	algorithms = []string{"auto", learn.AlgorithmRandomForest, learn.AlgorithmGradientBoosting, learn.AlgorithmLinear,
		learn.AlgorithmMLP, "als", "item_similarity"}
	// workerOnly are the worker's algorithms the engine does not train.
	workerOnly = []string{"xgboost", "extra_trees", "hist_gradient_boosting", "pytorch_mlp", "keras_mlp"}
)

// NeedsWorker is the reason a worker-only algorithm, scenario or script is
// refused here.
const NeedsWorker = "needs the hermod-ml worker"

func capabilities() worker.Capabilities {
	caps := worker.Capabilities{Tasks: tasks, Algorithms: algorithms, Unavailable: map[string]string{}}
	for _, a := range workerOnly {
		caps.Unavailable[a] = NeedsWorker
	}
	return caps
}

// Capabilities is what the engine trains, as the worker's capabilities
// route says it.
func (e *Engine) Capabilities() worker.Capabilities { return capabilities() }

// Capabilities is what the built-in engine trains, by itself.
func Capabilities() worker.Capabilities { return capabilities() }

// slot takes a training slot, or reports that every one is taken.
func (e *Engine) slot() (func(), error) {
	select {
	case e.slots <- struct{}{}:
		return func() { <-e.slots }, nil
	default:
		return nil, &apiError{status: http.StatusTooManyRequests, msg: "Training is busy; try again when the current training finishes."}
	}
}

// trainContext bounds a training by the request's context: a caller that
// goes away, or Hermod's train timeout, stops it.
func trainContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}
