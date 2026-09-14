// Package fcm sends messages to devices, topics and conditions through Firebase
// Cloud Messaging.
//
// # What a message becomes
//
// Every hermod message resolves to one FCM destination — a registration token,
// a topic or a condition, and FCM accepts exactly one of the three. The
// destination comes from the message's own fcm_token/fcm_topic/fcm_condition
// metadata when it has any, and otherwise from the sink's configured default,
// which is a Go template over the row. A token field that renders a
// comma-separated list fans out as a multicast.
//
// The body is a notification (title, body, image — all templated) and a data
// map, and a message may have either or both. Data-only messages are how an app
// that renders its own alert is fed; notification-only messages are how a
// person is told something. Android, APNs and Web Push each take their own
// block of options, because "high priority" means a different thing on each.
//
// # Delivery semantics
//
// FCM has no idempotency key and no de-duplication: sending the same message
// twice delivers it twice. That is why the default sink deliberately does not
// implement hermod.BatchSink. Hermod's RetrySink retries a whole batch when any
// message in it fails, so a batching sink turns one transient failure into a
// second notification for every device that had already received the first.
// Without WriteBatch the engine retries one message at a time, and only the
// message that failed is repeated.
//
// NewBatching opts into batching for workflows where throughput matters more
// than a duplicate notification. It uses SendEach, which is one HTTP call per
// message issued concurrently, so the win is latency rather than request count.
//
// # Retrying
//
// FCM distinguishes refusals that another attempt could satisfy — UNAVAILABLE,
// INTERNAL, the two rate limits — from those it could not. A token belonging to
// an app that was uninstalled is gone for good; a payload over the size limit
// will be over it again. Those are returned wrapped in ErrPermanent so a caller
// that can tell the difference dead-letters immediately instead of spending the
// retry budget to reach the same answer.
package fcm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/gsoultan/hermod"
	"google.golang.org/api/option"
)

// client is the part of messaging.Client this sink uses. It is an interface so
// topic management can be tested: the SDK hard-codes the instance-id endpoint,
// so unlike the send path it cannot be pointed at a local server.
type client interface {
	Send(ctx context.Context, m *messaging.Message) (string, error)
	SendDryRun(ctx context.Context, m *messaging.Message) (string, error)
	SendEach(ctx context.Context, ms []*messaging.Message) (*messaging.BatchResponse, error)
	SendEachDryRun(ctx context.Context, ms []*messaging.Message) (*messaging.BatchResponse, error)
}

// topicManager is the other half, used by the subscribe actions.
type topicManager interface {
	SubscribeToTopic(ctx context.Context, tokens []string, topic string) (*messaging.TopicManagementResponse, error)
	UnsubscribeFromTopic(ctx context.Context, tokens []string, topic string) (*messaging.TopicManagementResponse, error)
}

// Sink writes hermod messages to Firebase Cloud Messaging.
//
// It deliberately does not implement hermod.BatchSink; see the package comment.
type Sink struct {
	cfg       Config
	formatter hermod.Formatter
	projectID string

	// Compiled once in New so a broken template is a refusal at save time and
	// rendering a row costs no parsing.
	token, topic, condition tmpl
	title, body, imageURL   tmpl
	androidCollapse         tmpl
	androidTag              tmpl
	apnsCollapse            tmpl
	apnsThread              tmpl
	apnsBadge               tmpl
	webpushLink             tmpl
	data                    map[string]tmpl

	// destFields are the row columns the destination templates read. Under
	// DataFields they are withheld from the payload — see destinationFields.
	destFields []string

	// now is overridable so the absolute apns-expiration header is testable.
	now func() time.Time

	mu     sync.Mutex
	client client
}

// BatchingSink is a Sink that also implements hermod.BatchSink. Use it only
// where a duplicate notification after a partial batch failure is acceptable —
// the package comment explains why that is the trade.
type BatchingSink struct {
	*Sink
}

var (
	_ hermod.Sink      = (*Sink)(nil)
	_ hermod.Sink      = (*BatchingSink)(nil)
	_ hermod.BatchSink = (*BatchingSink)(nil)
)

