// Package metis drives a BPMN 2.0 workflow engine from a Hermod pipeline.
//
// Each message becomes one act against a Metis server: it starts a process
// instance, correlates a message into an instance already waiting for one, or
// broadcasts a signal to every instance listening. The row that changed becomes
// the process variables, so a committed database transaction is what starts the
// business process that answers it.
//
// # Retries and duplicate process instances
//
// Starting a process is not idempotent and the engine has no de-duplication
// key, so a retry after a timeout may start a second instance of somebody's
// order-fulfilment process. Hermod's RetrySink retries every error the same way,
// so this sink makes the distinction itself, using the idempotency claim as the
// lever — the same design the panmail sink uses, for the same reason:
//
//   - The engine *stated* a refusal (400, 401, 403, 404). It answered, and its
//     answer was that it did nothing, so the claim is released and a corrected
//     retry is free to take it.
//   - The outcome is *unknown* (a transport failure, or a 5xx). The claim is
//     kept, so the retry that follows finds the key taken and does nothing
//     instead of starting a second instance.
//
// The 5xx case is where this sink differs from a sink whose refusals are all
// stated. A 500 is an answer, but it is not a statement that nothing happened:
// the engine may have written the instance and then faltered on the way to
// telling us. Treating it as a refusal is what would start the process twice.
//
// With idempotency off there is nothing to hold the claim, and a retry after an
// unknown outcome may start a duplicate. The error says so rather than looking
// routine.
package metis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gsoultan/hermod"
	sdk "github.com/gsoultan/metis-sdk"
)

// Action is what a message does to the engine.
type Action string

const (
	// ActionStartProcess starts a new instance of a deployed definition. This
	// is the usual one: a row changed, so a process begins.
	ActionStartProcess Action = "start_process"
	// ActionSendMessage correlates a message into whichever instance is waiting
	// on that name, selected by the correlation key. Use it to answer a process
	// that is already running and blocked on an outside event.
	ActionSendMessage Action = "send_message"
	// ActionBroadcastSignal delivers a signal to every instance in the project
	// waiting on it. Unlike a message, a signal has no single addressee.
	ActionBroadcastSignal Action = "broadcast_signal"
)

// Config is everything the sink needs. The string fields marked as templated are
// Go templates over the message — see renderData for what is in scope.
type Config struct {
	// BaseURL is the engine's origin, e.g. https://bpm.example.com. The /api/v1
	// prefix is the client's business. Plaintext http is refused unless the host
	// is loopback, because the token travels in a header.
	BaseURL string

	// Token authenticates every call. Supply this when it comes from a secret
	// store; supply Username and Password instead to have the sink log in and
	// hold the token itself.
	Token string

	// Username and Password log in on first use. Preferred over a static token
	// for a long-running pipeline: the sink logs in again when the token
	// expires, which a static token cannot do.
	Username string
	Password string

	// OrganizationID selects which organization the calls act in. Only needed
	// for an account belonging to more than one.
	OrganizationID string

	// ProjectID is the Metis project every call is scoped to. Required: the
	// engine refuses an empty one rather than widening the request.
	ProjectID string

	// Action is what each message does. Defaults to ActionStartProcess.
	Action Action

	// DefinitionKey is the process ID from the BPMN diagram — the key, not the
	// definition's ID, so a redeploy takes effect without reconfiguring this.
	// Required for ActionStartProcess. Templated.
	DefinitionKey string

	// MessageName is the BPMN message this correlates. Required for
	// ActionSendMessage. Templated.
	MessageName string

	// CorrelationKey selects which waiting instance the message reaches, when
	// several wait on the same name. Templated; empty matches every waiting
	// subscription for the name, which is rarely what a pipeline wants.
	CorrelationKey string

	// SignalName is the BPMN signal this broadcasts. Required for
	// ActionBroadcastSignal. Templated.
	SignalName string

	// VariableFields selects which keys become process variables. Empty sends
	// them all.
	VariableFields []string

	// Timeout bounds a single call. Zero uses the SDK's 30s default.
	Timeout time.Duration
}

