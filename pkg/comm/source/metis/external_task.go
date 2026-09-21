package metis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sdk "github.com/gsoultan/metis-sdk"
)

// ExternalTaskConfig is everything the external-task source needs.
//
// It has no ProjectID: a topic is subscribed to across the organization, which
// is what lets one Hermod workflow serve the same step in several processes.
type ExternalTaskConfig struct {
	// BaseURL is the engine's origin, e.g. https://bpm.example.com. Plaintext
	// http is refused unless the host is loopback, because the token travels in
	// a header.
	BaseURL string

	// Token authenticates every call. Supply this when it comes from a secret
	// store; supply Username and Password instead to have the source log in and
	// hold the token itself.
	Token string

	// Username and Password log in on first use, and again when the token
	// expires — which a static token cannot do.
	Username string
	Password string

	// OrganizationID selects which organization the calls act in. Only needed
	// for an account belonging to more than one.
	OrganizationID string

	// Topic is the service task's topic, as the BPMN diagram sets it. Required:
	// it is the queue this source subscribes to.
	Topic string

	// WorkerID is the identity that holds the locks. The engine authorises a
	// completion by this id alone, so two pipelines sharing one can finish each
	// other's tasks. Left empty, a distinct id is generated per source, which
	// is the safe default; set it only to something genuinely unique.
	WorkerID string

	// MaxTasks is how many tasks one fetch may lock. Defaults to 5. Every task
	// in a batch is locked when the batch is fetched, so its lock is running
	// while the ones ahead of it are still in the pipeline: a batch larger than
	// the pipeline can clear inside LockDuration ends in expired locks.
	MaxTasks int

	// LockDuration is how long a fetched task stays ours. Defaults to 1 minute.
	// It is the budget for everything the pipeline does with the task, and Ack
	// refuses to complete past it.
	LockDuration time.Duration

	// PollInterval is the pause after a fetch that returned nothing, or failed.
	// Defaults to 2 seconds. A fetch that returned work is followed immediately
	// by the next one: pacing a busy topic would cap it at MaxTasks per
	// interval however fast the pipeline runs, and a drained batch is evidence
	// there is more waiting.
	PollInterval time.Duration

	// VariableFields selects which keys of the acknowledged message become the
	// step's output variables. Empty sends them all, which also sends back
	// whatever the step was given. Name the outputs to keep a variable the
	// process should not learn — a card number read for the charge — out of the
	// instance.
	VariableFields []string

	// Timeout bounds a single call. Zero uses the SDK's 30s default.
	Timeout time.Duration
}

// ExternalTaskSource runs a BPMN service task as a Hermod pipeline.
//
// A service task marked with a topic is published by the engine as work rather
// than called out to. This source locks that work, hands each task to the
// pipeline as a message whose data is the step's variables, and — this is the
// part that makes it an integration rather than a feed — completes the task in
// Ack, sending back whatever the pipeline made of it. The process moves on to
// its next step with the pipeline's output in its variables.
//
// # Ack is the completion, and there is no Nack
//
// Hermod acknowledges a message once every sink write has succeeded, and does
// nothing at all when one fails. That maps exactly onto the external-task
// contract: an acknowledged task is completed, and a task the pipeline could
// not finish is simply never completed, so its lock lapses and the engine hands
// it to the next worker. Redelivery is the engine's, not this source's, which
// is why nothing here is persisted.
//
// The cost of that mapping is the one every lease-based queue has: work may be
// done twice. A task whose pipeline succeeded but whose completion did not
// reach the engine is redelivered, so the sinks downstream of this source want
// to be idempotent.
//
// # The lock is the budget
//
// Ack refuses to complete a task whose lock has already lapsed. Past that
// instant the task may be held by somebody else, and completing it would let
// two workers finish one step — the failure the lock exists to prevent. The
// engine refuses it too, inside the transaction; the check here is what makes
// the reason legible in the pipeline's own log instead of arriving as a remote
// error string.
//
// A pipeline that regularly loses tasks this way is asking for too many at
// once: lower MaxTasks, or raise LockDuration past what the slowest sink needs.
type ExternalTaskSource struct {
	cfg    ExternalTaskConfig
	client *sdk.Client

	authMu sync.Mutex
	authed bool

	mu       sync.Mutex
	logger   hermod.Logger
	pending  []hermod.Message
	lastPoll time.Time
	// lastFetchEmpty is what decides whether the next fetch is paced. An error
	// counts as empty: an unreachable engine should be backed off from, not
	// retried in a loop.
	lastFetchEmpty bool
}

