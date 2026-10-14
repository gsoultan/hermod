package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gsoultan/gsmail"

	"github.com/gsoultan/hermod/internal/storage"
)

// panmailGateway stands in for panmail's SendEmail procedure and records every
// request.
type panmailGateway struct {
	mu   sync.Mutex
	sent []map[string]any
	keys []string
	url  string
}

func newPanmailGateway(t *testing.T) *panmailGateway {
	t.Helper()
	g := &panmailGateway{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panmail.v1.EmailService/SendEmail" {
			t.Errorf("posted to %s, want the SendEmail procedure", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(raw, &decoded)
		g.mu.Lock()
		g.sent = append(g.sent, decoded)
		g.keys = append(g.keys, r.Header.Get("X-API-Key"))
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messageId":"msg-1","status":"EMAIL_EVENT_TYPE_PENDING"}`))
	}))
	t.Cleanup(srv.Close)
	g.url = srv.URL
	return g
}

func (g *panmailGateway) requests() ([]map[string]any, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]map[string]any(nil), g.sent...), append([]string(nil), g.keys...)
}

// settingsStorage answers notification_settings with fixed settings.
type settingsStorage struct {
	storage.Storage
	settings NotificationSettings
}

func (s *settingsStorage) GetSetting(context.Context, string) (string, error) {
	raw, err := json.Marshal(s.settings)
	return string(raw), err
}

func panmailSettings(gatewayURL string) NotificationSettings {
	return NotificationSettings{
		BaseURL:            "https://hermod.example.com",
		PanmailURL:         gatewayURL,
		PanmailAPIKey:      "key-123",
		PanmailProviderID:  "prov-1",
		PanmailFrom:        "alerts@example.com",
		PanmailTo:          "ops@example.com, oncall@example.com",
		PanmailDeadLetters: true,
	}
}

// manualTimer captures the digest's flush so a test decides when the window
// closes.
type manualTimer struct {
	mu    sync.Mutex
	fires []func()
	waits []time.Duration
}

func (m *manualTimer) after(d time.Duration, f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.waits = append(m.waits, d)
	m.fires = append(m.fires, f)
}

func (m *manualTimer) scheduled() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.fires)
}

func (m *manualTimer) fire(t *testing.T, i int) {
	t.Helper()
	m.mu.Lock()
	if i >= len(m.fires) {
		m.mu.Unlock()
		t.Fatalf("no flush %d was scheduled", i)
	}
	f := m.fires[i]
	m.mu.Unlock()
	f()
}

func digestService(t *testing.T, settings NotificationSettings) (*Service, *manualTimer) {
	t.Helper()
	svc := NewService(&settingsStorage{settings: settings})
	timer := &manualTimer{}
	svc.deadLetters.after = timer.after
	return svc, timer
}

var ordersWorkflow = storage.Workflow{ID: "wf-1", Name: "Orders sync"}

func sample(i int) func() DeadLetterSample {
	return func() DeadLetterSample {
		return DeadLetterSample{
			MessageID: fmt.Sprintf("m-%d", i),
			NodeID:    "transform-1",
			SinkID:    "sink-orders",
			Error:     fmt.Sprintf("field total is not a number (row %d)", i),
			FailedAt:  time.Date(2026, 10, 14, 4, 0, i, 0, time.UTC),
			Payload:   fmt.Sprintf(`{"order":%d}`, i),
		}
	}
}

func field(mail map[string]any, key string) string {
	s, _ := mail[key].(string)
	return s
}

