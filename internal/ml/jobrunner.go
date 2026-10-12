package ml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/httpclient"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

const (
	// defaultJobWorkers is how many jobs one Hermod runs at a time, unless
	// HERMOD_ML_JOB_WORKERS says otherwise.
	defaultJobWorkers = 2
	// jobClaimTTL is how long a claim on a running job lasts without being
	// renewed. A Hermod that stops while running a job leaves its claim, and
	// another takes the job over once this much time has passed.
	jobClaimTTL = time.Minute
	// jobHeartbeat is how often a running job renews its claim, records its
	// progress and looks whether it was cancelled.
	jobHeartbeat = 5 * time.Second
	// jobPoll is how often a runner looks for jobs no one told it about:
	// those queued on another Hermod, and expired claims.
	jobPoll = 2 * time.Second
	// jobPurgeInterval is how often expired jobs are removed.
	jobPurgeInterval = time.Hour
	// maxJobAttempts is how many times a job is started before it is given
	// up: a job that takes down every Hermod that runs it is not tried
	// forever.
	maxJobAttempts = 3
	// maxQuotaWait bounds how long a job waits, in all, for a quota or a
	// busy worker to let it through.
	maxQuotaWait = time.Hour
)

// JobRunner runs the queued ML jobs of every vhost. Every Hermod serving the
// API runs one; a job is claimed in storage before it runs, so each runs on
// one of them, and a job whose Hermod stopped is taken over by another once
// its claim expires.
type JobRunner struct {
	svc     func() *Service
	logger  hermod.Logger
	owner   string
	workers int
	now     func() time.Time
	client  *http.Client

	claimTTL, heartbeat, poll time.Duration
	// wake is told when one of this runner's jobs ends, so a free slot is
	// filled at once.
	wake chan struct{}
}

// NewJobRunner builds a JobRunner. svc is called on every round, because
// setup and a database switch replace the store while Hermod runs. logger
// may be nil.
func NewJobRunner(svc func() *Service, logger hermod.Logger) *JobRunner {
	workers := defaultJobWorkers
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("HERMOD_ML_JOB_WORKERS"))); err == nil && n > 0 {
		workers = n
	}
	return &JobRunner{
		svc: svc, logger: logger, owner: newClaimOwner(), workers: workers, now: time.Now,
		client:   callbackClient(),
		claimTTL: jobClaimTTL, heartbeat: jobHeartbeat, poll: jobPoll,
		wake: make(chan struct{}, 1),
	}
}

// Run runs jobs, up to its number of workers at a time, and sends their
// callbacks, until ctx ends. Jobs still running then are left to their
// claims: another Hermod, or this one restarted, takes them over.
func (r *JobRunner) Run(ctx context.Context) {
	slots := make(chan struct{}, r.workers)
	var running sync.WaitGroup
	defer running.Wait()
	poll := time.NewTicker(r.poll)
	defer poll.Stop()
	purge := time.NewTicker(jobPurgeInterval)
	defer purge.Stop()
	for {
		if svc := r.svc(); svc != nil {
			r.fill(ctx, svc, slots, &running)
			r.sendCallbacks(ctx, svc)
		}
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		case <-jobWake:
		case <-r.wake:
		case <-purge.C:
			r.purge(ctx)
		}
	}
}

// fill claims jobs while a worker is free, and runs each on its own.
func (r *JobRunner) fill(ctx context.Context, svc *Service, slots chan struct{}, running *sync.WaitGroup) {
	js, err := svc.Jobs()
	if err != nil {
		return
	}
	for ctx.Err() == nil {
		select {
		case slots <- struct{}{}:
		default:
			return // every worker is busy
		}
		j, ok, err := js.ClaimMLJob(ctx, r.owner, r.claimTTL, r.now())
		if err != nil || !ok {
			<-slots
			if err != nil && ctx.Err() == nil {
				r.warn("Looking for ML jobs to run failed", "error", err)
			}
			return
		}
		running.Go(func() {
			defer func() {
				<-slots
				select {
				case r.wake <- struct{}{}:
				default:
				}
			}()
			r.runOne(ctx, svc, js, j)
		})
	}
}

// Tick runs, one after another, every job that can be claimed now, then
// sends the callbacks that are due. Run does the same with workers; Tick is
// for a caller that wants it done before it goes on.
func (r *JobRunner) Tick(ctx context.Context) {
	svc := r.svc()
	if svc == nil {
		return
	}
	js, err := svc.Jobs()
	if err != nil {
		return
	}
	for ctx.Err() == nil {
		j, ok, err := js.ClaimMLJob(ctx, r.owner, r.claimTTL, r.now())
		if err != nil || !ok {
			break
		}
		r.runOne(ctx, svc, js, j)
	}
	r.sendCallbacks(ctx, svc)
}

