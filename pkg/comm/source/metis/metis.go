// Package metis reads a BPMN 2.0 workflow engine into a Hermod pipeline.
//
// It polls a Metis project for one of three things — process instances, human
// tasks, or incidents — and hands each new row to the pipeline as a message. The
// point is to get process history somewhere it can be queried: how long
// instances take, which steps produce incidents, whose inbox is backing up.
//
// # The cursor advances on Ack, not on Read
//
// The engine's listings are ordered newest first and have no "since" filter, so
// the source keeps the watermark itself: the created_at of the last row the
// pipeline *acknowledged*, plus the ids of any rows sharing that exact instant.
//
// Only Ack moves it. Reading a row means it was fetched, not that it arrived
// anywhere, and a worker that dies with messages in flight must come back for
// them. Advancing on read is how a position gets ahead of what was delivered,
// and the row that fell in the gap is never handed out again — the defect this
// repository has fixed in ten other sources. Reading does move a second,
// in-process position, which is what stops a running poll re-reading the rows it
// just handed out; that one is deliberately not persisted.
//
// # Incidents cost a request per failed instance
//
// The engine lists incidents per instance, not per project, so the incidents
// stream lists instances first and asks only the failed ones for their
// incidents. That bounds the cost to the number of failed instances rather than
// the number of instances — but it also bounds what can be seen, because an
// instance that fails after it has aged out of ScanPages of the instance listing
// is never asked. Raise ScanPages when instances are numerous and failures are
// slow to appear.
package metis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sdk "github.com/gsoultan/metis-sdk"
)

// Stream is which of the engine's listings the source reads.
type Stream string

const (
	// StreamInstances reads process instances — one row per run of a process.
	StreamInstances Stream = "instances"
	// StreamTasks reads human tasks, every status, not just open ones.
	StreamTasks Stream = "tasks"
	// StreamIncidents reads the failures an operator has to resolve. See the
	// package documentation for what this costs and what it cannot see.
	StreamIncidents Stream = "incidents"
)

// Streams lists the streams a source can be configured with, for a UI that
// offers them rather than hardcoding the strings.
func Streams() []Stream {
	return slices.Clone([]Stream{StreamInstances, StreamTasks, StreamIncidents})
}

// Config is everything the source needs.
type Config struct {
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

	// ProjectID is the Metis project to read. Required.
	ProjectID string

	// Stream is which listing to read. Defaults to StreamInstances.
	Stream Stream

	// PollInterval is how long to wait between listings. Defaults to 10s.
	PollInterval time.Duration

	// PageSize is how many rows to ask for per listing. Zero uses the server's
	// default. A page smaller than the number of rows that appear between two
	// polls loses the oldest of them, so size it above the expected rate.
	PageSize int

	// ScanPages bounds how many pages of instances the incidents stream walks
	// looking for failures. Defaults to 1. Ignored by the other streams.
	ScanPages int

	// Timeout bounds a single call. Zero uses the SDK's 30s default.
	Timeout time.Duration
}

// Source polls a Metis engine and emits its rows as messages.
type Source struct {
	cfg    Config
	client *sdk.Client

	authMu sync.Mutex
	authed bool

	mu      sync.Mutex
	logger  hermod.Logger
	pending []hermod.Message

	// seen is how far reading has got. It keeps a running poll from handing out
	// a row twice inside one process, and is deliberately not persisted.
	seen cursor
	// acked is the position a restart resumes from — the last row the pipeline
	// said it had taken. This is the only one GetState reports.
	acked cursor

	lastPoll time.Time
}

var (
	_ hermod.Source   = (*Source)(nil)
	_ hermod.Stateful = (*Source)(nil)
)

// New builds the source. Configuration mistakes are refused here rather than on
// the first poll, so a source that cannot work never reports itself healthy.
func New(cfg Config) (*Source, error) {
	if cfg.Stream == "" {
		cfg.Stream = StreamInstances
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.ScanPages <= 0 {
		cfg.ScanPages = 1
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

	return &Source{cfg: cfg, client: client}, nil
}

func validate(cfg Config) error {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("metis source: an engine base url is required")
	}
	if err := refusePlaintextOffLoopback(cfg.BaseURL); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Token) == "" && strings.TrimSpace(cfg.Username) == "" {
		return errors.New("metis source: a token, or a username and password, is required")
	}
	if strings.TrimSpace(cfg.ProjectID) == "" {
		return errors.New("metis source: a project id is required; the engine refuses an empty " +
			"one rather than listing every project in the organization")
	}
	switch cfg.Stream {
	case StreamInstances, StreamTasks, StreamIncidents:
	default:
		return fmt.Errorf("metis source: unknown stream %q; it must be one of %s, %s or %s",
			cfg.Stream, StreamInstances, StreamTasks, StreamIncidents)
	}
	return nil
}

