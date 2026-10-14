package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"sync"
	texttemplate "text/template"
	"time"
	"unicode/utf8"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// DeadLetterDigestWindow is how long a workflow's dead letters gather before
// they are mailed. The first dead letter opens the window and the mail goes
// when it closes, so a workflow parking a thousand messages a second sends one
// mail a minute, not a thousand.
const DeadLetterDigestWindow = time.Minute

const (
	// deadLetterSamples is how many messages one digest shows in full. The
	// rest are counted.
	deadLetterSamples = 10
	// deadLetterPayloadCap bounds one message's payload in a digest.
	deadLetterPayloadCap = 4 << 10
)

// DeadLetterSample is one dead-lettered message as a digest shows it.
type DeadLetterSample struct {
	MessageID string
	NodeID    string
	SinkID    string
	Error     string
	FailedAt  time.Time
	// Payload is the message as text, already capped by payloadText.
	Payload   string
	Truncated bool
}

// SampleMessage reads what a digest shows of msg: its payload, its after
// image, or its data, as indented JSON when it is JSON, capped.
func SampleMessage(msg hermod.Message) (payload string, truncated bool) {
	if msg == nil {
		return "", false
	}
	raw := msg.Payload()
	if len(raw) == 0 {
		raw = msg.After()
	}
	if len(raw) == 0 {
		if data := msg.Data(); len(data) > 0 {
			raw, _ = json.Marshal(data)
		}
	}
	return payloadText(raw)
}

// payloadText renders raw for a mail: indented when it is JSON, cut at
// deadLetterPayloadCap on a rune boundary.
func payloadText(raw []byte) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	// Indenting a payload far past the cap is wasted work that is then cut.
	if len(raw) <= deadLetterPayloadCap && json.Valid(raw) {
		var out bytes.Buffer
		if json.Indent(&out, raw, "", "  ") == nil {
			raw = out.Bytes()
		}
	}
	if len(raw) <= deadLetterPayloadCap {
		return string(raw), false
	}
	cut := raw[:deadLetterPayloadCap]
	for len(cut) > 0 && !utf8.Valid(cut) {
		cut = cut[:len(cut)-1]
	}
	return string(cut), true
}

// deadLetterBatch is one workflow's open window.
type deadLetterBatch struct {
	wf      storage.Workflow
	count   int
	samples []DeadLetterSample
	opened  time.Time
}

// deadLetterDigest gathers dead letters per workflow until the window closes.
type deadLetterDigest struct {
	mu      sync.Mutex
	pending map[string]*deadLetterBatch
	// after schedules f once d has passed. Tests replace it.
	after func(d time.Duration, f func())
}

func newDeadLetterDigest() *deadLetterDigest {
	return &deadLetterDigest{
		pending: make(map[string]*deadLetterBatch),
		after:   func(d time.Duration, f func()) { time.AfterFunc(d, f) },
	}
}

// DeadLettered records that wf parked a message. sample builds what the mail
// shows of it and is called only while the digest still has room to show it,
// so a burst costs a counter increment per message past the first few.
//
// It runs on the engine's write path and never blocks on the network.
func (s *Service) DeadLettered(wf storage.Workflow, sample func() DeadLetterSample) {
	d := s.deadLetters
	d.mu.Lock()
	b, open := d.pending[wf.ID]
	if !open {
		b = &deadLetterBatch{wf: wf, opened: time.Now()}
		d.pending[wf.ID] = b
	}
	b.count++
	if len(b.samples) < deadLetterSamples && sample != nil {
		b.samples = append(b.samples, sample())
	}
	d.mu.Unlock()

	if !open {
		d.after(DeadLetterDigestWindow, func() { s.flushDeadLetters(wf.ID) })
	}
}

// FlushDeadLetters mails every open window now. Shutdown calls it so the
// dead letters of the last minute are not lost with the process; Wait or
// WaitFor then waits for the sends.
func (s *Service) FlushDeadLetters() {
	d := s.deadLetters
	d.mu.Lock()
	ids := make([]string, 0, len(d.pending))
	for id := range d.pending {
		ids = append(ids, id)
	}
	d.mu.Unlock()
	for _, id := range ids {
		s.flushDeadLetters(id)
	}
}

// flushDeadLetters closes one workflow's window and mails it. A window already
// flushed (by FlushDeadLetters, before its timer fired) is gone, and this does
// nothing.
func (s *Service) flushDeadLetters(workflowID string) {
	d := s.deadLetters
	d.mu.Lock()
	b, ok := d.pending[workflowID]
	delete(d.pending, workflowID)
	d.mu.Unlock()
	if !ok {
		return
	}

	s.mu.RLock()
	store, resolve, logger := s.storage, s.resolve, s.logger
	s.mu.RUnlock()
	if store == nil {
		return
	}

	s.inflight.add()
	go func() {
		defer s.inflight.done()
		ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
		defer cancel()

		val, err := store.GetSetting(ctx, "notification_settings")
		if err != nil || val == "" {
			return
		}
		var ns NotificationSettings
		if err := json.Unmarshal([]byte(val), &ns); err != nil {
			return
		}
		if !ns.PanmailDeadLetters || !ns.panmailConfigured() {
			return
		}
		if err := ns.SendPanmail(ctx, resolve, ns.deadLetterMail(b)); err != nil && logger != nil {
			logger.Error("Dead-letter digest could not be mailed",
				"channel", "panmail",
				"workflow_id", b.wf.ID,
				"dead_letters", b.count,
				"error", err.Error())
		}
	}()
}