// purge removes the jobs, and results, past their seven days.
func (r *JobRunner) purge(ctx context.Context) {
	svc := r.svc()
	if svc == nil {
		return
	}
	js, err := svc.Jobs()
	if err != nil {
		return
	}
	if n, err := js.DeleteExpiredMLJobs(ctx, r.now()); err != nil {
		r.warn("Removing expired ML jobs failed", "error", err)
	} else if n > 0 {
		r.info("Removed expired ML jobs", "jobs", n)
	}
}

// progress is what a running job has reported, written to the store by its
// heartbeat.
type progress struct {
	mu      sync.Mutex
	value   float64
	message string
	changed chan struct{}
}

func (p *progress) report(value float64, message string) {
	p.mu.Lock()
	p.value, p.message = math.Max(0, math.Min(value, 1)), message
	p.mu.Unlock()
	select {
	case p.changed <- struct{}{}:
	default:
	}
}

func (p *progress) get() (float64, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.value, p.message
}

// runOne runs a claimed job to its end and records it. While it runs, a
// heartbeat renews the claim, writes its progress and stops it when it is
// cancelled or the claim is lost.
func (r *JobRunner) runOne(ctx context.Context, svc *Service, js storage.MLJobStore, j storage.MLJob) {
	start := time.Now()
	finish := func(status string, result json.RawMessage, failure error) {
		r.finish(ctx, js, j, status, result, failure, time.Since(start))
	}
	switch {
	case j.CancelRequested:
		finish(storage.MLJobCancelled, nil, nil)
		return
	case j.Attempts > maxJobAttempts:
		finish(storage.MLJobFailed, nil, fmt.Errorf("the job was started %d times and stopped each time before it finished, "+
			"because the Hermod running it went away; it is not tried again", j.Attempts-1))
		return
	}

	jobCtx, stop := context.WithCancel(ctx)
	defer stop()
	p := &progress{changed: make(chan struct{}, 1)}
	p.report(0, "Started")
	var hb beat
	done := make(chan struct{})
	go func() {
		defer close(done)
		hb = r.beat(jobCtx, stop, js, j, p)
	}()

	result, err := r.execute(jobCtx, svc, j, p.report)
	stop()
	<-done
	cancelled, lost := hb == beatCancelled, hb == beatLost

	switch {
	case lost:
		r.warn("Lost the claim on an ML job while running it; another Hermod has it", "vhost", j.VHost, "job", j.ID)
	case cancelled:
		finish(storage.MLJobCancelled, nil, nil)
	case ctx.Err() != nil:
		// This Hermod is stopping. The claim expires and the job is taken
		// over, here after a restart or by another Hermod.
	case err != nil:
		finish(storage.MLJobFailed, nil, err)
	default:
		finish(storage.MLJobSucceeded, result, nil)
	}
}

// How a job's heartbeat ended: with the job, because it was cancelled, or
// because the claim on it was lost.
type beat int

const (
	beatEnded beat = iota
	beatCancelled
	beatLost
)

// beat renews the claim on a running job and writes its progress, every
// heartbeat and whenever the progress changes, until jobCtx ends. It stops
// the job, with stop, when cancelling it was asked for or the claim was
// lost, and says which.
func (r *JobRunner) beat(jobCtx context.Context, stop func(), js storage.MLJobStore, j storage.MLJob, p *progress) beat {
	t := time.NewTicker(r.heartbeat)
	defer t.Stop()
	for {
		select {
		case <-jobCtx.Done():
			return beatEnded
		case <-t.C:
		case <-p.changed:
		}
		value, message := p.get()
		cancel, err := js.UpdateMLJobProgress(jobCtx, j.ID, r.owner, value, message, r.now().Add(r.claimTTL))
		switch {
		case errors.Is(err, storage.ErrMLJobClaimLost):
			stop()
			return beatLost
		case err == nil && cancel:
			stop()
			return beatCancelled
		}
	}
}

// finish records how the job ended. A result larger than maxInlineResult is
// kept apart; one larger than MaxJobResultBytes fails the job instead.
func (r *JobRunner) finish(ctx context.Context, js storage.MLJobStore, j storage.MLJob, status string,
	result json.RawMessage, failure error, took time.Duration) {
	ctx = context.WithoutCancel(ctx)
	f := storage.MLJobFinish{Status: status, At: r.now()}
	switch {
	case failure != nil:
		f.Error = failure.Error()
	case len(result) > MaxJobResultBytes:
		f.Status, f.Error = storage.MLJobFailed, fmt.Sprintf("%v: it is %.1f MB and a job keeps at most %d MB; send fewer rows per job",
			ErrJobResultTooLarge, float64(len(result))/(1<<20), MaxJobResultBytes>>20)
	case len(result) > maxInlineResult:
		if err := js.PutMLJobResult(ctx, j.VHost, j.ID, result); err != nil {
			f.Status, f.Error = storage.MLJobFailed, "the result could not be saved: "+err.Error()
		} else {
			f.ResultRef = storage.MLJobResultStored
		}
	default:
		f.Result = result
	}
	if err := js.FinishMLJob(ctx, j.ID, r.owner, f); err != nil {
		r.warn("Recording the end of an ML job failed", "vhost", j.VHost, "job", j.ID, "error", err)
		return
	}
	jobsFinished.WithLabelValues(j.VHost, j.Kind, f.Status).Inc()
	jobSeconds.WithLabelValues(j.Kind).Observe(took.Seconds())
	if f.Status == storage.MLJobFailed {
		r.info("An ML job failed", "vhost", j.VHost, "model", j.Model, "job", j.ID, "kind", j.Kind, "error", f.Error)
	}
}

