package ml

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

const (
	// JobRetention is how long a job, and its result, is kept after it was
	// made.
	JobRetention = 7 * 24 * time.Hour
	// MaxRowsPerJob bounds one prediction job. The runner sends the rows in
	// calls of at most MaxRowsPerCall, so a job is how a caller with more
	// rows than one call takes sends them.
	MaxRowsPerJob = 50_000
	// maxInlineResult is the largest result kept on the job itself; a larger
	// one is kept apart and read on its own (JobResult).
	maxInlineResult = 64 << 10
	// MaxJobResultBytes bounds a result Hermod keeps. MongoDB holds one in a
	// document, at most 16 MB, so a larger answer fails the job with what to
	// do instead.
	MaxJobResultBytes = 12 << 20
	// defaultMaxQueuedJobs bounds the jobs a vhost may have waiting to run,
	// unless HERMOD_ML_MAX_QUEUED_JOBS says otherwise.
	defaultMaxQueuedJobs = 1000
	// DefaultSyncTimeout bounds a prediction or recommendation call that
	// waits for its answer, unless HERMOD_ML_SYNC_TIMEOUT says otherwise.
	DefaultSyncTimeout = 10 * time.Second
	// CallbackSecretName is the vhost secret a job's callback is signed with.
	CallbackSecretName = "ML_CALLBACK_SECRET" //nolint:gosec // G101: the name of a vhost secret, not a credential.
	// QuotaQueuedJobs names the bound on a vhost's queued jobs.
	QuotaQueuedJobs = "max_queued_jobs"
	// maxCallbackURL bounds a callback URL.
	maxCallbackURL = 2048
)

var (
	// ErrJobNotFound is returned for a job the vhost does not hold.
	ErrJobNotFound = errors.New("no job by that id")
	// ErrBadJob marks a job that is wrong in itself.
	ErrBadJob = errors.New("invalid job")
	// ErrSyncTimeout is returned for a call that waited for its answer
	// longer than the sync time limit.
	ErrSyncTimeout = errors.New("the call took longer than the limit for an answer while you wait")
	// ErrJobResultTooLarge is returned for a result over MaxJobResultBytes.
	ErrJobResultTooLarge = errors.New("the result is too large to keep")
	// ErrNoCallbackSecret is returned, with ErrBadJob, for a callback URL on
	// a vhost that holds no CallbackSecretName to sign it with.
	ErrNoCallbackSecret = errors.New("no callback signing secret")
)

// TrainJob is what a training job runs: the spec and how its version goes
// live, the body of POST .../train.
type TrainJob struct {
	Spec   worker.TrainSpec `json:"spec"`
	GoLive GoLive           `json:"go_live"`
}

// predictJob is what a prediction job runs.
type predictJob struct {
	Instances []inference.Row `json:"instances"`
}

// JobSubmission is one job to queue: the model of a vhost, the kind, and the
// request of that kind (Rows for a prediction, Recommend for
// recommendations, Train for a training). CallbackURL, when set, is told
// when the job ends. CallerKind and CreatedBy say who asked.
type JobSubmission struct {
	VHost, Model, Kind string

	Rows      []inference.Row
	Recommend *RecommendRequest
	Train     *TrainJob

	CallbackURL string
	CallerKind  string
	CreatedBy   string
}

// jobWake tells this process's runner a job was queued, so it starts without
// waiting for its next poll. One pending signal is enough.
var jobWake = make(chan struct{}, 1)

func wakeRunner() {
	select {
	case jobWake <- struct{}{}:
	default:
	}
}

// The server-wide job settings, read once.
var (
	envSyncTimeout = sync.OnceValue(func() time.Duration {
		return durationEnv("HERMOD_ML_SYNC_TIMEOUT", DefaultSyncTimeout)
	})
	envMaxQueuedJobs = sync.OnceValue(func() int {
		n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("HERMOD_ML_MAX_QUEUED_JOBS")))
		if err != nil || n < 0 {
			return defaultMaxQueuedJobs
		}
		return n
	})
)