// deadLetterView is what the digest templates see.
type deadLetterView struct {
	Workflow    storage.Workflow
	Count       int
	Samples     []DeadLetterSample
	More        int
	QueueURL    string
	HidePayload bool
	Opened      string
}

func (ns NotificationSettings) deadLetterMail(b *deadLetterBatch) PanmailMail {
	view := deadLetterView{
		Workflow:    b.wf,
		Count:       b.count,
		Samples:     b.samples,
		More:        b.count - len(b.samples),
		HidePayload: ns.PanmailHidePayload,
		Opened:      b.opened.UTC().Format(time.RFC3339),
	}
	if ns.BaseURL != "" {
		// The dead letter page, narrowed to this workflow: it shows every
		// dead letter whatever the workflow's sink is.
		view.QueueURL = fmt.Sprintf("%s/dead-letters?workflow_id=%s", strings.TrimRight(ns.BaseURL, "/"), url.QueryEscape(b.wf.ID))
	}

	name := b.wf.Name
	if name == "" {
		name = b.wf.ID
	}
	noun := "messages"
	if b.count == 1 {
		noun = "message"
	}
	mail := PanmailMail{
		Subject: fmt.Sprintf("[Hermod] %d %s dead-lettered in %q", b.count, noun, name),
		// One window per workflow, so its id and opening instant name it.
		IdempotencyKey: fmt.Sprintf("hermod-dlq-%s-%d", b.wf.ID, b.opened.UnixNano()),
	}
	var text, html bytes.Buffer
	if err := deadLetterText.Execute(&text, view); err == nil {
		mail.Text = text.String()
	}
	if err := deadLetterHTML.Execute(&html, view); err == nil {
		mail.HTML = html.String()
	}
	return mail
}

// Where names the step that gave up on a message.
func (d DeadLetterSample) Where() string {
	parts := make([]string, 0, 2)
	if d.NodeID != "" {
		parts = append(parts, "node "+d.NodeID)
	}
	if d.SinkID != "" {
		parts = append(parts, "sink "+d.SinkID)
	}
	if len(parts) == 0 {
		return "before any sink (validation or routing)"
	}
	return strings.Join(parts, ", ")
}

// When is the failure time as the mail shows it.
func (d DeadLetterSample) When() string {
	if d.FailedAt.IsZero() {
		return ""
	}
	return d.FailedAt.UTC().Format(time.RFC3339)
}

var templateFuncs = map[string]any{"inc": func(i int) int { return i + 1 }}

// The text body is plain text, so it is rendered with text/template: an
// html/template would escape the quotes and angle brackets of the payload.
var deadLetterText = texttemplate.Must(texttemplate.New("dlq-text").Funcs(templateFuncs).Parse(`{{.Count}} message(s) were dead-lettered in workflow "{{.Workflow.Name}}" ({{.Workflow.ID}}) since {{.Opened}}.
{{if .QueueURL}}
Open the dead-letter queue: {{.QueueURL}}
{{end}}{{range $i, $s := .Samples}}
{{inc $i}}. Message {{$s.MessageID}}{{if $s.When}} at {{$s.When}}{{end}}
   Failed at: {{$s.Where}}
   Error: {{if $s.Error}}{{$s.Error}}{{else}}(no error recorded){{end}}
{{- if not $.HidePayload}}{{if $s.Payload}}
   Message:
{{$s.Payload}}{{if $s.Truncated}}
   (cut at 4 KB){{end}}{{end}}{{end}}
{{end}}{{if gt .More 0}}
...and {{.More}} more not shown.
{{end}}`))

var deadLetterHTML = template.Must(template.New("dlq").Funcs(templateFuncs).Parse(`<div style="font-family:sans-serif">
<p><strong>{{.Count}}</strong> message(s) were dead-lettered in workflow <strong>{{.Workflow.Name}}</strong> (<code>{{.Workflow.ID}}</code>) since {{.Opened}}.</p>
{{if .QueueURL}}<p><a href="{{.QueueURL}}">Open the dead-letter queue</a></p>{{end}}
{{range $i, $s := .Samples}}<h3 style="margin-bottom:4px">{{inc $i}}. Message <code>{{$s.MessageID}}</code></h3>
<p style="margin:0">{{if $s.When}}At {{$s.When}}. {{end}}Failed at {{$s.Where}}.</p>
<p style="margin:0;color:#b42318">{{if $s.Error}}{{$s.Error}}{{else}}(no error recorded){{end}}</p>
{{if not $.HidePayload}}{{if $s.Payload}}<pre style="background:#f4f4f5;padding:8px;white-space:pre-wrap">{{$s.Payload}}</pre>{{if $s.Truncated}}<p style="margin:0"><em>Cut at 4 KB.</em></p>{{end}}{{end}}{{end}}
{{end}}{{if gt .More 0}}<p>…and {{.More}} more not shown.</p>{{end}}
</div>`))