// New builds a single-message FCM sink.
func New(cfg Config) (*Sink, error) {
	projectID, err := resolveProject(cfg)
	if err != nil {
		return nil, err
	}

	switch cfg.Action {
	case ActionSubscribe, ActionUnsubscribe:
		// These take both: the tokens name the devices to move and the topic
		// names where to move them. The one-destination rule below is about
		// sending, and does not apply.
		if strings.TrimSpace(cfg.Topic) == "" {
			return nil, fmt.Errorf("fcm sink: the %s action needs a topic to %s devices to", cfg.Action, cfg.Action)
		}
		if strings.TrimSpace(cfg.Token) == "" {
			return nil, fmt.Errorf("fcm sink: the %s action needs a device token field naming the devices to move", cfg.Action)
		}
	default:
		if n := countSet(cfg.Token, cfg.Topic, cfg.Condition); n > 1 {
			return nil, fmt.Errorf(
				"fcm sink: exactly one of the default token, topic and condition may be set, and %d are; "+
					"FCM accepts one destination per message and refuses one carrying more", n)
		}
	}

	s := &Sink{
		cfg:       cfg,
		formatter: cfg.Formatter,
		projectID: projectID,
		now:       time.Now,
		data:      make(map[string]tmpl, len(cfg.Data)),
	}

	if err := s.compileTemplates(cfg); err != nil {
		return nil, err
	}
	s.destFields = destinationFields(s.token, s.topic, s.condition)
	return s, nil
}

// destinationFields is the set of row columns the destination templates read.
//
// A registration token is a capability: whoever holds it can push to that
// device. Under DataFields every column becomes a data key, so a message
// addressed by {{.device_token}} used to carry that token back to the device it
// was addressed to — and a multicast, whose field holds every recipient's
// token, handed each device the whole list. A topic name is the same shape of
// mistake, one step less severe.
//
// Withholding them is a default, not a prohibition: an operator who wants the
// value in the payload names it under data_json and gets it.
func destinationFields(targets ...tmpl) []string {
	seen := map[string]bool{}
	for _, t := range targets {
		for _, f := range t.fields() {
			seen[f] = true
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// compileTemplates parses every templated field once, so a template that does
// not parse is refused when the sink is saved rather than when the first row
// arrives, and no message pays to re-parse it.
func (s *Sink) compileTemplates(cfg Config) error {
	fields := []struct {
		name string
		raw  string
		dst  *tmpl
	}{
		{"token", cfg.Token, &s.token},
		{"topic", cfg.Topic, &s.topic},
		{"condition", cfg.Condition, &s.condition},
		{"title", cfg.Title, &s.title},
		{"body", cfg.Body, &s.body},
		{"image_url", cfg.ImageURL, &s.imageURL},
		{"android_collapse_key", cfg.Android.CollapseKey, &s.androidCollapse},
		{"android_tag", cfg.Android.Tag, &s.androidTag},
		{"apns_collapse_id", cfg.APNS.CollapseID, &s.apnsCollapse},
		{"apns_thread_id", cfg.APNS.ThreadID, &s.apnsThread},
		{"apns_badge", cfg.APNS.Badge, &s.apnsBadge},
		{"webpush_link", cfg.Webpush.Link, &s.webpushLink},
	}
	for _, f := range fields {
		compiled, err := compile(f.name, f.raw)
		if err != nil {
			return err
		}
		*f.dst = compiled
	}
	for key, raw := range cfg.Data {
		compiled, err := compile("data."+key, raw)
		if err != nil {
			return err
		}
		s.data[key] = compiled
	}
	return nil
}

// NewBatching builds a sink that implements hermod.BatchSink, sending a batch
// with one concurrent call per message.
func NewBatching(cfg Config) (*BatchingSink, error) {
	s, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &BatchingSink{Sink: s}, nil
}

// resolveProject settles which Firebase project this sink pushes to, and
// refuses the configurations where that answer would be a guess.
//
// An empty CredentialsJSON used to fall through to Google Application Default
// Credentials without saying so, which on any machine with gcloud logged in
// meant notifications going to whatever project that account defaults to. It is
// now an explicit opt-in that has to name the project.
func resolveProject(cfg Config) (string, error) {
	creds := strings.TrimSpace(cfg.CredentialsJSON)

	if creds == "" {
		if !cfg.UseDefaultCredentials {
			return "", errors.New("fcm sink: no credentials: paste a Firebase service account key, " +
				"or set use_default_credentials and a project id to use the ambient Google credentials on purpose")
		}
		if cfg.ProjectID == "" {
			return "", errors.New("fcm sink: use_default_credentials needs an explicit project id; " +
				"the ambient credentials do not say which Firebase project to push to")
		}
		return cfg.ProjectID, nil
	}

	var parsed struct {
		Type      string `json:"type"`
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(creds), &parsed); err != nil {
		return "", fmt.Errorf("fcm sink: the credentials are not valid JSON: %w", err)
	}
	if cfg.ProjectID != "" {
		return cfg.ProjectID, nil
	}
	if parsed.ProjectID == "" {
		return "", errors.New("fcm sink: the credentials carry no project_id; " +
			"paste the whole service account key, or set the project id field")
	}
	return parsed.ProjectID, nil
}

func countSet(values ...string) int {
	n := 0
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	return n
}

// setClientForTest injects a client, which is how topic management is covered.
func (s *Sink) setClientForTest(c client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
}

func (s *Sink) ensureConnected(ctx context.Context) (client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		return s.client, nil
	}

	var opts []option.ClientOption
	if s.cfg.HTTPClient != nil {
		// A caller supplying its own transport is supplying its own auth with
		// it — the emulator and the tests both do. Adding a credentials option
		// on top would have the SDK mint tokens nothing is going to check.
		opts = append(opts, option.WithHTTPClient(s.cfg.HTTPClient))
	} else if creds := strings.TrimSpace(s.cfg.CredentialsJSON); creds != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(creds)))
	}
	if s.cfg.Endpoint != "" {
		opts = append(opts, option.WithEndpoint(s.cfg.Endpoint))
	}

	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: s.projectID}, opts...)
	if err != nil {
		return nil, fmt.Errorf("fcm sink: initialising the firebase app failed: %w", err)
	}
	c, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("fcm sink: creating the messaging client failed: %w", err)
	}

	s.client = c
	return c, nil
}