// IdempotencyStore guards against starting the same process twice. It is the
// same shape the SMTP and panmail sinks declare, so one store serves all three.
type IdempotencyStore interface {
	// Claim returns true if this call created the record (we own it), false if
	// it already exists.
	Claim(ctx context.Context, key string) (bool, error)
	// MarkSent records a successful completion for the key.
	MarkSent(ctx context.Context, key string) error
	// Release gives up a claim whose work did not complete, so a retry can take
	// it again. Releasing a key already marked sent must do nothing, and
	// releasing one never claimed must not be an error.
	Release(ctx context.Context, key string) error
}

// Sink drives a Metis engine from a Hermod pipeline.
type Sink struct {
	cfg       Config
	client    *sdk.Client
	formatter hermod.Formatter

	idemStore         IdempotencyStore
	enableIdempotency bool
	idemKeyTemplate   string

	authMu sync.Mutex
	authed bool

	mu                sync.Mutex
	lastInstanceID    string
	lastWriteDedup    bool
	lastWriteConflict bool
}

var (
	_ hermod.Sink                = (*Sink)(nil)
	_ hermod.BatchSink           = (*Sink)(nil)
	_ hermod.IdempotencyReporter = (*Sink)(nil)
)

// New builds the sink. Configuration mistakes are refused here rather than on
// the first message, so a sink that cannot work never reports itself healthy.
func New(cfg Config, formatter hermod.Formatter) (*Sink, error) {
	if cfg.Action == "" {
		cfg.Action = ActionStartProcess
	}
	if err := validate(cfg); err != nil {
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

	return &Sink{cfg: cfg, client: client, formatter: formatter}, nil
}

func validate(cfg Config) error {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("metis sink: an engine base url is required")
	}
	if err := refusePlaintextOffLoopback(cfg.BaseURL); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Token) == "" && strings.TrimSpace(cfg.Username) == "" {
		return errors.New("metis sink: a token, or a username and password, is required; " +
			"the engine records the acting user from the token and has no way to accept an override")
	}
	if strings.TrimSpace(cfg.ProjectID) == "" {
		return errors.New("metis sink: a project id is required; the engine refuses an empty one " +
			"rather than widening the call to every project in the organization")
	}

	switch cfg.Action {
	case ActionStartProcess:
		if strings.TrimSpace(cfg.DefinitionKey) == "" {
			return errors.New("metis sink: a definition key is required to start a process; " +
				"it is the process id from the diagram, not the definition's id")
		}
	case ActionSendMessage:
		if strings.TrimSpace(cfg.MessageName) == "" {
			return errors.New("metis sink: a message name is required to correlate a message")
		}
	case ActionBroadcastSignal:
		if strings.TrimSpace(cfg.SignalName) == "" {
			return errors.New("metis sink: a signal name is required to broadcast a signal")
		}
	default:
		return fmt.Errorf("metis sink: unknown action %q; it must be one of %s, %s or %s",
			cfg.Action, ActionStartProcess, ActionSendMessage, ActionBroadcastSignal)
	}
	return nil
}

// refusePlaintextOffLoopback keeps the bearer token off the wire in the clear.
// Loopback is allowed so a developer running an engine locally is not forced
// into a certificate.
func refusePlaintextOffLoopback(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("metis sink: base url is not a url: %w", err)
	}
	if parsed.Scheme != "http" {
		return nil
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("metis sink: refusing plaintext http to %q — the token travels in an "+
		"Authorization header, so use https (http is allowed to loopback only)", host)
}

// EnableIdempotency turns the duplicate guard on. It needs a store to be of any
// use; without one the sink cannot suppress a repeat.
func (s *Sink) EnableIdempotency(on bool) { s.enableIdempotency = on }

// SetIdempotencyStore injects the store implementation.
func (s *Sink) SetIdempotencyStore(store IdempotencyStore) { s.idemStore = store }