// A burst of failures is one mail per workflow per window, not one mail per
// message, and it names the workflow, the node, the sink, the error and the
// message, with a link back to the dead-letter queue.
func TestDeadLetterDigestBatchesABurstIntoOneMail(t *testing.T) {
	gw := newPanmailGateway(t)
	svc, timer := digestService(t, panmailSettings(gw.url))

	built := 0
	for i := 1; i <= 15; i++ {
		build := sample(i)
		svc.DeadLettered(ordersWorkflow, func() DeadLetterSample { built++; return build() })
	}

	if n := timer.scheduled(); n != 1 {
		t.Fatalf("scheduled %d flushes for one burst, want 1", n)
	}
	if timer.waits[0] != DeadLetterDigestWindow {
		t.Errorf("window = %s, want %s", timer.waits[0], DeadLetterDigestWindow)
	}
	if sent, _ := gw.requests(); len(sent) != 0 {
		t.Fatalf("sent %d mails before the window closed, want 0", len(sent))
	}
	if built != deadLetterSamples {
		t.Errorf("built %d samples, want %d: messages past the sample limit are only counted", built, deadLetterSamples)
	}

	timer.fire(t, 0)
	svc.Wait()

	sent, keys := gw.requests()
	if len(sent) != 1 {
		t.Fatalf("sent %d mails for a burst of 15, want 1", len(sent))
	}
	mail := sent[0]
	if keys[0] != "key-123" {
		t.Errorf("X-API-Key = %q, want key-123", keys[0])
	}
	if got := field(mail, "providerId"); got != "prov-1" {
		t.Errorf("providerId = %q, want prov-1", got)
	}
	if to := fmt.Sprint(mail["to"]); !strings.Contains(to, "ops@example.com") || !strings.Contains(to, "oncall@example.com") {
		t.Errorf("to = %s, want both recipients", to)
	}
	subject := field(mail, "subject")
	if !strings.Contains(subject, "15") || !strings.Contains(subject, "Orders sync") {
		t.Errorf("subject = %q, want the count and the workflow", subject)
	}
	text := field(mail, "bodyText")
	for _, want := range []string{
		"Orders sync", "wf-1", "transform-1", "sink-orders", "m-1",
		"field total is not a number (row 1)", `{"order":1}`,
		"https://hermod.example.com/dead-letters?workflow_id=wf-1",
		"5 more",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text body lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "m-11") {
		t.Error("text body lists a message past the sample limit")
	}
	if html := field(mail, "bodyHtml"); !strings.Contains(html, "/dead-letters?workflow_id=wf-1") {
		t.Errorf("html body lacks the link back to the queue:\n%s", html)
	}
	if field(mail, "idempotencyKey") == "" {
		t.Error("digest carries no idempotency key, so a retried send could mail twice")
	}
}

// The window starts again after a flush: the next failure is a new digest.
func TestDeadLetterDigestStartsANewWindowAfterAFlush(t *testing.T) {
	gw := newPanmailGateway(t)
	svc, timer := digestService(t, panmailSettings(gw.url))

	svc.DeadLettered(ordersWorkflow, sample(1))
	timer.fire(t, 0)
	svc.Wait() // the sends run concurrently; keep the two in order
	svc.DeadLettered(ordersWorkflow, sample(2))
	if n := timer.scheduled(); n != 2 {
		t.Fatalf("scheduled %d flushes, want a second one for the failure after the first digest", n)
	}
	timer.fire(t, 1)
	svc.Wait()

	sent, _ := gw.requests()
	if len(sent) != 2 {
		t.Fatalf("sent %d mails, want 2", len(sent))
	}
	if text := field(sent[1], "bodyText"); strings.Contains(text, "(row 1)") || !strings.Contains(text, "(row 2)") {
		t.Errorf("second digest should carry only m-2:\n%s", field(sent[1], "bodyText"))
	}
}

// Each workflow has its own digest.
func TestDeadLetterDigestIsPerWorkflow(t *testing.T) {
	gw := newPanmailGateway(t)
	svc, timer := digestService(t, panmailSettings(gw.url))

	svc.DeadLettered(ordersWorkflow, sample(1))
	svc.DeadLettered(storage.Workflow{ID: "wf-2", Name: "Invoices"}, sample(2))
	if n := timer.scheduled(); n != 2 {
		t.Fatalf("scheduled %d flushes for two workflows, want 2", n)
	}
	timer.fire(t, 0)
	timer.fire(t, 1)
	svc.Wait()
	if sent, _ := gw.requests(); len(sent) != 2 {
		t.Fatalf("sent %d mails, want one per workflow", len(sent))
	}
}

// Nothing is mailed unless the operator turned dead-letter mail on.
func TestDeadLetterDigestIsOffUnlessEnabled(t *testing.T) {
	gw := newPanmailGateway(t)
	settings := panmailSettings(gw.url)
	settings.PanmailDeadLetters = false
	svc, timer := digestService(t, settings)

	svc.DeadLettered(ordersWorkflow, sample(1))
	timer.fire(t, 0)
	svc.Wait()
	if sent, _ := gw.requests(); len(sent) != 0 {
		t.Fatalf("sent %d mails with dead-letter mail off, want 0", len(sent))
	}
}