// refusePlaintextOffLoopback keeps the bearer token off the wire in the clear.
func refusePlaintextOffLoopback(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("metis source: base url is not a url: %w", err)
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
	return fmt.Errorf("metis source: refusing plaintext http to %q — the token travels in an "+
		"Authorization header, so use https (http is allowed to loopback only)", host)
}

// SetLogger installs the pipeline's logger.
func (s *Source) SetLogger(logger hermod.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

func (s *Source) log(level, msg string, kv ...any) {
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

// Read returns the next row, polling the engine when it has none buffered.
//
// It blocks until a row appears or the context is done, and the context is what
// bounds it: an engine with nothing to report never returns on its own.
func (s *Source) Read(ctx context.Context) (hermod.Message, error) {
	for {
		if msg := s.next(); msg != nil {
			return msg, nil
		}
		if err := s.waitForNextPoll(ctx); err != nil {
			return nil, err
		}
		if err := s.poll(ctx); err != nil {
			return nil, err
		}
	}
}

// next pops the oldest buffered row.
func (s *Source) next() hermod.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	msg := s.pending[0]
	s.pending = s.pending[1:]
	return msg
}

// waitForNextPoll sleeps out the rest of the poll interval. The first poll is
// immediate: a pipeline that has just started should not wait an interval to
// discover the engine is unreachable.
func (s *Source) waitForNextPoll(ctx context.Context) error {
	s.mu.Lock()
	last := s.lastPoll
	s.mu.Unlock()

	if last.IsZero() {
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

// poll lists the configured stream and buffers whatever has not been handed out
// yet, oldest first.
func (s *Source) poll(ctx context.Context) error {
	s.mu.Lock()
	s.lastPoll = time.Now()
	s.mu.Unlock()

	if err := s.ensureAuth(ctx, false); err != nil {
		return err
	}

	rows, err := s.list(ctx)
	if err != nil && sdk.IsUnauthorized(err) && s.hasCredentials() {
		// The token expired. Logging in again and listing again is free: a
		// listing changes nothing.
		if aerr := s.ensureAuth(ctx, true); aerr != nil {
			return aerr
		}
		rows, err = s.list(ctx)
	}
	if err != nil {
		return fmt.Errorf("metis source: list %s: %w", s.cfg.Stream, err)
	}

	// The engine answers newest first; a pipeline wants the order things
	// happened in, and the cursor only makes sense walking forwards.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].createdAt.Before(rows[j].createdAt) })

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range rows {
		if s.seen.passed(row.createdAt, row.id) {
			continue
		}
		s.seen.advance(row.createdAt, row.id)
		s.pending = append(s.pending, s.message(row))
	}
	return nil
}

// row is one item of any of the three streams, reduced to what the cursor and
// the message need.
type row struct {
	id        string
	createdAt time.Time
	data      map[string]any
}

func (s *Source) list(ctx context.Context) ([]row, error) {
	switch s.cfg.Stream {
	case StreamTasks:
		return s.listTasks(ctx)
	case StreamIncidents:
		return s.listIncidents(ctx)
	default:
		return s.listInstances(ctx)
	}
}

func (s *Source) listInstances(ctx context.Context) ([]row, error) {
	instances, _, err := s.client.ListInstances(ctx, sdk.ListInstancesOptions{
		ProjectID: s.cfg.ProjectID,
		PageSize:  s.cfg.PageSize,
	})
	if err != nil {
		return nil, err
	}

	rows := make([]row, 0, len(instances))
	for _, inst := range instances {
		rows = append(rows, row{id: inst.ID, createdAt: inst.CreatedAt, data: instanceData(inst)})
	}
	return rows, nil
}

func instanceData(inst sdk.Instance) map[string]any {
	data := map[string]any{
		"id":         inst.ID,
		"status":     string(inst.Status),
		"finished":   inst.IsFinished(),
		"created_at": formatTime(inst.CreatedAt),
	}
	if inst.Definition != nil {
		data["definition_id"] = inst.Definition.ID
		data["definition_key"] = inst.Definition.Key
		data["definition_name"] = inst.Definition.Name
	}
	if inst.Variables != nil {
		data["variables"] = map[string]any(inst.Variables)
	}
	return data
}

func (s *Source) listTasks(ctx context.Context) ([]row, error) {
	tasks, _, err := s.client.ListTasks(ctx, sdk.ListTasksOptions{
		ProjectID: s.cfg.ProjectID,
		PageSize:  s.cfg.PageSize,
	})
	if err != nil {
		return nil, err
	}

	rows := make([]row, 0, len(tasks))
	for _, task := range tasks {
		data := map[string]any{
			"id":          task.ID,
			"name":        task.Name,
			"description": task.Description,
			"type":        string(task.Type),
			"status":      string(task.Status),
			"open":        task.IsOpen(),
			"priority":    task.Priority,
			"form_key":    task.FormKey,
			"assignee":    task.AssigneeUsername(),
			"instance_id": task.InstanceID(),
			"node_id":     task.NodeID(),
			"created_at":  formatTime(task.CreatedAt),
		}
		if task.DueDate != nil {
			data["due_date"] = formatTime(*task.DueDate)
		}
		if task.Variables != nil {
			data["variables"] = map[string]any(task.Variables)
		}
		rows = append(rows, row{id: task.ID, createdAt: task.CreatedAt, data: data})
	}
	return rows, nil
}

// listIncidents walks the instance listing for failures and asks each one what
// went wrong. See the package documentation for the window this leaves.
func (s *Source) listIncidents(ctx context.Context) ([]row, error) {
	var rows []row

	for page := 1; page <= s.cfg.ScanPages; page++ {
		instances, info, err := s.client.ListInstances(ctx, sdk.ListInstancesOptions{
			ProjectID: s.cfg.ProjectID,
			Page:      page,
			PageSize:  s.cfg.PageSize,
		})
		if err != nil {
			return nil, err
		}

		for _, inst := range instances {
			if inst.Status != sdk.ProcessFailed {
				continue
			}
			incidents, err := s.client.ListIncidents(ctx, inst.ID)
			if err != nil {
				// One unreadable instance must not cost the whole poll: the
				// others still have incidents worth reporting, and the cursor
				// only advances past what is actually emitted.
				s.log("warn", "metis source: could not list incidents", "instance_id", inst.ID, "error", err)
				continue
			}
			for _, inc := range incidents {
				rows = append(rows, row{id: inc.ID, createdAt: inc.CreatedAt, data: incidentData(inc)})
			}
		}

		if info == nil || !info.HasMore {
			break
		}
	}
	return rows, nil
}

func incidentData(inc sdk.Incident) map[string]any {
	data := map[string]any{
		"id":         inc.ID,
		"error":      inc.Error,
		"status":     string(inc.Status),
		"open":       inc.IsOpen(),
		"node_id":    inc.NodeID(),
		"created_at": formatTime(inc.CreatedAt),
	}
	if inc.Instance != nil {
		data["instance_id"] = inc.Instance.ID
		data["instance_status"] = string(inc.Instance.Status)
	}
	if inc.Definition != nil {
		data["definition_key"] = inc.Definition.Key
	}
	if inc.ResolvedAt != nil {
		data["resolved_at"] = formatTime(*inc.ResolvedAt)
	}
	return data
}

// message builds the pipeline's message. The watermark rides in metadata rather
// than in the data map, so a stream whose own fields happen to be called
// `created_at` cannot move the cursor by accident.
func (s *Source) message(r row) hermod.Message {
	msg := message.AcquireMessage()
	msg.SetID(r.id)
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable(string(s.cfg.Stream))
	msg.SetMetadata("source", "metis")
	msg.SetMetadata("metis_stream", string(s.cfg.Stream))
	msg.SetMetadata("metis_project_id", s.cfg.ProjectID)
	msg.SetMetadata("metis_created_at", formatTime(r.createdAt))

	for k, v := range r.data {
		msg.SetData(k, v)
	}
	if encoded, err := json.Marshal(message.SanitizeMap(r.data)); err == nil {
		msg.SetPayload(encoded)
	}
	return msg
}

// Ack moves the position a restart resumes from.
//
// This is the only thing that moves it. See the package documentation for why
// Read does not.
func (s *Source) Ack(_ context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	stamp := msg.Metadata()["metis_created_at"]
	if stamp == "" {
		// Not one of ours, or stripped by a transformation. Either way there is
		// no watermark to move to, and guessing would move it to the wrong row.
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		s.log("warn", "metis source: ignoring an unparseable watermark", "value", stamp, "error", err)
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.acked.advance(at, msg.ID())
	return nil
}

// Ping lists the organization's projects — a read, so a health check never
// disturbs a running process.
func (s *Source) Ping(ctx context.Context) error {
	if err := s.ensureAuth(ctx, false); err != nil {
		return fmt.Errorf("metis source: %w", err)
	}
	if _, err := s.client.ListProjects(ctx); err != nil {
		if sdk.IsUnauthorized(err) && s.hasCredentials() {
			if aerr := s.ensureAuth(ctx, true); aerr != nil {
				return fmt.Errorf("metis source: %w", aerr)
			}
			if _, err = s.client.ListProjects(ctx); err == nil {
				return nil
			}
		}
		return fmt.Errorf("metis source: cannot reach the engine: %w", err)
	}
	return nil
}

// GetState reports the acknowledged position, and only that. The reading
// position is further ahead whenever messages are in flight, and persisting it
// is what loses them.
func (s *Source) GetState() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.acked.at.IsZero() {
		return map[string]string{}
	}
	return map[string]string{
		"last_at":  formatTime(s.acked.at),
		"last_ids": strings.Join(s.acked.tieIDs(), ","),
	}
}

// SetState resumes from a previously acknowledged position.
func (s *Source) SetState(state map[string]string) {
	stamp := state["last_at"]
	if stamp == "" {
		return
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		// Silently ignoring this would restart the stream from the beginning and
		// replay everything, which looks like a connector bug rather than a bad
		// checkpoint. Say which value could not be read.
		s.log("error", "metis source: stored position is not a timestamp, so the stream will "+
			"resume from the beginning and replay", "last_at", stamp, "error", err)
		return
	}

	ids := map[string]struct{}{}
	for _, id := range strings.Split(state["last_ids"], ",") {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids[trimmed] = struct{}{}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Both: the acknowledged position is where this source resumes, and reading
	// starts from there too. Rows after it were never delivered.
	s.acked = cursor{at: at, ids: ids}
	s.seen = cursor{at: at, ids: maps.Clone(ids)}
}

// Close releases nothing: the SDK client holds only an http.Client, whose idle
// connections the runtime reclaims.
func (s *Source) Close() error { return nil }

func (s *Source) hasCredentials() bool { return strings.TrimSpace(s.cfg.Username) != "" }

// ensureAuth logs in when the source holds a password rather than a token. force
// re-logs in even when a token is already held, which is how an expired one is
// replaced.
func (s *Source) ensureAuth(ctx context.Context, force bool) error {
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

// --- the cursor ------------------------------------------------------------------

// cursor is a position in a listing ordered by time: an instant, plus the ids of
// the rows sharing that exact instant that have already been passed.
//
// The ids are what stop a tie being dropped. Two instances created in the same
// millisecond are two rows at one timestamp, and a cursor that were only a
// timestamp would have to choose between re-delivering the first or losing the
// second.
type cursor struct {
	at  time.Time
	ids map[string]struct{}
}

// passed reports whether (at, id) has already been handed out.
func (c *cursor) passed(at time.Time, id string) bool {
	if c.at.IsZero() {
		return false
	}
	if at.After(c.at) {
		return false
	}
	if at.Before(c.at) {
		return true
	}
	_, ok := c.ids[id]
	return ok
}

// advance moves the cursor to (at, id).
//
// It never moves backwards. Acks arrive in whatever order a concurrent worker
// pool finishes, and a rewind would re-deliver everything between.
func (c *cursor) advance(at time.Time, id string) {
	if !c.at.IsZero() && at.Before(c.at) {
		return
	}
	if c.at.IsZero() || at.After(c.at) {
		c.at = at
		c.ids = map[string]struct{}{id: {}}
		return
	}
	if c.ids == nil {
		c.ids = map[string]struct{}{}
	}
	c.ids[id] = struct{}{}
}

// tieIDs is the ids at the cursor's instant, sorted so the persisted state is
// stable rather than reordering on every save.
func (c *cursor) tieIDs() []string {
	out := make([]string, 0, len(c.ids))
	for id := range c.ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// formatTime renders a watermark. A zero time renders empty rather than as year
// one, so "no position" stays distinguishable from "the beginning of time".
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