// SetIdempotencyKeyTemplate overrides the derived key with a template over the
// message — an order id, say, so two events about one order start one process.
func (s *Sink) SetIdempotencyKeyTemplate(tmpl string) {
	s.idemKeyTemplate = strings.TrimSpace(tmpl)
}

// LastInstanceID is the id the engine gave the most recently started instance.
// It is empty for the message and signal actions, which address instances that
// already exist and return nothing to identify them.
func (s *Sink) LastInstanceID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastInstanceID
}

// LastWriteIdempotent implements hermod.IdempotencyReporter.
func (s *Sink) LastWriteIdempotent() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastWriteDedup, s.lastWriteConflict
}

// Write drives one message into the engine.
//
// A nil error means the engine accepted the act, not that the process it began
// will succeed: a started instance can still fail at its first step. Read
// [sdk.Client.ListIncidents], or the metis source's incidents stream, for that.
func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}

	s.mu.Lock()
	s.lastWriteDedup = false
	s.lastWriteConflict = false
	s.mu.Unlock()

	act, err := s.compose(msg)
	if err != nil {
		return err
	}

	if !s.enableIdempotency || s.idemStore == nil {
		if err := s.dispatch(ctx, act); err != nil {
			return s.unguardedError(err)
		}
		return nil
	}

	key, err := s.idempotencyKey(msg, act)
	if err != nil {
		return err
	}
	return s.writeGuarded(ctx, key, act)
}

// writeGuarded dispatches under an idempotency claim, and decides what the claim
// means afterwards. That decision is the whole reason this sink exists rather
// than a bare SDK call — see the package documentation.
func (s *Sink) writeGuarded(ctx context.Context, key string, act action) error {
	claimed, err := s.idemStore.Claim(ctx, key)
	if err != nil {
		return fmt.Errorf("metis sink: claim idempotency key: %w", err)
	}
	if !claimed {
		// Already handled — either done, or held by the claim of a call whose
		// outcome nobody knows. Both mean: do not do this again.
		s.mu.Lock()
		s.lastWriteDedup = true
		s.mu.Unlock()
		return nil
	}

	dispatchErr := s.dispatch(ctx, act)
	if dispatchErr != nil {
		if !stated(dispatchErr) {
			// The outcome is unknown. Keeping the claim is what stops the retry
			// that follows from starting a second instance; the cost is a
			// process that may never have started staying unstarted.
			return fmt.Errorf("metis sink: the %s call did not complete and its outcome is "+
				"not known, so the idempotency claim on %q is being kept to stop a retry "+
				"acting twice; release it to re-drive: %w", s.cfg.Action, key, dispatchErr)
		}
		// The engine said plainly it did not act, so a retry cannot duplicate.
		if rerr := s.idemStore.Release(ctx, key); rerr != nil {
			return fmt.Errorf("metis sink: the engine refused the call (%w) and the "+
				"idempotency claim could not be released (%v), so a retry of this message "+
				"will be suppressed", dispatchErr, rerr)
		}
		return fmt.Errorf("metis sink: %w", dispatchErr)
	}

	if err := s.idemStore.MarkSent(ctx, key); err != nil {
		return fmt.Errorf("metis sink: mark sent: %w", err)
	}
	return nil
}