// A payload is whatever the source sent, so it is escaped in the HTML body.
func TestDeadLetterDigestEscapesThePayload(t *testing.T) {
	gw := newPanmailGateway(t)
	svc, timer := digestService(t, panmailSettings(gw.url))

	svc.DeadLettered(storage.Workflow{ID: "wf-1", Name: "<b>Orders</b>"}, func() DeadLetterSample {
		return DeadLetterSample{MessageID: "m-1", Error: "<img src=x>", Payload: `<script>alert(1)</script>`}
	})
	timer.fire(t, 0)
	svc.Wait()

	sent, _ := gw.requests()
	if len(sent) != 1 {
		t.Fatalf("sent %d mails, want 1", len(sent))
	}
	html := field(sent[0], "bodyHtml")
	for _, raw := range []string{"<script>", "<img src=x>", "<b>Orders</b>"} {
		if strings.Contains(html, raw) {
			t.Errorf("html body carries %q unescaped", raw)
		}
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("html body lacks the escaped payload:\n%s", html)
	}
}

// An operator who does not want message contents in mail gets the workflow,
// node and error without them.
func TestDeadLetterDigestCanLeaveThePayloadOut(t *testing.T) {
	gw := newPanmailGateway(t)
	settings := panmailSettings(gw.url)
	settings.PanmailHidePayload = true
	svc, timer := digestService(t, settings)

	svc.DeadLettered(ordersWorkflow, sample(1))
	timer.fire(t, 0)
	svc.Wait()

	sent, _ := gw.requests()
	if len(sent) != 1 {
		t.Fatalf("sent %d mails, want 1", len(sent))
	}
	for _, body := range []string{field(sent[0], "bodyText"), field(sent[0], "bodyHtml")} {
		if strings.Contains(body, `{"order"`) {
			t.Errorf("body carries the payload with payloads hidden:\n%s", body)
		}
		if !strings.Contains(body, "field total is not a number") {
			t.Errorf("body lost the error along with the payload:\n%s", body)
		}
	}
}

// Shutdown sends what is pending instead of dropping it with the process.
func TestFlushDeadLettersSendsWhatIsPending(t *testing.T) {
	gw := newPanmailGateway(t)
	svc, timer := digestService(t, panmailSettings(gw.url))

	svc.DeadLettered(ordersWorkflow, sample(1))
	svc.FlushDeadLetters()
	svc.Wait()
	if sent, _ := gw.requests(); len(sent) != 1 {
		t.Fatalf("sent %d mails on flush, want 1", len(sent))
	}

	// The window's own timer firing afterwards finds nothing to send.
	timer.fire(t, 0)
	svc.Wait()
	if sent, _ := gw.requests(); len(sent) != 1 {
		t.Fatalf("sent %d mails, want the flushed digest only", len(sent))
	}
}

// The gateway and its key are usually secrets. They are resolved when the
// mail is sent, so the stored settings never hold the key itself.
func TestPanmailResolvesReferences(t *testing.T) {
	gw := newPanmailGateway(t)
	t.Setenv("HERMOD_ENV_PANMAIL_KEY", "key-123")
	settings := panmailSettings(gw.url)
	settings.PanmailAPIKey = "{{env:PANMAIL_KEY}}"
	svc, timer := digestService(t, settings)

	svc.DeadLettered(ordersWorkflow, sample(1))
	timer.fire(t, 0)
	svc.Wait()

	_, keys := gw.requests()
	if len(keys) != 1 || keys[0] != "key-123" {
		t.Fatalf("X-API-Key = %v, want the resolved key-123", keys)
	}
}

// A reference that does not resolve fails the send with the reference named,
// not the literal token sent as a key.
func TestPanmailRefusesAnUnresolvedReference(t *testing.T) {
	gw := newPanmailGateway(t)
	settings := panmailSettings(gw.url)
	settings.PanmailAPIKey = "{{env:NOT_SET_ANYWHERE}}"

	err := settings.SendPanmail(t.Context(), nil, PanmailMail{Subject: "s", Text: "t"})
	if err == nil {
		t.Fatal("an unresolved api key was sent")
	}
	if !strings.Contains(err.Error(), "NOT_SET_ANYWHERE") {
		t.Errorf("error %q does not name the reference", err)
	}
	if sent, _ := gw.requests(); len(sent) != 0 {
		t.Fatalf("sent %d mails, want 0", len(sent))
	}
}