// ExternalTaskSource is deliberately not hermod.Stateful: see the type's doc.
var _ hermod.Source = (*ExternalTaskSource)(nil)

// NewExternalTask builds the source. Configuration mistakes are refused here
// rather than on the first fetch, so a source that cannot work never reports
// itself healthy.
func NewExternalTask(cfg ExternalTaskConfig) (*ExternalTaskSource, error) {
	if cfg.MaxTasks <= 0 {
		cfg.MaxTasks = 5
	}
	if cfg.LockDuration <= 0 {
		cfg.LockDuration = time.Minute
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if strings.TrimSpace(cfg.WorkerID) == "" {
		id, err := generateWorkerID()
		if err != nil {
			return nil, fmt.Errorf("metis external-task source: %w", err)
		}
		cfg.WorkerID = id
	}
	if err := validateExternalTask(cfg); err != nil {
		return nil, err
	}

	opts := []sdk.Option{}
	if cfg.Token != "" {
		opts = append(opts, sdk.WithToken(cfg.Token))
	}
	if cfg.Timeout > 0 {
		opts = append(opts, sdk.WithHTTPClient(&http.Client{Timeout: cfg.Timeout}))
	}

	client := sdk.NewClient(cfg.BaseURL, opts...)
	if cfg.OrganizationID != "" {
		client.SetOrganization(cfg.OrganizationID)
	}

	return &ExternalTaskSource{cfg: cfg, client: client}, nil
}

func validateExternalTask(cfg ExternalTaskConfig) error {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("metis external-task source: an engine base url is required")
	}
	if err := refusePlaintextOffLoopback(cfg.BaseURL); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Token) == "" && strings.TrimSpace(cfg.Username) == "" {
		return errors.New("metis external-task source: a token, or a username and password, is required")
	}
	if strings.TrimSpace(cfg.Topic) == "" {
		return errors.New("metis external-task source: a topic is required; it is the service task's " +
			"topic attribute on the diagram, and it is what this source subscribes to")
	}
	return nil
}

// generateWorkerID names this source in a way no other source will repeat.
//
// The hostname makes a lock legible to whoever is looking at the engine; the
// random half is what actually makes it unique, because two replicas of the
// same deployment share a hostname and a worker id that collides is a worker
// completing another's tasks.
func generateWorkerID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate a worker id: %w", err)
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "hermod"
	}
	return host + "-" + hex.EncodeToString(buf[:]), nil
}

// WorkerID is the identity this source holds its locks under.
func (s *ExternalTaskSource) WorkerID() string { return s.cfg.WorkerID }

// SetLogger installs the pipeline's logger.
func (s *ExternalTaskSource) SetLogger(logger hermod.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

func (s *ExternalTaskSource) log(level, msg string, kv ...any) {
	s.mu.Lock()
	logger := s.logger
	s.mu.Unlock()
	if logger == nil {
		return
	}
	if level == "error" {
		logger.Error(msg, kv...)
		return
	}
	logger.Warn(msg, kv...)
}

// Read returns the next locked task, fetching more when it has none buffered.
//
// It blocks until a task appears or the context is done, and the context is
// what bounds it: a topic nobody is producing work for never returns on its
// own.
func (s *ExternalTaskSource) Read(ctx context.Context) (hermod.Message, error) {
	for {
		if msg := s.next(); msg != nil {
			return msg, nil
		}
		if err := s.waitForNextFetch(ctx); err != nil {
			return nil, err
		}
		if err := s.fetch(ctx); err != nil {
			return nil, err
		}
	}
}

// next pops the oldest buffered task.
func (s *ExternalTaskSource) next() hermod.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	msg := s.pending[0]
	s.pending = s.pending[1:]
	return msg
}