// durationEnv reads a Go duration ("30s") or a number of seconds; a value
// that is unset or unreadable is def, and "0" turns the limit off.
func durationEnv(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil && d >= 0 {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	return def
}

// WithSyncTimeout replaces the sync time limit HERMOD_ML_SYNC_TIMEOUT sets;
// zero turns it off.
func (s *Service) WithSyncTimeout(d time.Duration) *Service {
	s.syncTimeout = d
	return s
}

// SyncTimeoutError is a call that waited longer than Limit for its answer.
type SyncTimeoutError struct{ Limit time.Duration }

func (e *SyncTimeoutError) Error() string {
	return fmt.Sprintf("the model did not answer within %s, the limit for a call that waits for its answer; "+
		`send "respond": "async" in the body, or the header "Prefer: respond-async", to run it as a job and read the answer when it is ready`,
		e.Limit)
}

// Is makes errors.Is(err, ErrSyncTimeout) true.
func (e *SyncTimeoutError) Is(target error) bool { return target == ErrSyncTimeout }

// Sync runs call, a prediction or recommendation a caller waits for, within
// the sync time limit, and turns running over it into a SyncTimeoutError
// that says how to run the call as a job instead. A model whose own timeout
// is longer than the limit keeps its own: whoever set it expects its calls
// to take that long. A caller that went away is not told it timed out.
func (s *Service) Sync(ctx context.Context, vhost, name string, call func(context.Context) error) error {
	limit := s.syncTimeout
	if limit <= 0 {
		return call(ctx)
	}
	if m, err := s.Model(ctx, vhost, name); err == nil && m.TimeoutMs > 0 {
		limit = max(limit, time.Duration(m.TimeoutMs)*time.Millisecond)
	}
	limited, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	err := call(limited)
	if err != nil && ctx.Err() == nil && errors.Is(limited.Err(), context.DeadlineExceeded) {
		return &SyncTimeoutError{Limit: limit}
	}
	return err
}

// Jobs returns the store's job store, or ErrMLJobsUnsupported.
func (s *Service) Jobs() (storage.MLJobStore, error) {
	js, ok := s.store().(storage.MLJobStore)
	if !ok {
		return nil, storage.ErrMLJobsUnsupported
	}
	return js, nil
}

func badJob(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadJob, fmt.Sprintf(format, args...))
}

// CheckCallbackURL reports what is wrong with a callback URL. Hermod posts to
// it on its own, so it is held to the rules of a URL fetched from somewhere
// the operator does not control (handlers.IsSafeWasmURL): http or https, a
// host that is neither localhost nor a loopback, link-local, private or
// unspecified address. A name that resolves to such an address is refused
// again when the callback is sent, by httpclient.SafeDialer. Credentials in
// the URL are refused too, so they cannot end up in the job listing.
func CheckCallbackURL(raw string) error {
	if len(raw) > maxCallbackURL {
		return badJob("a callback URL is at most %d characters", maxCallbackURL)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return badJob("callback_url must be an http or https URL")
	}
	if u.User != nil {
		return badJob("callback_url must not hold a user name or password; the callback is signed instead")
	}
	if internalHost(u.Hostname()) {
		return badJob("callback_url must not point at this machine, or at a private, loopback or link-local address")
	}
	return nil
}

// internalHost reports whether host names this machine or an address inside
// a network: localhost, or a loopback, link-local, private or unspecified IP.
func internalHost(host string) bool {
	lower := strings.ToLower(host)
	if lower == "" || lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil &&
		(ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() || ip.IsUnspecified())
}

// callbackSecret is the vhost's callback signing secret, or "" when it holds
// none.
func (s *Service) callbackSecret(ctx context.Context, vhost string) (string, error) {
	if s.secrets == nil {
		return "", nil
	}
	return s.secrets.GetScoped(ctx, vhost, CallbackSecretName)
}

// encode checks a submission and returns the request its job stores.
func (s *Service) encode(ctx context.Context, sub JobSubmission) (json.RawMessage, error) {
	switch sub.Kind {
	case storage.MLJobPredict:
		return s.encodePredict(ctx, sub)
	case storage.MLJobRecommend:
		return s.encodeRecommend(ctx, sub)
	case storage.MLJobTrain:
		if sub.Train == nil {
			return nil, badJob("a training job needs a training spec")
		}
		if _, err := s.Worker(); err != nil {
			return nil, err
		}
		if err := s.checkTraining(sub.Model, sub.Train.Spec, sub.Train.GoLive); err != nil {
			return nil, err
		}
		return json.Marshal(sub.Train)
	}
	return nil, badJob("a job is %q, %q or %q, not %q", storage.MLJobPredict, storage.MLJobRecommend, storage.MLJobTrain, sub.Kind)
}