// Panmail is also an ordinary alert channel, and Settings → Test reaches it.
func TestPanmailIsATestedChannel(t *testing.T) {
	gw := newPanmailGateway(t)
	results := panmailSettings(gw.url).Test(t.Context())
	for _, r := range results {
		if r.Channel == "panmail" {
			if r.Status != "ok" {
				t.Fatalf("panmail test = %+v, want ok", r)
			}
			if sent, _ := gw.requests(); len(sent) != 1 {
				t.Fatalf("test sent %d mails, want 1", len(sent))
			}
			return
		}
	}
	t.Fatalf("no panmail result in %+v", results)
}

// A large payload is cut, and the mail says so.
func TestMessagePayloadIsCapped(t *testing.T) {
	big := `{"blob":"` + strings.Repeat("x", 3*deadLetterPayloadCap) + `"}`
	got, truncated := payloadText([]byte(big))
	if !truncated || len(got) > deadLetterPayloadCap {
		t.Errorf("payload of %d bytes came back as %d bytes, truncated=%v; want at most %d and truncated",
			len(big), len(got), truncated, deadLetterPayloadCap)
	}
	got, truncated = payloadText([]byte(`{"a":1}`))
	if truncated || got != "{\n  \"a\": 1\n}" {
		t.Errorf("small JSON payload = %q truncated=%v, want it indented and whole", got, truncated)
	}
}

// Literal leaves a reference as text, for callers that must not resolve one.
func TestLiteralResolvesNothing(t *testing.T) {
	t.Setenv("HERMOD_ENV_PANMAIL_KEY", "key-123")
	gw := newPanmailGateway(t)
	settings := panmailSettings(gw.url)
	settings.PanmailAPIKey = "{{env:PANMAIL_KEY}}"

	if err := settings.SendPanmail(t.Context(), Literal, PanmailMail{Subject: "s", Text: "t"}); err != nil {
		t.Fatalf("SendPanmail = %v", err)
	}
	if _, keys := gw.requests(); len(keys) != 1 || keys[0] != "{{env:PANMAIL_KEY}}" {
		t.Fatalf("X-API-Key = %v, want the reference as written", keys)
	}
}

// sentEmails replaces the SMTP send for the test and records what it was given.
func sentEmails(t *testing.T) func() []gsmail.Email {
	t.Helper()
	var mu sync.Mutex
	var got []gsmail.Email
	prev := smtpSend
	smtpSend = func(_ context.Context, _ NotificationSettings, e gsmail.Email) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e)
		return nil
	}
	t.Cleanup(func() { smtpSend = prev })
	return func() []gsmail.Email {
		mu.Lock()
		defer mu.Unlock()
		return append([]gsmail.Email(nil), got...)
	}
}

// telegramBot stands in for the Telegram Bot API and records each message.
func telegramBot(t *testing.T) func() []map[string]string {
	t.Helper()
	var mu sync.Mutex
	var got []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["path"] = r.URL.Path
		mu.Lock()
		got = append(got, body)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	prev := telegramAPIBase
	telegramAPIBase = srv.URL
	t.Cleanup(func() { telegramAPIBase = prev })
	return func() []map[string]string {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]string(nil), got...)
	}
}

func smtpAndTelegramSettings() NotificationSettings {
	return NotificationSettings{
		BaseURL:  "https://hermod.example.com",
		SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPFrom: "hermod@example.com",
		DefaultEmail:  "ops@example.com, oncall@example.com",
		TelegramToken: "123:abc", TelegramChatID: "-100200",
	}
}