// waitForNextFetch sleeps out the rest of the poll interval, but only after a
// fetch that found nothing.
//
// The first fetch is immediate: a pipeline that has just started should not
// wait an interval to discover the engine is unreachable. So is the one after a
// batch that had work, because that batch is evidence there is more waiting and
// pacing a busy topic caps it at MaxTasks per interval.
func (s *ExternalTaskSource) waitForNextFetch(ctx context.Context) error {
	s.mu.Lock()
	last := s.lastPoll
	idle := s.lastFetchEmpty
	s.mu.Unlock()

	if last.IsZero() || !idle {
		return ctx.Err()
	}
	wait := time.Until(last.Add(s.cfg.PollInterval))
	if wait <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// fetch locks a batch of tasks and buffers them as messages.
func (s *ExternalTaskSource) fetch(ctx context.Context) error {
	s.mu.Lock()
	s.lastPoll = time.Now()
	// Pessimistic until proven otherwise: an empty answer and a failed call
	// both mean the next fetch should be paced.
	s.lastFetchEmpty = true
	s.mu.Unlock()

	if err := s.ensureExternalAuth(ctx, false); err != nil {
		return fmt.Errorf("metis external-task source: %w", err)
	}

	tasks, err := s.client.FetchAndLock(ctx, s.cfg.Topic, s.cfg.WorkerID, s.cfg.MaxTasks, s.cfg.LockDuration)
	if err != nil {
		if sdk.IsUnauthorized(err) && s.hasExternalCredentials() {
			if aerr := s.ensureExternalAuth(ctx, true); aerr != nil {
				return fmt.Errorf("metis external-task source: %w", aerr)
			}
			tasks, err = s.client.FetchAndLock(ctx, s.cfg.Topic, s.cfg.WorkerID, s.cfg.MaxTasks, s.cfg.LockDuration)
		}
		if err != nil {
			return fmt.Errorf("metis external-task source: fetch and lock %q: %w", s.cfg.Topic, err)
		}
	}
	if len(tasks) == 0 {
		return nil
	}

	msgs := make([]hermod.Message, 0, len(tasks))
	for _, t := range tasks {
		msgs = append(msgs, s.message(t))
	}

	s.mu.Lock()
	s.pending = append(s.pending, msgs...)
	s.lastFetchEmpty = false
	s.mu.Unlock()
	return nil
}

// message turns a locked task into the pipeline's message.
//
// The step's variables are the data, unaltered. Everything about the task
// itself rides in metadata, so a process variable called `id` or `topic` is not
// shadowed by the envelope carrying it.
func (s *ExternalTaskSource) message(t sdk.ExternalTask) hermod.Message {
	msg := message.AcquireMessage()
	msg.SetID(t.ID)
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable(t.Topic)
	msg.SetMetadata("source", "metis")
	msg.SetMetadata("metis_stream", "external_tasks")
	msg.SetMetadata("metis_task_id", t.ID)
	msg.SetMetadata("metis_topic", t.Topic)
	msg.SetMetadata("metis_worker_id", s.cfg.WorkerID)
	msg.SetMetadata("metis_instance_id", t.InstanceID())
	msg.SetMetadata("metis_node_id", t.NodeID())
	msg.SetMetadata("metis_retries", strconv.Itoa(t.Retries))
	if t.LockExpiration != nil {
		msg.SetMetadata("metis_lock_expiration", t.LockExpiration.Format(time.RFC3339Nano))
	}

	data := map[string]any(t.Variables)
	for k, v := range data {
		msg.SetData(k, v)
	}
	if encoded, err := json.Marshal(message.SanitizeMap(data)); err == nil {
		msg.SetPayload(encoded)
	}
	return msg
}

// Ack completes the task, sending the pipeline's output back as the step's
// variables. The process resumes at the next node with them in scope.
//
// It refuses to complete a task whose lock has lapsed; see the type's doc for
// why that is a refusal and not a best effort.
//
// # An acknowledgement is not always a success
//
// Hermod acknowledges a message it could not deliver but did preserve: a node
// that failed, or a sink that refused, parks the message in the dead-letter
// sink and then acknowledges, so the source stops replaying something already
// kept. For a queue or a replication slot that is exactly right.
//
// Here it would be a lie. Completing the task tells the process the step
// succeeded, and it advances to its next node — approving the payment, shipping
// the order — on work that is in fact sitting in a dead-letter queue. So a
// message carrying either marker fails the task instead, with the reason the
// pipeline recorded, and the process stops where it is with an incident an
// operator can see.
func (s *ExternalTaskSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	taskID, err := s.taskIdentity(msg)
	if err != nil {
		return err
	}

	if reason, undelivered := preservedNotDelivered(msg); undelivered {
		if err := s.refuseExpiredLock(msg, taskID, "fail"); err != nil {
			return err
		}
		// No retries. Parking the message means Hermod has taken custody of
		// it: the copy in the dead-letter sink is there to be replayed. Asking
		// the engine for another attempt as well would put the same work in
		// two places, and replaying the parked copy after a redelivered task
		// already ran is the double execution this whole model exists to
		// prevent. One incident, one copy, one operator decision.
		if ferr := s.fail(ctx, taskID, reason, 0); ferr != nil {
			return fmt.Errorf("metis external-task source: fail task %s: %w", taskID, ferr)
		}
		return nil
	}

	if err := s.refuseExpiredLock(msg, taskID, "complete"); err != nil {
		return err
	}

	vars := s.outputVariables(msg)
	if err := s.completeWithRetry(ctx, taskID, vars); err != nil {
		return fmt.Errorf("metis external-task source: complete task %s: %w", taskID, err)
	}
	return nil
}

// preservedNotDelivered reports whether the engine kept this message somewhere
// other than the pipeline's destination, and why.
//
// The markers are the engine's, set on the message before it acknowledges.
// `_hermod_failed_at` is the one that matters: the engine stamps it on every
// park, from either route into the dead-letter sink. `_hermod_dead_lettered` is
// set *on top of it* when a node failed, and reading only that one misses the
// case this exists for — a sink outage, where the message is parked and
// acknowledged carrying no other sign that it never arrived.
// `_hermod_validation_failed` is the third route, which sets neither of the
// other two.
//
// A workflow writing to several sinks, where one delivered and another was
// parked, is read here as not delivered. That is the conservative answer and
// the intended one: the step did not do all of what it was configured to do,
// and an operator looking at the incident is a better outcome than a process
// advancing on a half-finished step.
func preservedNotDelivered(msg hermod.Message) (string, bool) {
	meta := msg.Metadata()
	if meta["_hermod_failed_at"] == "" &&
		meta["_hermod_dead_lettered"] != "true" &&
		meta["_hermod_validation_failed"] != "true" {
		return "", false
	}
	reason := strings.TrimSpace(meta["_hermod_last_error"])
	if reason == "" {
		reason = "the pipeline could not deliver this message and parked it in the dead-letter sink"
	}
	return reason, true
}

// fail reports a task failed, logging in again when the token has expired. A
// 401 is a stated refusal — the engine did nothing — so repeating the call
// cannot spend two retries.
func (s *ExternalTaskSource) fail(ctx context.Context, taskID, reason string, retries int) error {
	err := s.client.FailExternalTask(ctx, taskID, s.cfg.WorkerID, reason, "", retries, 0)
	if err != nil && sdk.IsUnauthorized(err) && s.hasExternalCredentials() {
		if aerr := s.ensureExternalAuth(ctx, true); aerr != nil {
			return aerr
		}
		return s.client.FailExternalTask(ctx, taskID, s.cfg.WorkerID, reason, "", retries, 0)
	}
	return err
}

// taskIdentity reads which task a message is, refusing one that no longer says.
//
// Every message this source emits carries the id. One that arrives without it
// has had its metadata stripped somewhere in the pipeline, and there is nothing
// to complete — going quiet would leave the task to expire and be redone
// forever, with nothing in the log saying why.
func (s *ExternalTaskSource) taskIdentity(msg hermod.Message) (string, error) {
	taskID := strings.TrimSpace(msg.Metadata()["metis_task_id"])
	if taskID == "" {
		return "", fmt.Errorf("metis external-task source: message %q carries no metis_task_id, so there is "+
			"no task to act on; a transformation dropped the metadata this source set", msg.ID())
	}
	return taskID, nil
}

// refuseExpiredLock stops an act on a task this source may no longer hold.
func (s *ExternalTaskSource) refuseExpiredLock(msg hermod.Message, taskID, act string) error {
	raw := msg.Metadata()["metis_lock_expiration"]
	if raw == "" {
		// The engine sent no expiry, so there is nothing to compare against.
		// It enforces the lock itself either way.
		return nil
	}
	expiry, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		s.log("warn", "metis external-task source: unparseable lock expiry; leaving the check to the engine",
			"task_id", taskID, "value", raw, "error", err)
		return nil
	}
	if !time.Now().After(expiry) {
		return nil
	}
	return fmt.Errorf("metis external-task source: refusing to %s task %s — its lock expired at %s, so the "+
		"engine may have given it to another worker; the pipeline took longer than lock_duration, so raise "+
		"it or lower max_tasks", act, taskID, expiry.Format(time.RFC3339))
}

