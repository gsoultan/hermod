package notification

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// captureTelegram stands in for the Telegram Bot API and records the one
// request the send path makes.
type captureTelegram struct {
	mu     sync.Mutex
	body   map[string]string
	path   string
	status int
	reply  string
	block  chan struct{}
}

func (c *captureTelegram) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.block != nil {
			<-c.block
		}
		raw, _ := io.ReadAll(r.Body)
		var parsed map[string]string
		_ = json.Unmarshal(raw, &parsed)

		c.mu.Lock()
		c.body = parsed
		c.path = r.URL.Path
		status, reply := c.status, c.reply
		c.mu.Unlock()

		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if reply != "" {
			_, _ = io.WriteString(w, reply)
		}
	}))
	t.Cleanup(srv.Close)

	prev := telegramAPIBase
	telegramAPIBase = srv.URL
	t.Cleanup(func() { telegramAPIBase = prev })
}

func (c *captureTelegram) sent() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body
}

func testSettings() NotificationSettings {
	return NotificationSettings{
		TelegramToken:  "123:ABC",
		TelegramChatID: "-1001",
	}
}

// A Go error string routinely carries Markdown's active characters — table and
// column names use underscores, and pq quotes identifiers. Sent with
// parse_mode=Markdown and nothing escaped, Telegram rejects the whole message
// with 400 "can't parse entities" and the alert is lost.
func TestSendTelegramEscapesActiveCharacters(t *testing.T) {
	tg := &captureTelegram{}
	tg.start(t)

	ns := testSettings()
	ns.BaseURL = "https://hermod.example.com"
	wf := storage.Workflow{ID: "wf_1", Name: "orders_sync *prod*"}
	msg := `Error: pq: relation "user_events" does not exist [see_docs]`

	if err := ns.SendTelegram(t.Context(), "Workflow Error", msg, wf); err != nil {
		t.Fatalf("SendTelegram: %v", err)
	}

	body := tg.sent()
	if body == nil {
		t.Fatal("no request reached the API")
	}
	if got := body["parse_mode"]; got != "HTML" {
		t.Errorf("parse_mode = %q, want HTML (Markdown cannot carry unescaped error text)", got)
	}

	text := body["text"]
	// Every literal < & > from the payload must arrive escaped, and no raw
	// Markdown emphasis may survive to be parsed as an entity.
	for _, raw := range []string{"user_events", "orders_sync", "see_docs"} {
		if !strings.Contains(text, raw) {
			t.Errorf("text lost %q; got %q", raw, text)
		}
	}
	// The point of leaving Markdown behind: in HTML mode * and _ are inert, so
	// they arrive literally instead of being parsed as an unbalanced entity.
	if !strings.Contains(text, "*prod*") {
		t.Errorf("HTML mode should pass Markdown metacharacters through literally: %q", text)
	}
}

func TestSendTelegramEscapesHTMLMetacharacters(t *testing.T) {
	tg := &captureTelegram{}
	tg.start(t)

	ns := testSettings()
	wf := storage.Workflow{ID: "wf-1", Name: "a<b>c"}

	if err := ns.SendTelegram(t.Context(), "Workflow Error", "x & y <script>", wf); err != nil {
		t.Fatalf("SendTelegram: %v", err)
	}

	text := tg.sent()["text"]
	if strings.Contains(text, "<script>") || strings.Contains(text, "a<b>c") {
		t.Errorf("raw HTML reached Telegram and will be rejected or interpreted: %q", text)
	}
	if !strings.Contains(text, "&lt;script&gt;") || !strings.Contains(text, "&amp;") {
		t.Errorf("metacharacters were not escaped: %q", text)
	}
}

func TestSendTelegramSurfacesAPIError(t *testing.T) {
	tg := &captureTelegram{
		status: http.StatusBadRequest,
		reply:  `{"ok":false,"description":"Bad Request: can't parse entities"}`,
	}
	tg.start(t)

	err := testSettings().SendTelegram(t.Context(), "T", "m", storage.Workflow{ID: "w"})
	if err == nil {
		t.Fatal("want an error for a 400 response")
	}
	if !strings.Contains(err.Error(), "parse entities") {
		t.Errorf("error lost the API description: %v", err)
	}
}

// The send used http.DefaultClient, which has no timeout, on a context with no
// deadline. A black-holed api.telegram.org then blocks whichever engine
// goroutine emitted the status change.
func TestNotificationHTTPClientHasTimeout(t *testing.T) {
	if httpClient.Timeout <= 0 {
		t.Fatal("notification http client has no timeout")
	}
	if httpClient == http.DefaultClient {
		t.Fatal("notification path must not use http.DefaultClient")
	}
}

type stubStorage struct {
	storage.Storage
	logs []storage.Log
	mu   sync.Mutex
}

func (s *stubStorage) GetSetting(ctx context.Context, key string) (string, error) { return "", nil }