// WriteBatch drives each message in turn.
//
// There is no batch endpoint: one message is one act on one instance, and
// grouping here would only hide which one failed.
func (s *Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if err := s.Write(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

// Ping lists the organization's projects.
//
// It is deliberately a read: the engine's write calls all *do* something to
// somebody's process, and a health check that starts a process is not a health
// check. A successful ping says the engine is reachable and the credentials are
// good — more than a bare dial can tell you.
func (s *Sink) Ping(ctx context.Context) error {
	if err := s.ensureAuth(ctx, false); err != nil {
		return fmt.Errorf("metis sink: %w", err)
	}
	if _, err := s.client.ListProjects(ctx); err != nil {
		if sdk.IsUnauthorized(err) && s.hasCredentials() {
			if aerr := s.ensureAuth(ctx, true); aerr != nil {
				return fmt.Errorf("metis sink: %w", aerr)
			}
			if _, err = s.client.ListProjects(ctx); err == nil {
				return nil
			}
		}
		return fmt.Errorf("metis sink: cannot reach the engine: %w", err)
	}
	return nil
}

// Close releases nothing: the SDK client holds only an http.Client, whose idle
// connections the runtime reclaims.
func (s *Sink) Close() error { return nil }

// action is one resolved call, with every template already rendered.
type action struct {
	kind           Action
	definitionKey  string
	messageName    string
	signalName     string
	correlationKey string
	variables      sdk.Variables
}

// target is the name this act addresses — the definition, message or signal.
// It is what the idempotency key and the error messages identify it by.
func (a action) target() string {
	switch a.kind {
	case ActionSendMessage:
		return a.messageName
	case ActionBroadcastSignal:
		return a.signalName
	default:
		return a.definitionKey
	}
}

// dispatch performs the call, logging in first when it has credentials.
//
// An expired token answers 401, which is a stated refusal: the engine did
// nothing, so logging in again and repeating the call cannot act twice. That is
// the only error retried here, and only when there is a credential to retry
// with — a static token has no second thing to try.
func (s *Sink) dispatch(ctx context.Context, act action) error {
	if err := s.ensureAuth(ctx, false); err != nil {
		return err
	}

	err := s.call(ctx, act)
	if err != nil && sdk.IsUnauthorized(err) && s.hasCredentials() {
		if aerr := s.ensureAuth(ctx, true); aerr != nil {
			return aerr
		}
		return s.call(ctx, act)
	}
	return err
}

func (s *Sink) call(ctx context.Context, act action) error {
	switch act.kind {
	case ActionSendMessage:
		return s.client.SendMessage(ctx, s.cfg.ProjectID, act.messageName, act.correlationKey, act.variables)
	case ActionBroadcastSignal:
		return s.client.BroadcastSignal(ctx, s.cfg.ProjectID, act.signalName, act.variables)
	default:
		instanceID, err := s.client.StartProcess(ctx, s.cfg.ProjectID, act.definitionKey, act.variables)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.lastInstanceID = instanceID
		s.mu.Unlock()
		return nil
	}
}

func (s *Sink) hasCredentials() bool { return strings.TrimSpace(s.cfg.Username) != "" }

// ensureAuth logs in when the sink holds a password rather than a token. force
// re-logs in even when a token is already held, which is how an expired one is
// replaced.
func (s *Sink) ensureAuth(ctx context.Context, force bool) error {
	if !s.hasCredentials() {
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

// compose renders the configured templates against the message.
func (s *Sink) compose(msg hermod.Message) (action, error) {
	data := renderData(msg)

	var err error
	field := func(name, tmpl string) string {
		if err != nil {
			return ""
		}
		var rendered string
		rendered, err = s.render(name, tmpl, data)
		return rendered
	}

	act := action{
		kind:           s.cfg.Action,
		definitionKey:  field("definition key", s.cfg.DefinitionKey),
		messageName:    field("message name", s.cfg.MessageName),
		signalName:     field("signal name", s.cfg.SignalName),
		correlationKey: field("correlation key", s.cfg.CorrelationKey),
	}
	if err != nil {
		return action{}, err
	}

	// A template that renders to nothing leaves the engine to guess, and for the
	// definition key there is nothing to guess from. Catch it here rather than
	// as a 400 with the message already consumed.
	if act.target() == "" {
		return action{}, fmt.Errorf("metis sink: the %s rendered empty for message %q",
			targetName(s.cfg.Action), msg.ID())
	}

	act.variables = s.variables(data)
	return act, nil
}

func targetName(a Action) string {
	switch a {
	case ActionSendMessage:
		return "message name"
	case ActionBroadcastSignal:
		return "signal name"
	default:
		return "definition key"
	}
}

// variables is what the instance carries. VariableFields narrows it; empty sends
// everything in scope.
func (s *Sink) variables(data map[string]any) sdk.Variables {
	if len(s.cfg.VariableFields) == 0 {
		return sdk.Variables(data)
	}
	out := make(sdk.Variables, len(s.cfg.VariableFields))
	for _, key := range s.cfg.VariableFields {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if v, ok := data[key]; ok {
			out[key] = v
		}
	}
	return out
}

// renderData is what the templates and the process variables see: the message's
// own fields, with its data map copied over the top so a column named `table`
// wins for the workflow that configured it.
//
// The envelope fields go in first for exactly that reason — writing them last
// would overwrite a CDC row's own `table` or `id` column with the envelope's.
func renderData(msg hermod.Message) map[string]any {
	data := map[string]any{
		"id":        msg.ID(),
		"operation": string(msg.Operation()),
		"table":     msg.Table(),
		"schema":    msg.Schema(),
		"metadata":  msg.Metadata(),
	}
	maps.Copy(data, msg.Data())
	return data
}

func (s *Sink) render(field, tmpl string, data map[string]any) (string, error) {
	if tmpl == "" || !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}
	parsed, err := template.New("metis").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("metis sink: %s template is not valid: %w", field, err)
	}
	var buf bytes.Buffer
	if err := parsed.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("metis sink: rendering the %s template failed: %w", field, err)
	}
	return buf.String(), nil
}

// idempotencyKey is the identity of the act, not of the message: two messages
// that resolve to the same process, started with the same variables, are the
// same act, and doing both is the duplicate this guards against.
func (s *Sink) idempotencyKey(msg hermod.Message, act action) (string, error) {
	if s.idemKeyTemplate != "" {
		rendered, err := s.render("idempotency key", s.idemKeyTemplate, renderData(msg))
		if err != nil {
			return "", err
		}
		// "<no value>" is what text/template writes for a field the message did
		// not have. Treating it as a key would collapse every such message into
		// one, and suppress all but the first.
		if key := strings.TrimSpace(rendered); key != "" && key != "<no value>" {
			return key, nil
		}
	}

	h := fnv.New128a()
	write := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	write(string(act.kind))
	write(s.cfg.ProjectID)
	write(act.target())
	write(act.correlationKey)
	write(msg.ID())

	// The variables are part of the act: the same row at two different values is
	// two different processes to start. Marshalled with sorted keys, which
	// encoding/json does for a map, so the digest is stable across runs.
	if encoded, err := json.Marshal(act.variables); err == nil {
		write(string(encoded))
	}
	return fmt.Sprintf("metis:%x", h.Sum(nil)), nil
}

// stated reports whether the engine told us it did not act.
//
// 400, 401, 403 and 404 are the engine's own refusals: the request was rejected
// before anything was written, so repeating it cannot act twice. Everything else
// leaves the outcome unknown — a transport failure never reached an answer, and
// a 5xx is an answer that says the engine broke, not that it broke *before*
// committing the instance.
func stated(err error) bool {
	if err == nil {
		return false
	}
	// 5xx is absent from this list on purpose, and adding it is the mutation
	// TestWrite_ServerErrorKeepsTheClaim exists to catch.
	return sdk.IsInvalid(err) || sdk.IsUnauthorized(err) || sdk.IsNotFound(err)
}

// unguardedError is what a failed call reports when there is no claim to hold. A
// stated refusal is reported as-is; an unknown outcome has to say that the retry
// which follows may act a second time, because nothing here can stop it.
func (s *Sink) unguardedError(err error) error {
	if stated(err) {
		return fmt.Errorf("metis sink: %w", err)
	}
	return fmt.Errorf("metis sink: the %s call did not complete and its outcome is not "+
		"known; the retry that follows may act on the engine a second time. Turn on "+
		"idempotency to suppress that: %w", s.cfg.Action, err)
}

// Actions lists the actions a sink can be configured with, for a UI that offers
// them rather than hardcoding the strings.
func Actions() []Action {
	return slices.Clone([]Action{ActionStartProcess, ActionSendMessage, ActionBroadcastSignal})
}