// withTimeout applies the configured per-call deadline, if there is one.
func (s *Sink) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.cfg.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.cfg.Timeout)
}

// Write sends one message.
func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}

	c, err := s.ensureConnected(ctx)
	if err != nil {
		return err
	}

	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	switch s.cfg.Action {
	case ActionSubscribe, ActionUnsubscribe:
		return s.manageTopic(ctx, c, msg)
	}

	msgs, err := s.build(msg)
	if err != nil {
		return err
	}

	if len(msgs) == 1 {
		return s.sendOne(ctx, c, msgs[0])
	}
	return s.sendEach(ctx, c, msgs)
}

func (s *Sink) sendOne(ctx context.Context, c client, m *messaging.Message) error {
	var err error
	if s.cfg.DryRun {
		_, err = c.SendDryRun(ctx, m)
	} else {
		_, err = c.Send(ctx, m)
	}
	if err != nil {
		return fmt.Errorf("fcm sink: send failed: %w", classify(err, m.Token))
	}
	return nil
}

// sendEach issues one call per message concurrently and reports what failed.
//
// The result is permanent only when nothing in the batch could ever succeed.
// A mixed outcome stays retryable, because the messages that failed on a
// transient refusal still deserve another attempt — at the cost, documented on
// the package, of re-delivering the ones that succeeded.
func (s *Sink) sendEach(ctx context.Context, c client, msgs []*messaging.Message) error {
	var resp *messaging.BatchResponse
	var err error
	if s.cfg.DryRun {
		resp, err = c.SendEachDryRun(ctx, msgs)
	} else {
		resp, err = c.SendEach(ctx, msgs)
	}
	if err != nil {
		// A transport-level failure here means no message was sent at all.
		return fmt.Errorf("fcm sink: batch send failed: %w", err)
	}
	if resp.FailureCount == 0 {
		return nil
	}

	failures := make([]error, 0, resp.FailureCount)
	allPermanent := true
	for i, r := range resp.Responses {
		if r.Success || r.Error == nil {
			continue
		}
		var token string
		if i < len(msgs) {
			token = msgs[i].Token
		}
		classified := classify(r.Error, token)
		if !errors.Is(classified, ErrPermanent) {
			allPermanent = false
		}
		failures = append(failures, classified)
	}

	joined := fmt.Errorf("fcm sink: %d of %d messages failed: %w",
		resp.FailureCount, len(msgs), errors.Join(failures...))
	if allPermanent {
		return permanent(joined)
	}
	return joined
}

// manageTopic moves the message's devices on or off a topic.
//
// It is what turns a device-registration table into an FCM audience: an insert
// subscribes, a delete unsubscribes, and the topic is then addressable without
// the pipeline holding a token list of its own.
func (s *Sink) manageTopic(ctx context.Context, c client, msg hermod.Message) error {
	mgr, ok := c.(topicManager)
	if !ok {
		return errors.New("fcm sink: this client cannot manage topic subscriptions")
	}

	data := renderData(msg)

	topic, err := s.topic.render(data)
	if err != nil {
		return err
	}
	if topic = strings.TrimSpace(topic); topic == "" {
		return permanentf("fcm sink: the topic template rendered nothing for message %s", msg.ID())
	}

	tokens, err := s.topicTokens(msg, data)
	if err != nil {
		return err
	}

	var resp *messaging.TopicManagementResponse
	if s.cfg.Action == ActionSubscribe {
		resp, err = mgr.SubscribeToTopic(ctx, tokens, topic)
	} else {
		resp, err = mgr.UnsubscribeFromTopic(ctx, tokens, topic)
	}
	if err != nil {
		return fmt.Errorf("fcm sink: %s to topic %q failed: %w", s.cfg.Action, topic, classify(err, ""))
	}
	if resp.FailureCount > 0 {
		// Per-token refusals here are about the token, not the call: an already
		// stale registration cannot be made to subscribe by trying again.
		reasons := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			if e.Index < len(tokens) {
				reasons = append(reasons, fmt.Sprintf("%s: %s", tokens[e.Index], e.Reason))
			}
		}
		return permanentf("fcm sink: %d of %d devices could not %s topic %q (%s)",
			resp.FailureCount, len(tokens), s.cfg.Action, topic, strings.Join(reasons, "; "))
	}
	return nil
}