func (s *stubStorage) CreateLog(ctx context.Context, l storage.Log) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, l)
	return nil
}

type failingProvider struct{ delay time.Duration }

func (p *failingProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return errors.New("provider exploded")
}
func (p *failingProvider) Type() string                 { return "failing" }
func (p *failingProvider) SetStorage(s storage.Storage) {}

type recordLogger struct {
	mu     sync.Mutex
	errors []string
}

func (l *recordLogger) Debug(msg string, kv ...any) {}
func (l *recordLogger) Info(msg string, kv ...any)  {}
func (l *recordLogger) Warn(msg string, kv ...any)  {}
func (l *recordLogger) Error(msg string, kv ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, msg)
}
func (l *recordLogger) seen() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.errors...)
}

// A provider failure went to fmt.Printf, so a broken Telegram token was
// invisible in the structured log and in the UI.
func TestNotifyReportsProviderFailureOnTheLogger(t *testing.T) {
	svc := NewService(&stubStorage{})
	lg := &recordLogger{}
	svc.SetLogger(lg)
	svc.AddProvider(&failingProvider{})

	svc.Notify(t.Context(), "Workflow Error", "boom", storage.Workflow{ID: "w1"})
	svc.Wait()

	if len(lg.seen()) == 0 {
		t.Fatal("a failing provider produced no logger output")
	}
}

// Notify runs on the engine's status-change callback. A slow channel must not
// hold that goroutine.
func TestNotifyDoesNotBlockTheCaller(t *testing.T) {
	svc := NewService(&stubStorage{})
	svc.SetLogger(&recordLogger{})
	svc.AddProvider(&failingProvider{delay: 2 * time.Second})

	done := make(chan struct{})
	go func() {
		svc.Notify(context.Background(), "Workflow Error", "boom", storage.Workflow{ID: "w1"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Notify blocked on a slow provider")
	}
}

// The send must outlive the caller's context: the registry passes the engine's
// context, which is cancelled the moment the workflow stops — which is exactly
// when the "stopped" alert is raised.
func TestNotifySurvivesCallerCancellation(t *testing.T) {
	tg := &captureTelegram{}
	tg.start(t)

	svc := NewService(&stubStorage{})
	svc.SetLogger(&recordLogger{})
	p := &recordingProvider{}
	svc.AddProvider(p)

	ctx, cancel := context.WithCancel(context.Background())
	svc.Notify(ctx, "Workflow Stopped", "m", storage.Workflow{ID: "w1"})
	cancel()
	svc.Wait()

	if p.count() == 0 {
		t.Fatal("cancelling the caller's context dropped the notification")
	}
}

type recordingProvider struct {
	mu sync.Mutex
	n  int
}

func (p *recordingProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	return nil
}
func (p *recordingProvider) Type() string                 { return "recording" }
func (p *recordingProvider) SetStorage(s storage.Storage) {}
func (p *recordingProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

func TestNotifyStillDedupesWithinTheWindow(t *testing.T) {
	svc := NewService(&stubStorage{})
	svc.SetLogger(&recordLogger{})
	p := &recordingProvider{}
	svc.AddProvider(p)

	for range 5 {
		svc.Notify(context.Background(), "Workflow Error", "m", storage.Workflow{ID: "w1"})
	}
	svc.Wait()

	if got := p.count(); got != 1 {
		t.Errorf("sends = %d, want 1 inside the dedupe window", got)
	}
}

// An operator stopping a workflow, or a worker draining on a deploy, is normal
// operation. UINotificationProvider hardcoded Level "ERROR" for everything it
// wrote, so the lifecycle alerts would have filed routine events as errors in
// the log table and in the UI's error view.
func TestLifecycleAlertsAreNotLoggedAsErrors(t *testing.T) {
	st := &stubStorage{}
	svc := NewService(st)
	svc.SetLogger(&recordLogger{})
	svc.AddProvider(NewUINotificationProvider(st))

	svc.NotifyLevel(context.Background(), LevelInfo, "Workflow Stopped", "m", storage.Workflow{ID: "w1"})
	svc.Wait()

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.logs) != 1 {
		t.Fatalf("wrote %d log rows, want 1", len(st.logs))
	}
	if st.logs[0].Level != "INFO" {
		t.Errorf("level = %q, want INFO: a routine stop is not an error", st.logs[0].Level)
	}
}

// A real fault still has to be an error.
func TestFaultAlertsStayErrors(t *testing.T) {
	st := &stubStorage{}
	svc := NewService(st)
	svc.SetLogger(&recordLogger{})
	svc.AddProvider(NewUINotificationProvider(st))

	svc.Notify(context.Background(), "Workflow Error", "m", storage.Workflow{ID: "w1"})
	svc.Wait()

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.logs) != 1 || st.logs[0].Level != "ERROR" {
		t.Fatalf("logs = %+v, want one ERROR row", st.logs)
	}
}
