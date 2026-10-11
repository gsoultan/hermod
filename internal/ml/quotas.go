package ml

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/gsoultan/hermod/internal/storage"
)

// Quota names, as the API, the errors and the refusals metric spell them.
const (
	QuotaDatasets             = "max_datasets"
	QuotaDatasetRows          = "max_dataset_rows"
	QuotaDatasetBytes         = "max_dataset_bytes"
	QuotaModels               = "max_models"
	QuotaConcurrentTrainings  = "max_concurrent_trainings"
	QuotaPredictionsPerSecond = "max_predictions_per_second"
)

// ErrQuotaExceeded matches every QuotaError with errors.Is.
var ErrQuotaExceeded = errors.New("ML quota exceeded")

// QuotaError is a refusal because a vhost is at one of its ML quotas.
// Retryable is set when waiting is enough (a training finishing, the next
// second's predictions); otherwise an Administrator has to raise the quota or
// the vhost has to free something.
type QuotaError struct {
	VHost     string
	Quota     string
	Retryable bool
	msg       string
}

func (e *QuotaError) Error() string { return e.msg }

// Is makes errors.Is(err, ErrQuotaExceeded) true for every QuotaError.
func (e *QuotaError) Is(target error) bool { return target == ErrQuotaExceeded }

// refuse counts the refusal and returns it.
func refuse(vhost, quota string, retryable bool, format string, args ...any) *QuotaError {
	quotaRefusals.WithLabelValues(vhost, quota).Inc()
	return &QuotaError{
		VHost: vhost, Quota: quota, Retryable: retryable,
		msg: fmt.Sprintf("vhost %q ", vhost) + fmt.Sprintf(format, args...) + " (ML quota " + quota + ")",
	}
}

// Quotas are the limits in force for a vhost: its own where it has them, the
// server's default otherwise. Zero means no limit.
type Quotas struct {
	MaxDatasets             int64   `json:"max_datasets"`
	MaxDatasetRows          int64   `json:"max_dataset_rows"`
	MaxDatasetBytes         int64   `json:"max_dataset_bytes"`
	MaxModels               int64   `json:"max_models"`
	MaxConcurrentTrainings  int64   `json:"max_concurrent_trainings"`
	MaxPredictionsPerSecond float64 `json:"max_predictions_per_second"`
}

// The environment variables that set the server-wide defaults.
var quotaEnv = map[string]string{
	QuotaDatasets:             "HERMOD_ML_MAX_DATASETS",
	QuotaDatasetRows:          "HERMOD_ML_MAX_DATASET_ROWS",
	QuotaDatasetBytes:         "HERMOD_ML_MAX_DATASET_BYTES",
	QuotaModels:               "HERMOD_ML_MAX_MODELS",
	QuotaConcurrentTrainings:  "HERMOD_ML_MAX_CONCURRENT_TRAININGS",
	QuotaPredictionsPerSecond: "HERMOD_ML_MAX_PREDICTIONS_PER_SECOND",
}

// QuotaDefaultsFromEnv reads the server-wide defaults. A variable that is
// unset, unreadable or negative sets nothing, so that quota is unlimited for
// a vhost without its own.
func QuotaDefaultsFromEnv() storage.MLQuotas {
	count := func(quota string) *int64 {
		n, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(quotaEnv[quota])), 10, 64)
		if err != nil || n < 0 {
			return nil
		}
		return &n
	}
	var d storage.MLQuotas
	d.MaxDatasets, d.MaxDatasetRows, d.MaxDatasetBytes = count(QuotaDatasets), count(QuotaDatasetRows), count(QuotaDatasetBytes)
	d.MaxModels, d.MaxConcurrentTrainings = count(QuotaModels), count(QuotaConcurrentTrainings)
	if f, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(quotaEnv[QuotaPredictionsPerSecond])), 64); err == nil &&
		f >= 0 && !math.IsInf(f, 0) {
		d.MaxPredictionsPerSecond = &f
	}
	return d
}

// envQuotaDefaults is QuotaDefaultsFromEnv, read once.
var envQuotaDefaults = sync.OnceValue(QuotaDefaultsFromEnv)

// WithQuotaDefaults replaces the server-wide defaults the environment sets.
func (s *Service) WithQuotaDefaults(d storage.MLQuotas) *Service {
	s.quotaDefaults = d
	return s
}

// QuotaDefaults returns the server-wide defaults.
func (s *Service) QuotaDefaults() storage.MLQuotas {
	return s.quotaDefaults
}

// StoredQuotas returns the quotas set on the vhost itself; a vhost with none
// set gets an empty set, every quota falling back to the default.
func (s *Service) StoredQuotas(ctx context.Context, vhost string) (storage.MLQuotas, error) {
	qs, ok := s.store().(storage.MLQuotaStore)
	if !ok {
		return storage.MLQuotas{VHost: vhost}, nil
	}
	q, err := qs.GetMLQuotas(ctx, vhost)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.MLQuotas{VHost: vhost}, nil
	}
	if err != nil {
		return storage.MLQuotas{}, err
	}
	return q, nil
}

// SetQuotas replaces the vhost's own quotas. Each call to a quota reads them
// afresh, so a change applies from the next call on every replica.
func (s *Service) SetQuotas(ctx context.Context, q storage.MLQuotas) error {
	if err := storage.ValidateMLQuotas(q); err != nil {
		return err
	}
	qs, ok := s.store().(storage.MLQuotaStore)
	if !ok {
		return storage.ErrMLQuotasUnsupported
	}
	return qs.PutMLQuotas(ctx, q)
}