// outputVariables is what the process gets back.
func (s *ExternalTaskSource) outputVariables(msg hermod.Message) sdk.Variables {
	data := msg.Data()
	if len(s.cfg.VariableFields) == 0 {
		out := make(sdk.Variables, len(data))
		for k, v := range data {
			out[k] = v
		}
		return out
	}
	out := make(sdk.Variables, len(s.cfg.VariableFields))
	for _, field := range s.cfg.VariableFields {
		if v, ok := data[field]; ok {
			out[field] = v
		}
	}
	return out
}

// completeWithRetry completes the task, logging in again first when the token
// has expired. A 401 is a stated refusal — the engine did nothing — so
// repeating the call cannot complete the task twice.
func (s *ExternalTaskSource) completeWithRetry(ctx context.Context, taskID string, vars sdk.Variables) error {
	err := s.client.CompleteExternalTask(ctx, taskID, s.cfg.WorkerID, vars)
	if err != nil && sdk.IsUnauthorized(err) && s.hasExternalCredentials() {
		if aerr := s.ensureExternalAuth(ctx, true); aerr != nil {
			return aerr
		}
		return s.client.CompleteExternalTask(ctx, taskID, s.cfg.WorkerID, vars)
	}
	return err
}

// Ping lists the organization's projects — a read, so a health check never
// locks work this source is not ready to run.
func (s *ExternalTaskSource) Ping(ctx context.Context) error {
	if err := s.ensureExternalAuth(ctx, false); err != nil {
		return fmt.Errorf("metis external-task source: %w", err)
	}
	if _, err := s.client.ListProjects(ctx); err != nil {
		if sdk.IsUnauthorized(err) && s.hasExternalCredentials() {
			if aerr := s.ensureExternalAuth(ctx, true); aerr != nil {
				return fmt.Errorf("metis external-task source: %w", aerr)
			}
			if _, err = s.client.ListProjects(ctx); err == nil {
				return nil
			}
		}
		return fmt.Errorf("metis external-task source: cannot reach the engine: %w", err)
	}
	return nil
}

// Close releases nothing at the engine.
//
// There is no call that hands a locked task back, so tasks fetched but never
// acknowledged stay locked until they expire and the engine redelivers them.
// That is the same outcome as the process being killed, which is the case this
// model is built for.
func (s *ExternalTaskSource) Close() error { return nil }

func (s *ExternalTaskSource) hasExternalCredentials() bool {
	return strings.TrimSpace(s.cfg.Username) != ""
}

// ensureExternalAuth logs in when the source holds a password rather than a
// token. force re-logs in even when a token is already held, which is how an
// expired one is replaced.
func (s *ExternalTaskSource) ensureExternalAuth(ctx context.Context, force bool) error {
	if !s.hasExternalCredentials() {
		return nil
	}

	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.authed && !force {
		return nil
	}
	if err := s.client.Login(ctx, s.cfg.Username, s.cfg.Password); err != nil {
		return fmt.Errorf("log in to the engine: %w", err)
	}
	s.authed = true
	return nil
}