// encodePredict checks a prediction job: rows, within MaxRowsPerJob, for a
// model of the vhost that predicts rows.
func (s *Service) encodePredict(ctx context.Context, sub JobSubmission) (json.RawMessage, error) {
	if len(sub.Rows) == 0 {
		return nil, badJob(`"instances" must hold at least one row`)
	}
	if len(sub.Rows) > MaxRowsPerJob {
		return nil, fmt.Errorf("%w: a job takes at most %d rows", ErrTooManyRows, MaxRowsPerJob)
	}
	m, err := s.Model(ctx, sub.VHost, sub.Model)
	if err != nil {
		return nil, err
	}
	if m.IsRecommender() {
		return nil, fmt.Errorf("model %q: %w", sub.Model, ErrRecommender)
	}
	return json.Marshal(predictJob{Instances: sub.Rows})
}

// encodeRecommend checks a recommendation job: a request a recommendation
// model of the vhost can answer.
func (s *Service) encodeRecommend(ctx context.Context, sub JobSubmission) (json.RawMessage, error) {
	if sub.Recommend == nil {
		return nil, badJob("a recommendation job needs a user or an item")
	}
	if _, _, _, _, err := sub.Recommend.check(); err != nil {
		return nil, err
	}
	m, err := s.Model(ctx, sub.VHost, sub.Model)
	if err != nil {
		return nil, err
	}
	if !m.IsRecommender() {
		return nil, fmt.Errorf("model %q: %w", sub.Model, ErrNotRecommender)
	}
	return json.Marshal(sub.Recommend)
}

// checkTraining refuses a training the worker would never be asked for: the
// checks train makes before it starts.
func (s *Service) checkTraining(name string, spec worker.TrainSpec, rule GoLive) error {
	if spec.Dataset == "" || (spec.Target == "" && spec.Task != worker.TaskRecommendation) {
		return badJob("name the dataset to train on and the target column to predict")
	}
	if err := rule.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrBadJob, err)
	}
	if err := checkRecommendSpec(spec); err != nil {
		return err
	}
	if err := checkSpec(spec); err != nil {
		return err
	}
	if !storage.ValidMLModelName(name) {
		return badJob("model name %q must start with a letter and hold only letters, digits, '_' or '-'", name)
	}
	return nil
}

// newJobID is a job's id: random, so one vhost cannot guess another's.
func newJobID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "job_" + hex.EncodeToString(b)
}

// SubmitJob queues a job and returns it. Everything a job could be refused
// for in itself is refused here, so a queued job fails only for what
// happens when it runs. A vhost may have at most HERMOD_ML_MAX_QUEUED_JOBS
// jobs waiting; the quotas a call is held to apply when the job runs.
func (s *Service) SubmitJob(ctx context.Context, sub JobSubmission) (storage.MLJob, error) {
	js, err := s.Jobs()
	if err != nil {
		return storage.MLJob{}, err
	}
	req, err := s.encode(ctx, sub)
	if err != nil {
		return storage.MLJob{}, err
	}
	if sub.CallbackURL != "" {
		if err := s.checkCallback(sub.CallbackURL); err != nil {
			return storage.MLJob{}, err
		}
		secret, err := s.callbackSecret(ctx, sub.VHost)
		if err != nil {
			return storage.MLJob{}, fmt.Errorf("reading the callback signing secret: %w", err)
		}
		if secret == "" {
			return storage.MLJob{}, fmt.Errorf("%w: %w: a callback is signed with the vhost secret %s, which vhost %q does not hold; "+
				"an Administrator adds it under the vhost's secrets", ErrBadJob, ErrNoCallbackSecret, CallbackSecretName, sub.VHost)
		}
	}
	if s.maxQueuedJobs > 0 {
		_, queued, err := js.ListMLJobs(ctx, storage.MLJobFilter{VHost: sub.VHost, Status: storage.MLJobQueued, Limit: 1})
		if err != nil {
			return storage.MLJob{}, err
		}
		if queued >= s.maxQueuedJobs {
			return storage.MLJob{}, refuse(sub.VHost, QuotaQueuedJobs, true,
				"may have at most %d jobs waiting to run; try again when some have started", s.maxQueuedJobs)
		}
	}
	now := time.Now().UTC()
	j := storage.MLJob{
		ID: newJobID(), VHost: sub.VHost, Model: sub.Model, Kind: sub.Kind, Status: storage.MLJobQueued,
		ProgressMessage: "Waiting to start", Request: req, CallbackURL: sub.CallbackURL,
		CallerKind: sub.CallerKind, CreatedBy: sub.CreatedBy, CreatedAt: now, ExpiresAt: now.Add(JobRetention),
	}
	if err := js.CreateMLJob(ctx, j); err != nil {
		return storage.MLJob{}, err
	}
	jobsSubmitted.WithLabelValues(j.VHost, j.Kind).Inc()
	wakeRunner()
	return j, nil
}