// The digest goes to every channel it is turned on for, not only Panmail.
func TestDeadLetterDigestGoesByEmailAndTelegram(t *testing.T) {
	emails := sentEmails(t)
	telegram := telegramBot(t)
	settings := smtpAndTelegramSettings()
	settings.EmailDeadLetters = true
	settings.TelegramDeadLetters = true
	svc, timer := digestService(t, settings)

	for i := 1; i <= 12; i++ {
		svc.DeadLettered(ordersWorkflow, sample(i))
	}
	timer.fire(t, 0)
	svc.Wait()

	mails := emails()
	if len(mails) != 1 {
		t.Fatalf("sent %d emails, want 1", len(mails))
	}
	m := mails[0]
	if m.From != "hermod@example.com" || len(m.To) != 2 || m.To[1] != "oncall@example.com" {
		t.Fatalf("from %q to %v", m.From, m.To)
	}
	if !strings.Contains(m.Subject, `12 messages dead-lettered in "Orders sync"`) {
		t.Errorf("subject = %q", m.Subject)
	}
	for _, want := range []string{"transform-1", "field total is not a number (row 1)", `{"order":1}`, "/dead-letters?workflow_id=wf-1", "2 more"} {
		if !strings.Contains(string(m.Body), want) {
			t.Errorf("text body lacks %q", want)
		}
	}
	if !strings.Contains(string(m.HTMLBody), "/dead-letters?workflow_id=wf-1") {
		t.Error("the email has no HTML body with the link")
	}

	msgs := telegram()
	if len(msgs) != 1 {
		t.Fatalf("sent %d Telegram messages, want 1", len(msgs))
	}
	tg := msgs[0]
	if tg["chat_id"] != "-100200" || tg["parse_mode"] != "HTML" || tg["path"] != "/bot123:abc/sendMessage" {
		t.Fatalf("telegram request = %v", tg)
	}
	for _, want := range []string{"12 messages dead-lettered", "Orders sync", "m-1", "transform-1", "field total is not a number (row 1)", "/dead-letters?workflow_id=wf-1"} {
		if !strings.Contains(tg["text"], want) {
			t.Errorf("telegram text lacks %q:\n%s", want, tg["text"])
		}
	}
	if n := utf8.RuneCountInString(tg["text"]); n > telegramMessageLimit {
		t.Errorf("telegram text is %d characters, over the limit %d", n, telegramMessageLimit)
	}
}

// A channel that is configured but not turned on for dead letters gets
// nothing: an inbox that takes alerts did not ask for every parked message.
func TestDeadLetterDigestOnlyGoesWhereItIsTurnedOn(t *testing.T) {
	emails := sentEmails(t)
	telegram := telegramBot(t)
	settings := smtpAndTelegramSettings()
	settings.TelegramDeadLetters = true
	svc, timer := digestService(t, settings)

	svc.DeadLettered(ordersWorkflow, sample(1))
	timer.fire(t, 0)
	svc.Wait()

	if n := len(emails()); n != 0 {
		t.Errorf("email got %d digests with email dead letters off", n)
	}
	if n := len(telegram()); n != 1 {
		t.Errorf("telegram got %d digests, want 1", n)
	}
}

// Telegram reads the message as HTML, so what other systems wrote is
// escaped, and a huge payload is cut to fit Telegram's limit.
func TestDeadLetterTelegramEscapesAndFits(t *testing.T) {
	telegram := telegramBot(t)
	settings := smtpAndTelegramSettings()
	settings.TelegramDeadLetters = true
	svc, timer := digestService(t, settings)

	for i := range 10 {
		svc.DeadLettered(storage.Workflow{ID: "wf-x", Name: "<b>evil</b>"}, func() DeadLetterSample {
			return DeadLetterSample{MessageID: fmt.Sprint("m-", i), NodeID: "n", Error: "<script>x</script>",
				Payload: strings.Repeat("é", 3000)}
		})
	}
	timer.fire(t, 0)
	svc.Wait()

	msgs := telegram()
	if len(msgs) != 1 {
		t.Fatalf("sent %d, want 1", len(msgs))
	}
	text := msgs[0]["text"]
	if strings.Contains(text, "<script>") || strings.Contains(text, "<b>evil</b>") {
		t.Fatalf("telegram text was not escaped:\n%s", text)
	}
	if n := utf8.RuneCountInString(text); n > telegramMessageLimit {
		t.Fatalf("telegram text is %d characters, over %d", n, telegramMessageLimit)
	}
}

func TestDeadLetterContentsCanBeLeftOutOfEveryChannel(t *testing.T) {
	emails := sentEmails(t)
	telegram := telegramBot(t)
	settings := smtpAndTelegramSettings()
	settings.EmailDeadLetters, settings.TelegramDeadLetters, settings.DeadLetterHidePayload = true, true, true
	svc, timer := digestService(t, settings)

	svc.DeadLettered(ordersWorkflow, sample(1))
	timer.fire(t, 0)
	svc.Wait()

	// The payload is {"order":1}; HTML escapes its quotes.
	carries := func(s string) bool { return strings.Contains(s, `order":1`) || strings.Contains(s, "order&#34;:1") }
	if m := emails(); len(m) != 1 || carries(string(m[0].Body)) || carries(string(m[0].HTMLBody)) {
		t.Errorf("the email carries the payload:\n%s\n%s", m[0].Body, m[0].HTMLBody)
	}
	if tg := telegram(); len(tg) != 1 || carries(tg[0]["text"]) {
		t.Errorf("the telegram message carries the payload: %v", tg)
	}
}
