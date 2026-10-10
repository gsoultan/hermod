package ml

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// Devices a training can ask for.
const (
	DeviceCPU = "cpu"
	DeviceGPU = "gpu"
)

// CustomPrefix marks an algorithm that is a custom training script:
// "custom:<script name>".
const CustomPrefix = "custom:"

// poolCleanupTimeout bounds removing a training's copy from a pool.
const poolCleanupTimeout = time.Minute

var (
	// ErrCustomScriptsOff is returned for anything to do with custom training
	// scripts while the server has them off, which is the default.
	ErrCustomScriptsOff = errors.New("custom training scripts are off on this server: an operator turns them on with HERMOD_ML_CUSTOM_SCRIPTS=true")
	// ErrNoCustomPool is returned for a custom-script training when no worker
	// pool is set aside for custom scripts. They never run on the main worker.
	ErrNoCustomPool = errors.New("custom training scripts run only on their own worker pool, and none is configured: set HERMOD_ML_CUSTOM_WORKER_URL")
	// ErrNoGPUPool is returned for a training on the gpu when no GPU worker
	// pool is configured.
	ErrNoGPUPool = errors.New(`no GPU worker pool is configured: set HERMOD_ML_GPU_WORKER_URL, or train with device "cpu"`)
	// ErrScriptNotFound is returned for a script the vhost does not hold.
	ErrScriptNotFound = errors.New("training script not found")
	// ErrScriptsUnsupported is returned when the storage backend cannot hold
	// training scripts.
	ErrScriptsUnsupported = errors.New("this storage backend cannot hold training scripts")
	// ErrBadTraining marks a training request that is wrong in itself.
	ErrBadTraining = errors.New("invalid training request")
)

// Pools are the worker pools a training can run on besides the main worker.
// A pool trains only: it keeps nothing between trainings, and every version it
// makes is served by the main worker.
type Pools struct {
	// Custom runs custom training scripts, and CustomScripts allows them at
	// all. A worker pool that runs uploaded code is kept apart from the one
	// holding every vhost's data and serving its models.
	Custom        *worker.Client
	CustomScripts bool
	// GPU runs trainings that ask for device "gpu".
	GPU *worker.Client
}

// PoolsFromEnv reads HERMOD_ML_CUSTOM_SCRIPTS, HERMOD_ML_CUSTOM_WORKER_URL and
// _TOKEN, and HERMOD_ML_GPU_WORKER_URL and _TOKEN. A HERMOD_ML_CUSTOM_SCRIPTS
// that is not a boolean leaves custom scripts off.
func PoolsFromEnv() Pools {
	on, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("HERMOD_ML_CUSTOM_SCRIPTS")))
	if err != nil && os.Getenv("HERMOD_ML_CUSTOM_SCRIPTS") != "" {
		slog.Warn("HERMOD_ML_CUSTOM_SCRIPTS is not true or false; custom training scripts stay off")
	}
	return Pools{
		Custom:        worker.PoolFromEnv("HERMOD_ML_CUSTOM_WORKER"),
		CustomScripts: err == nil && on,
		GPU:           worker.PoolFromEnv("HERMOD_ML_GPU_WORKER"),
	}
}

// envPools are the pools the environment names, read once.
var envPools = sync.OnceValue(PoolsFromEnv)

// WithPools replaces the worker pools the environment names.
func (s *Service) WithPools(p Pools) *Service {
	s.pools = p
	return s
}

// Capabilities says which training options this server offers, for the UI.
type Capabilities struct {
	// CustomScripts: custom scripts are on and their pool is configured.
	CustomScripts bool `json:"custom_scripts"`
	// ScriptsEnabled: custom scripts are on, pool or not; scripts can be
	// managed but not trained with until the pool is configured.
	ScriptsEnabled bool `json:"scripts_enabled"`
	GPU            bool `json:"gpu"`
}

// Capabilities reports the training options the configured pools allow.
func (s *Service) Capabilities() Capabilities {
	return Capabilities{
		CustomScripts:  s.pools.CustomScripts && s.pools.Custom != nil,
		ScriptsEnabled: s.pools.CustomScripts,
		GPU:            s.pools.GPU != nil,
	}
}

// trainVersion trains one version where the spec says it should run: a
// custom script on the custom pool, a gpu training on the GPU pool, anything
// else on the main worker. A version trained on a pool is moved to the main
// worker, which serves it; the version returned is the main worker's.
func (s *Service) trainVersion(ctx context.Context, main *worker.Client, vhost, name string, spec worker.TrainSpec) (worker.Version, error) {
	custom := strings.HasPrefix(spec.Algorithm, CustomPrefix)
	switch spec.Device {
	case "", DeviceCPU:
	case DeviceGPU:
		if custom {
			return worker.Version{}, fmt.Errorf("%w: a custom script runs on the custom worker pool, so device %q does not apply; give that pool a GPU instead", ErrBadTraining, spec.Device)
		}
		if s.pools.GPU == nil {
			return worker.Version{}, ErrNoGPUPool
		}
		return s.trainOnPool(ctx, main, s.pools.GPU, vhost, name, spec, nil)
	default:
		return worker.Version{}, fmt.Errorf("%w: device is %q or %q, not %q", ErrBadTraining, DeviceCPU, DeviceGPU, spec.Device)
	}
	if !custom {
		return main.Train(ctx, vhost, name, spec)
	}
	if !s.pools.CustomScripts {
		return worker.Version{}, ErrCustomScriptsOff
	}
	if s.pools.Custom == nil {
		return worker.Version{}, ErrNoCustomPool
	}
	script, err := s.Script(ctx, vhost, strings.TrimPrefix(spec.Algorithm, CustomPrefix))
	if err != nil {
		return worker.Version{}, err
	}
	return s.trainOnPool(ctx, main, s.pools.Custom, vhost, name, spec, &script)
}