// Job returns one job of the vhost, or ErrJobNotFound.
func (s *Service) Job(ctx context.Context, vhost, id string) (storage.MLJob, error) {
	js, err := s.Jobs()
	if err != nil {
		return storage.MLJob{}, err
	}
	j, err := js.GetMLJob(ctx, vhost, id)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.MLJob{}, ErrJobNotFound
	}
	return j, err
}

// JobResult returns a finished job's result, wherever it is kept, or nil for
// a job without one.
func (s *Service) JobResult(ctx context.Context, vhost, id string) (json.RawMessage, error) {
	j, err := s.Job(ctx, vhost, id)
	if err != nil {
		return nil, err
	}
	if j.ResultRef != storage.MLJobResultStored {
		return j.Result, nil
	}
	js, err := s.Jobs()
	if err != nil {
		return nil, err
	}
	raw, err := js.GetMLJobResult(ctx, vhost, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	return raw, err
}

// ListJobs returns a page of the vhost's jobs, newest first, and how many
// match.
func (s *Service) ListJobs(ctx context.Context, f storage.MLJobFilter) ([]storage.MLJob, int, error) {
	js, err := s.Jobs()
	if err != nil {
		return nil, 0, err
	}
	switch f.Status {
	case "", storage.MLJobQueued, storage.MLJobRunning, storage.MLJobSucceeded, storage.MLJobFailed, storage.MLJobCancelled:
	default:
		return nil, 0, badJob("status is queued, running, succeeded, failed or cancelled, not %q", f.Status)
	}
	return js.ListMLJobs(ctx, f)
}

// CancelJob cancels a queued job at once, and asks the Hermod running a
// running one to stop it, which it does within a few seconds. A finished job
// is returned as it is.
func (s *Service) CancelJob(ctx context.Context, vhost, id string) (storage.MLJob, error) {
	js, err := s.Jobs()
	if err != nil {
		return storage.MLJob{}, err
	}
	j, err := js.CancelMLJob(ctx, vhost, id, time.Now())
	if errors.Is(err, storage.ErrNotFound) {
		return storage.MLJob{}, ErrJobNotFound
	}
	if err == nil && j.Status == storage.MLJobCancelled {
		wakeRunner() // its callback, if it has one, is due
	}
	return j, err
}

// JobStatusPath is where a job is read: the serving path for a job submitted
// with a serving key, the vhost's API otherwise.
func JobStatusPath(j storage.MLJob) string {
	if j.CallerKind == storage.MLCallerREST || j.CallerKind == storage.MLCallerGRPC {
		return "/api/ml/serve/" + url.PathEscape(j.VHost) + "/jobs/" + j.ID
	}
	return "/api/vhosts/" + url.PathEscape(j.VHost) + "/ml/jobs/" + j.ID
}

// ServingJob reports whether a serving key may read the job: one submitted
// with a serving key, for the model the key is for.
func ServingJob(j storage.MLJob, model string) bool {
	return j.Model == model && (j.CallerKind == storage.MLCallerREST || j.CallerKind == storage.MLCallerGRPC)
}