// execute runs the job's request through the service, as the call it stands
// for would, and returns the answer that call would have given.
func (r *JobRunner) execute(ctx context.Context, svc *Service, j storage.MLJob, report func(float64, string)) (json.RawMessage, error) {
	ctx = WithCaller(ctx, j.CallerKind, "")
	switch j.Kind {
	case storage.MLJobPredict:
		var req predictJob
		if err := json.Unmarshal(j.Request, &req); err != nil {
			return nil, fmt.Errorf("reading the job's rows: %w", err)
		}
		preds, err := r.predict(ctx, svc, j, req.Instances, report)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"model": j.Model, "predictions": preds})
	case storage.MLJobRecommend:
		var req RecommendRequest
		if err := json.Unmarshal(j.Request, &req); err != nil {
			return nil, fmt.Errorf("reading the job's request: %w", err)
		}
		report(0, "Finding recommendations")
		var items any
		err := waitForQuota(ctx, func() error {
			got, err := svc.Recommend(ctx, j.VHost, j.Model, req)
			items = got
			return err
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"model": j.Model, "items": items})
	case storage.MLJobTrain:
		var req TrainJob
		if err := json.Unmarshal(j.Request, &req); err != nil {
			return nil, fmt.Errorf("reading the job's training spec: %w", err)
		}
		report(0.05, "Training on the ML worker")
		var res TrainResult
		err := waitForQuota(ctx, func() error {
			var err error
			res, err = svc.trainAs(ctx, j.ID, j.VHost, j.Model, req.Spec, req.GoLive, j.CreatedBy)
			return err
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	}
	return nil, fmt.Errorf("%w: unknown job kind %q", ErrBadJob, j.Kind)
}

// predict sends the rows in calls the vhost's quota lets through: at most
// MaxRowsPerCall, and at most one second's worth of its rate, so a job larger
// than either is paced rather than refused.
func (r *JobRunner) predict(ctx context.Context, svc *Service, j storage.MLJob, rows []inference.Row,
	report func(float64, string)) ([]inference.Row, error) {
	chunk := MaxRowsPerCall
	if q, err := svc.Quotas(ctx, j.VHost); err == nil && q.MaxPredictionsPerSecond > 0 {
		chunk = max(1, min(chunk, int(math.Ceil(q.MaxPredictionsPerSecond))))
	}
	out := make([]inference.Row, 0, len(rows))
	total := len(rows)
	report(0, fmt.Sprintf("0 of %s rows", thousands(total)))
	for len(out) < total {
		part := rows[len(out):min(len(out)+chunk, total)]
		var preds []inference.Row
		err := waitForQuota(ctx, func() error {
			var err error
			preds, err = svc.Predict(ctx, j.VHost, j.Model, part)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("after %s of %s rows: %w", thousands(len(out)), thousands(total), err)
		}
		out = append(out, preds...)
		report(float64(len(out))/float64(total), fmt.Sprintf("%s of %s rows", thousands(len(out)), thousands(total)))
	}
	return out, nil
}

// waitForQuota runs call, and runs it again after a pause while it is
// refused for a reason waiting cures: a quota that frees up (the rate, a
// training slot) or a worker busy with another training. It gives up after
// maxQuotaWait, or when ctx ends.
func waitForQuota(ctx context.Context, call func() error) error {
	pause, waited := 250*time.Millisecond, time.Duration(0)
	for {
		err := call()
		var quota *QuotaError
		retry := (errors.As(err, &quota) && quota.Retryable) || errors.Is(err, worker.ErrBusy)
		if !retry || waited >= maxQuotaWait {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pause):
		}
		waited += pause
		pause = min(pause*2, 30*time.Second)
	}
}

// thousands writes n with commas, as the UI shows counts.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// callbackClient is the client callbacks are sent with: httpclient's secure
// client, which refuses to connect to a private address whatever a name
// resolves to, and follows no redirect, so a callback goes only where its
// URL was checked to point.
func callbackClient() *http.Client {
	c := httpclient.NewSecureClient(callbackTimeout)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

func (r *JobRunner) info(msg string, kv ...any) {
	if r.logger != nil {
		r.logger.Info(msg, kv...)
	}
}

func (r *JobRunner) warn(msg string, kv ...any) {
	if r.logger != nil {
		r.logger.Warn(msg, kv...)
	}
}