// topicTokens is the device list a subscribe or unsubscribe moves. Message
// metadata overrides the configured field, the same way it does for a send.
func (s *Sink) topicTokens(msg hermod.Message, data map[string]any) ([]string, error) {
	rendered, err := s.token.render(data)
	if err != nil {
		return nil, err
	}
	tokens := splitList(rendered)
	if v := strings.TrimSpace(msg.Metadata()[metaToken]); v != "" {
		tokens = splitList(v)
	}
	if len(tokens) == 0 {
		return nil, permanentf("fcm sink: message %s names no device to %s", msg.ID(), s.cfg.Action)
	}
	if len(tokens) > maxTopicTokens {
		return nil, permanentf("fcm sink: message %s names %d devices; FCM moves at most %d per call",
			msg.ID(), len(tokens), maxTopicTokens)
	}
	return tokens, nil
}

// WriteBatch sends every message, one concurrent call each.
//
// Only BatchingSink has it: see the package comment for why the ordinary sink
// leaves batching to the engine's per-message retry.
func (s *BatchingSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	c, err := s.ensureConnected(ctx)
	if err != nil {
		return err
	}

	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	if s.cfg.Action == ActionSubscribe || s.cfg.Action == ActionUnsubscribe {
		// Topic management has no batch form that preserves per-message topics.
		for _, m := range msgs {
			if m == nil {
				continue
			}
			if err := s.manageTopic(ctx, c, m); err != nil {
				return err
			}
		}
		return nil
	}

	built, buildErrs := s.buildAll(msgs)

	var sendErr error
	for chunk := range chunks(built, maxMulticastTokens) {
		if err := s.sendEach(ctx, c, chunk); err != nil {
			sendErr = errors.Join(sendErr, err)
		}
	}

	switch {
	case sendErr == nil && len(buildErrs) == 0:
		return nil
	case sendErr == nil:
		return permanent(errors.Join(buildErrs...))
	case len(buildErrs) == 0:
		return sendErr
	}
	joined := errors.Join(sendErr, errors.Join(buildErrs...))
	if errors.Is(sendErr, ErrPermanent) {
		return permanent(joined)
	}
	return joined
}

// buildAll turns the batch into FCM messages, keeping the ones that build.
//
// A message that cannot be built will not build on a retry either, so holding
// the whole batch back for it would stall every message that can go. It is
// reported alongside the send result instead.
func (s *Sink) buildAll(msgs []hermod.Message) ([]*messaging.Message, []error) {
	built := make([]*messaging.Message, 0, len(msgs))
	var failures []error
	for _, m := range msgs {
		if m == nil {
			continue
		}
		one, err := s.build(m)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		built = append(built, one...)
	}
	return built, failures
}

// chunks yields slices of at most n elements.
func chunks[T any](items []T, n int) func(func([]T) bool) {
	return func(yield func([]T) bool) {
		for start := 0; start < len(items); start += n {
			end := min(start+n, len(items))
			if !yield(items[start:end]) {
				return
			}
		}
	}
}

// Ping checks that the credentials reach the configured project.
//
// It validates a message rather than sending one. The previous implementation
// issued a real Send with a made-up token: it cost send quota, and a token that
// happened to be live would have been notified by a connection test.
//
// A refusal about the *message* is a pass. It proves the request was
// authenticated, routed to the project and answered — which is the whole
// question. A refusal about the *credentials* is the failure being looked for.
func (s *Sink) Ping(ctx context.Context) error {
	c, err := s.ensureConnected(ctx)
	if err != nil {
		return err
	}

	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	probe := &messaging.Message{
		Topic: "hermod-connection-check",
		Data:  map[string]string{"hermod": "ping"},
	}
	if _, err := c.SendDryRun(ctx, probe); err != nil {
		if reachedFCM(err) {
			return nil
		}
		return fmt.Errorf("fcm sink: %w", err)
	}
	return nil
}

// Close releases the client. The Firebase SDK has nothing to shut down, but
// dropping the reference means a reconfigured sink builds a fresh one rather
// than reusing credentials that may since have been replaced.
func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = nil
	return nil
}