// trainOnPool copies the dataset to the pool under a name of its own, trains
// there, and imports the version into the main worker. The pool's copy and
// its version are removed whatever happens.
func (s *Service) trainOnPool(ctx context.Context, main, pool *worker.Client, vhost, name string, spec worker.TrainSpec, script *storage.MLScript) (worker.Version, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return worker.Version{}, fmt.Errorf("naming the training job: %w", err)
	}
	job := "job-" + hex.EncodeToString(buf)
	dataset := spec.Dataset

	data, err := main.ExportDataset(ctx, vhost, dataset)
	if err != nil {
		return worker.Version{}, fmt.Errorf("reading dataset %q: %w", dataset, err)
	}
	defer s.cleanPool(ctx, pool, vhost, job)
	_, err = pool.ImportDataset(ctx, vhost, job, data)
	_ = data.Close()
	if err != nil {
		return worker.Version{}, fmt.Errorf("copying dataset %q to the training pool: %w", dataset, err)
	}

	spec.Dataset = job
	var trained worker.Version
	if script != nil {
		trained, err = pool.TrainCustom(ctx, vhost, job, spec, worker.Script{Name: script.Name, SHA256: script.SHA256, Source: script.Source})
	} else {
		trained, err = pool.Train(ctx, vhost, job, spec)
	}
	if err != nil {
		return worker.Version{}, err
	}

	artifact, err := pool.ExportVersion(ctx, vhost, job, trained.Version)
	if err != nil {
		return worker.Version{}, fmt.Errorf("reading the trained version from the training pool: %w", err)
	}
	defer func() { _ = artifact.Close() }()
	v, err := main.ImportVersion(ctx, vhost, name, dataset, artifact)
	if err != nil {
		return worker.Version{}, fmt.Errorf("handing the trained version to the ML worker: %w", err)
	}
	return v, nil
}

// cleanPool removes a training's dataset and model from the pool. It runs
// after the caller's context may have ended, so it has its own deadline.
func (s *Service) cleanPool(ctx context.Context, pool *worker.Client, vhost, job string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), poolCleanupTimeout)
	defer cancel()
	if err := pool.DeleteModel(ctx, vhost, job); err != nil && !errors.Is(err, worker.ErrNotFound) {
		slog.Warn("ml: could not remove a training's model from its worker pool", "vhost", vhost, "job", job, "error", err)
	}
	if err := pool.DeleteDataset(ctx, vhost, job); err != nil && !errors.Is(err, worker.ErrNotFound) {
		slog.Warn("ml: could not remove a training's dataset from its worker pool", "vhost", vhost, "job", job, "error", err)
	}
}

// -- scripts -----------------------------------------------------------------

// Scripts returns the store's training scripts, or ErrCustomScriptsOff when
// the server has them off.
func (s *Service) Scripts() (storage.MLScriptStore, error) {
	if !s.pools.CustomScripts {
		return nil, ErrCustomScriptsOff
	}
	ss, ok := s.store().(storage.MLScriptStore)
	if !ok {
		return nil, ErrScriptsUnsupported
	}
	return ss, nil
}

// ListScripts lists the latest version of each of the vhost's scripts.
func (s *Service) ListScripts(ctx context.Context, vhost string) ([]storage.MLScript, error) {
	ss, err := s.Scripts()
	if err != nil {
		return nil, err
	}
	return ss.ListMLScripts(ctx, vhost)
}

// Script returns the latest version of a script, with its source.
func (s *Service) Script(ctx context.Context, vhost, name string) (storage.MLScript, error) {
	ss, err := s.Scripts()
	if err != nil {
		return storage.MLScript{}, err
	}
	sc, err := ss.GetMLScript(ctx, vhost, name)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.MLScript{}, fmt.Errorf("%w: %q in vhost %q", ErrScriptNotFound, name, vhost)
	}
	return sc, err
}

// ScriptVersions lists every version of a script, newest first.
func (s *Service) ScriptVersions(ctx context.Context, vhost, name string) ([]storage.MLScript, error) {
	ss, err := s.Scripts()
	if err != nil {
		return nil, err
	}
	return ss.ListMLScriptVersions(ctx, vhost, name)
}

// PutScript saves a script's source as its next version.
func (s *Service) PutScript(ctx context.Context, sc storage.MLScript) (storage.MLScript, error) {
	ss, err := s.Scripts()
	if err != nil {
		return storage.MLScript{}, err
	}
	if err := storage.ValidateMLScript(sc); err != nil {
		return storage.MLScript{}, fmt.Errorf("%w: %w", ErrBadTraining, err)
	}
	return ss.PutMLScript(ctx, sc)
}

// DeleteScript removes every version of a script. Model versions it trained
// keep their record of its name and SHA-256.
func (s *Service) DeleteScript(ctx context.Context, vhost, name string) error {
	ss, err := s.Scripts()
	if err != nil {
		return err
	}
	if err := ss.DeleteMLScript(ctx, vhost, name); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("%w: %q in vhost %q", ErrScriptNotFound, name, vhost)
		}
		return err
	}
	return nil
}