// Quotas returns the limits in force for the vhost.
func (s *Service) Quotas(ctx context.Context, vhost string) (Quotas, error) {
	own, err := s.StoredQuotas(ctx, vhost)
	if err != nil {
		return Quotas{}, fmt.Errorf("reading the ML quotas of vhost %q: %w", vhost, err)
	}
	d := s.quotaDefaults
	pick := func(own, def *int64) int64 {
		switch {
		case own != nil:
			return *own
		case def != nil:
			return *def
		}
		return 0
	}
	q := Quotas{
		MaxDatasets: pick(own.MaxDatasets, d.MaxDatasets), MaxDatasetRows: pick(own.MaxDatasetRows, d.MaxDatasetRows),
		MaxDatasetBytes: pick(own.MaxDatasetBytes, d.MaxDatasetBytes), MaxModels: pick(own.MaxModels, d.MaxModels),
		MaxConcurrentTrainings: pick(own.MaxConcurrentTrainings, d.MaxConcurrentTrainings),
	}
	switch {
	case own.MaxPredictionsPerSecond != nil:
		q.MaxPredictionsPerSecond = *own.MaxPredictionsPerSecond
	case d.MaxPredictionsPerSecond != nil:
		q.MaxPredictionsPerSecond = *d.MaxPredictionsPerSecond
	}
	return q, nil
}

// usage is what this replica is doing for each vhost right now: trainings
// running, and the prediction token buckets. It is per replica — a Service is
// built per use, so it lives here — and so are the two quotas it enforces.
var usage = struct {
	mu        sync.Mutex
	trainings map[string]int64
	buckets   map[string]*rate.Limiter
	creating  map[string]*sync.Mutex
}{trainings: map[string]int64{}, buckets: map[string]*rate.Limiter{}, creating: map[string]*sync.Mutex{}}

// startTraining takes one of the vhost's training slots, or refuses; the
// returned func gives it back.
func startTraining(vhost string, limit int64) (func(), error) {
	usage.mu.Lock()
	defer usage.mu.Unlock()
	if limit > 0 && usage.trainings[vhost] >= limit {
		return nil, refuse(vhost, QuotaConcurrentTrainings, true,
			"may run at most %d training(s) at a time; try again when one finishes", limit)
	}
	usage.trainings[vhost]++
	return func() {
		usage.mu.Lock()
		defer usage.mu.Unlock()
		if usage.trainings[vhost]--; usage.trainings[vhost] <= 0 {
			delete(usage.trainings, vhost)
		}
	}, nil
}

// allowPredictions takes rows tokens from the vhost's bucket, which holds one
// second's worth and refills at perSecond.
func allowPredictions(vhost string, perSecond float64, rows int, now time.Time) error {
	if perSecond <= 0 {
		return nil
	}
	burst := max(int(math.Ceil(perSecond)), 1)
	usage.mu.Lock()
	b, ok := usage.buckets[vhost]
	if !ok {
		b = rate.NewLimiter(rate.Limit(perSecond), burst)
		usage.buckets[vhost] = b
	} else if b.Limit() != rate.Limit(perSecond) || b.Burst() != burst {
		b.SetLimitAt(now, rate.Limit(perSecond))
		b.SetBurstAt(now, burst)
	}
	usage.mu.Unlock()

	if rows > burst {
		return refuse(vhost, QuotaPredictionsPerSecond, true,
			"may send its models at most %g rows per second, and this call holds %d; split it", perSecond, rows)
	}
	if !b.AllowN(now, rows) {
		return refuse(vhost, QuotaPredictionsPerSecond, true,
			"may send its models at most %g rows per second; try again shortly", perSecond)
	}
	return nil
}

// creationLock serializes the count-then-create of the vhost's models on this
// replica, so two creations at once cannot both take the last place.
func creationLock(vhost string) *sync.Mutex {
	usage.mu.Lock()
	defer usage.mu.Unlock()
	mu, ok := usage.creating[vhost]
	if !ok {
		mu = &sync.Mutex{}
		usage.creating[vhost] = mu
	}
	return mu
}

// checkNewModel refuses a model the vhost does not hold yet when it already
// holds as many as it may.
func (s *Service) checkNewModel(ctx context.Context, vhost, name string, limit int64) error {
	if limit <= 0 {
		return nil
	}
	ms, err := s.Models()
	if err != nil {
		return err
	}
	list, err := ms.ListMLModels(ctx, vhost)
	if err != nil {
		return fmt.Errorf("counting the models of vhost %q: %w", vhost, err)
	}
	for _, m := range list {
		if m.Name == name {
			return nil
		}
	}
	if int64(len(list)) >= limit {
		return refuse(vhost, QuotaModels, false, "may hold at most %d model(s) and holds %d", limit, len(list))
	}
	return nil
}

// checkNewDataset refuses a dataset the vhost does not hold yet when it
// already holds as many as it may. Datasets live on the worker, so two
// created at the same moment can both pass.
func (s *Service) checkNewDataset(ctx context.Context, vhost, name string, limit int64) error {
	if limit <= 0 {
		return nil
	}
	w, err := s.Worker()
	if err != nil {
		return err
	}
	list, err := w.Datasets(ctx, vhost)
	if err != nil {
		return fmt.Errorf("counting the datasets of vhost %q: %w", vhost, err)
	}
	for _, d := range list {
		if d.Name == name {
			return nil
		}
	}
	if int64(len(list)) >= limit {
		return refuse(vhost, QuotaDatasets, false, "may hold at most %d dataset(s) and holds %d", limit, len(list))
	}
	return nil
}
