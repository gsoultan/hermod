package registry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/notification"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/security/secrets"
)

// mailStorage answers notification_settings with panmail configured, and
// holds vhost secrets like the real backends.
type mailStorage struct {
	secretStorage
	settings notification.NotificationSettings
}

func (s *mailStorage) GetSetting(_ context.Context, key string) (string, error) {
	if key != "notification_settings" {
		return "", nil
	}
	raw, err := json.Marshal(s.settings)
	return string(raw), err
}

// mailGateway records what panmail's SendEmail procedure was sent.
type mailGateway struct {
	mu     sync.Mutex
	bodies []map[string]any
	keys   []string
}

func (g *mailGateway) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(raw, &decoded)
		g.mu.Lock()
		g.bodies = append(g.bodies, decoded)
		g.keys = append(g.keys, r.Header.Get("X-API-Key"))
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messageId":"msg-1","status":"EMAIL_EVENT_TYPE_PENDING"}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (g *mailGateway) sent() ([]map[string]any, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]map[string]any(nil), g.bodies...), append([]string(nil), g.keys...)
}

func newMailRegistry(t *testing.T, apiKey string) (*Registry, *mailStorage, *mailGateway) {
	t.Helper()
	gw := &mailGateway{}
	st := &mailStorage{
		secretStorage: secretStorage{secrets: map[string]string{}},
		settings: notification.NotificationSettings{
			BaseURL:            "https://hermod.example.com",
			PanmailURL:         gw.start(t),
			PanmailAPIKey:      apiKey,
			PanmailProviderID:  "prov-1",
			PanmailFrom:        "alerts@example.com",
			PanmailTo:          "ops@example.com",
			PanmailDeadLetters: true,
		},
	}
	return NewRegistry(st), st, gw
}

// What the engine reports about a parked message reaches the dead-letter
// digest, and the digest goes out through panmail with the workflow, the
// node, the error and the message itself.
func TestDeadLettersAreMailedThroughPanmail(t *testing.T) {
	reg, _, gw := newMailRegistry(t, "key-123")
	wf := storage.Workflow{ID: "wf-1", Name: "Orders sync"}

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("m-1")
	msg.SetPayload([]byte(`{"order":42}`))

	reg.deadLetterObserver(wf)(pkgengine.DeadLetter{
		WorkflowID: wf.ID, MessageID: "m-1", NodeID: "transform-1",
		Error: "field total is not a number", FailedAt: time.Now(), Message: msg,
	})
	reg.notificationService.FlushDeadLetters()
	reg.notificationService.Wait()

	bodies, keys := gw.sent()
	if len(bodies) != 1 {
		t.Fatalf("panmail got %d mails, want 1", len(bodies))
	}
	if keys[0] != "key-123" {
		t.Errorf("X-API-Key = %q, want key-123", keys[0])
	}
	text, _ := bodies[0]["bodyText"].(string)
	for _, want := range []string{"Orders sync", "transform-1", "field total is not a number", `"order": 42`, "/workflows/wf-1/edit?dlq=open"} {
		if !strings.Contains(text, want) {
			t.Errorf("mail lacks %q:\n%s", want, text)
		}
	}
}

// The settings are global, so their references resolve from the global
// secret manager only. A vhost secret of the same name must not be able to
// replace the key every alert is sent with.
func TestPanmailSettingsResolveGlobalSecretsOnly(t *testing.T) {
	t.Setenv(secrets.DefaultEnvPrefix+"PANMAIL_KEY", "key-123")
	reg, st, gw := newMailRegistry(t, "{{secret:PANMAIL_KEY}}")
	st.set("tenant-a", "PANMAIL_KEY", "tenant-a-key")
	wf := storage.Workflow{ID: "wf-1", Name: "Orders sync", VHost: "tenant-a"}

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("m-1")

	reg.deadLetterObserver(wf)(pkgengine.DeadLetter{WorkflowID: wf.ID, MessageID: "m-1", Message: msg})
	reg.notificationService.FlushDeadLetters()
	reg.notificationService.Wait()

	if _, keys := gw.sent(); len(keys) != 1 || keys[0] != "key-123" {
		t.Fatalf("X-API-Key = %v, want the global key-123", keys)
	}
}
