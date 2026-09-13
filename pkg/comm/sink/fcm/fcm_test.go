package fcm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"firebase.google.com/go/v4/messaging"
	"github.com/gsoultan/hermod"
)

// A syntactically valid service account. The tests never authenticate with it:
// every send goes to a local server through an injected HTTP client, which is
// the only reason the SDK will accept credentials this fake.
const serviceAccountJSON = `{"type":"service_account","project_id":"demo-project"}`

// --- fixtures ---------------------------------------------------------------

type mockMessage struct {
	hermod.Message
	id        string
	operation hermod.Operation
	table     string
	schema    string
	payload   []byte
	metadata  map[string]string
	data      map[string]any
}

func (m *mockMessage) ID() string                  { return m.id }
func (m *mockMessage) Operation() hermod.Operation { return m.operation }
func (m *mockMessage) Table() string               { return m.table }
func (m *mockMessage) Schema() string              { return m.schema }
func (m *mockMessage) Before() []byte              { return nil }
func (m *mockMessage) After() []byte               { return m.payload }
func (m *mockMessage) Payload() []byte             { return m.payload }
func (m *mockMessage) Metadata() map[string]string {
	if m.metadata == nil {
		return map[string]string{}
	}
	return m.metadata
}
func (m *mockMessage) Data() map[string]any           { return m.data }
func (m *mockMessage) MetadataRef() map[string]string { return m.Metadata() }
func (m *mockMessage) DataRef() map[string]any        { return m.data }
func (m *mockMessage) Clone() hermod.Message          { return m }
func (m *mockMessage) ToMap() map[string]any          { return m.data }
func (m *mockMessage) ClearPayloads()                 {}
func (m *mockMessage) Retain()                        {}
func (m *mockMessage) Release()                       {}

func testMessage() *mockMessage {
	return &mockMessage{
		id:        "ord-1",
		operation: hermod.OpCreate,
		table:     "orders",
		schema:    "public",
		payload:   []byte(`{"id":"ord-1","customer":"Ada","total":"42.00"}`),
		metadata:  map[string]string{},
		data: map[string]any{
			"id":       "ord-1",
			"customer": "Ada",
			"total":    "42.00",
			"unread":   3,
		},
	}
}

// fcmServer stands in for https://fcm.googleapis.com. It records every request
// body it is sent, so a test can assert on the exact JSON FCM would receive.
type fcmServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []map[string]any
	// respond, when set, decides the reply for request n (0-based).
	respond func(n int, w http.ResponseWriter)
}

func newFCMServer(t *testing.T) *fcmServer {
	t.Helper()
	s := &fcmServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Errorf("request body is not JSON: %v (%s)", err, body)
		}
		s.mu.Lock()
		n := len(s.requests)
		s.requests = append(s.requests, parsed)
		respond := s.respond
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if respond != nil {
			respond(n, w)
			return
		}
		fmt.Fprintf(w, `{"name":"projects/demo-project/messages/%d"}`, n)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *fcmServer) calls() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.requests...)
}

// only returns the single message body sent, failing if there was not exactly one.
func (s *fcmServer) only(t *testing.T) map[string]any {
	t.Helper()
	calls := s.calls()
	if len(calls) != 1 {
		t.Fatalf("got %d sends, want exactly 1", len(calls))
	}
	msg, ok := calls[0]["message"].(map[string]any)
	if !ok {
		t.Fatalf("request has no message object: %+v", calls[0])
	}
	return msg
}

// errorBody is the shape FCM returns for a refusal. The SDK maps the
// ErrorCode in the details array onto messaging.Is* predicates.
func errorBody(status, fcmCode string) string {
	return fmt.Sprintf(`{"error":{"status":%q,"message":"denied","details":[
		{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":%q}]}}`,
		status, fcmCode)
}

func newTestSink(t *testing.T, cfg Config) *Sink {
	t.Helper()
	if cfg.CredentialsJSON == "" && !cfg.UseDefaultCredentials {
		cfg.CredentialsJSON = serviceAccountJSON
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// pointAt wires a sink at the fake FCM server.
func pointAt(t *testing.T, srv *fcmServer, cfg Config) *Sink {
	t.Helper()
	cfg.Endpoint = srv.URL
	cfg.HTTPClient = srv.Client()
	return newTestSink(t, cfg)
}

// --- targeting --------------------------------------------------------------

func TestTargetResolution(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		metadata map[string]string
		want     map[string]string // field -> value, on the wire
		wantErr  string
	}{
		{
			name: "static topic",
			cfg:  Config{Topic: "orders"},
			want: map[string]string{"topic": "orders"},
		},
		{
			name: "topic template renders from the row",
			cfg:  Config{Topic: "orders-{{.table}}"},
			want: map[string]string{"topic": "orders-orders"},
		},
		{
			name: "token template renders from a column",
			cfg:  Config{Token: "{{.customer}}-device"},
			want: map[string]string{"token": "Ada-device"},
		},
		{
			name:     "metadata overrides the configured default",
			cfg:      Config{Topic: "orders"},
			metadata: map[string]string{"fcm_token": "from-metadata"},
			want:     map[string]string{"token": "from-metadata"},
		},
		{
			// A transformer that copies a nullable column into metadata writes
			// the key with an empty value. Treating "present" as "usable" sent
			// a message with a blank token and no fallback.
			name:     "a blank metadata value falls through to the default",
			cfg:      Config{Topic: "orders"},
			metadata: map[string]string{"fcm_token": "   "},
			want:     map[string]string{"topic": "orders"},
		},
		{
			name:     "metadata condition",
			cfg:      Config{},
			metadata: map[string]string{"fcm_condition": "'a' in topics"},
			want:     map[string]string{"condition": "'a' in topics"},
		},
		{
			name:    "nothing to send to",
			cfg:     Config{},
			wantErr: "no fcm destination",
		},
		{
			// The target template rendered empty for this row. Sending it would
			// be an FCM refusal on every message; saying which row and which
			// field is what makes it fixable.
			name:    "template renders empty",
			cfg:     Config{Token: "{{.missing}}"},
			wantErr: "token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFCMServer(t)
			sink := pointAt(t, srv, tt.cfg)
			msg := testMessage()
			msg.metadata = tt.metadata

			err := sink.Write(context.Background(), msg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Write = nil error, want one mentioning %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not mention %q", err, tt.wantErr)
				}
				if !errors.Is(err, ErrPermanent) {
					t.Error("a message with no usable destination is permanent; retrying cannot fix it")
				}
				return
			}
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			got := srv.only(t)
			for field, want := range tt.want {
				if got[field] != want {
					t.Errorf("%s = %v, want %q", field, got[field], want)
				}
			}
			for _, field := range []string{"token", "topic", "condition"} {
				if _, expected := tt.want[field]; !expected {
					if _, present := got[field]; present {
						t.Errorf("%s is set as well; FCM accepts exactly one target", field)
					}
				}
			}
		})
	}
}

// --- payload ----------------------------------------------------------------

func TestWriteSendsNotificationAndData(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{
		Topic:    "orders",
		Title:    "Order {{.id}}",
		Body:     "{{.customer}} spent {{.total}}",
		ImageURL: "https://cdn.example.com/{{.id}}.png",
		DataMode: DataFields,
		Data:     map[string]string{"deeplink": "app://orders/{{.id}}"},
	})

	if err := sink.Write(context.Background(), testMessage()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := srv.only(t)
	notification, _ := got["notification"].(map[string]any)
	if notification["title"] != "Order ord-1" {
		t.Errorf("title = %v", notification["title"])
	}
	if notification["body"] != "Ada spent 42.00" {
		t.Errorf("body = %v", notification["body"])
	}
	if notification["image"] != "https://cdn.example.com/ord-1.png" {
		t.Errorf("image = %v", notification["image"])
	}

	data, _ := got["data"].(map[string]any)
	for k, want := range map[string]string{
		"id":        "ord-1",
		"customer":  "Ada",
		"total":     "42.00",
		"unread":    "3", // FCM data values are strings; numbers are rendered
		"operation": "create",
		"table":     "orders",
		"schema":    "public",
		"deeplink":  "app://orders/ord-1",
	} {
		if data[k] != want {
			t.Errorf("data[%s] = %v, want %q", k, data[k], want)
		}
	}
}

// The envelope must not overwrite a column of the same name. A CDC row with its
// own `table` column had it replaced by the envelope's in 15 of 20 runs before
// the ordering was fixed in the other sinks.
func TestRowColumnsWinOverTheEnvelope(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Topic: "orders", DataMode: DataFields})

	msg := testMessage()
	msg.data = map[string]any{"id": "row-id", "table": "row-table", "operation": "row-op"}

	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ := srv.only(t)["data"].(map[string]any)
	for k, want := range map[string]string{"id": "row-id", "table": "row-table", "operation": "row-op"} {
		if data[k] != want {
			t.Errorf("data[%s] = %v, want the row's own value %q", k, data[k], want)
		}
	}
}

func TestDataModes(t *testing.T) {
	tests := []struct {
		mode    DataMode
		wantKey string
		absent  []string
	}{
		{DataEnvelope, "payload", nil},
		{DataFields, "customer", []string{"payload"}},
		{DataNone, "", []string{"payload", "customer", "id"}},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			srv := newFCMServer(t)
			sink := pointAt(t, srv, Config{Topic: "orders", DataMode: tt.mode, Title: "t", Body: "b"})
			if err := sink.Write(context.Background(), testMessage()); err != nil {
				t.Fatalf("Write: %v", err)
			}
			data, _ := srv.only(t)["data"].(map[string]any)
			if tt.wantKey != "" && data[tt.wantKey] == nil {
				t.Errorf("data[%s] missing; got %+v", tt.wantKey, data)
			}
			for _, k := range tt.absent {
				if _, present := data[k]; present {
					t.Errorf("data[%s] present under mode %s", k, tt.mode)
				}
			}
		})
	}
}

// FCM refuses a data payload over 4096 bytes. Sending one anyway meant a
// refusal on every retry and then the dead-letter queue, for a row that was
// never going to fit.
func TestOversizedDataPayload(t *testing.T) {
	big := strings.Repeat("x", 5000)

	t.Run("error is the default and is permanent", func(t *testing.T) {
		srv := newFCMServer(t)
		sink := pointAt(t, srv, Config{Topic: "orders"})
		msg := testMessage()
		msg.payload = []byte(`"` + big + `"`)

		err := sink.Write(context.Background(), msg)
		if err == nil {
			t.Fatal("Write of an oversized payload = nil error")
		}
		if !errors.Is(err, ErrPermanent) {
			t.Errorf("error %q is not permanent; a payload too big never gets smaller on retry", err)
		}
		if !strings.Contains(err.Error(), "4096") {
			t.Errorf("error %q does not name the limit", err)
		}
		if n := len(srv.calls()); n != 0 {
			t.Errorf("made %d calls to FCM; the message should never have been sent", n)
		}
	})

	t.Run("truncate keeps the message deliverable", func(t *testing.T) {
		srv := newFCMServer(t)
		sink := pointAt(t, srv, Config{Topic: "orders", OnOversize: OversizeTruncate})
		msg := testMessage()
		msg.payload = []byte(`"` + big + `"`)

		if err := sink.Write(context.Background(), msg); err != nil {
			t.Fatalf("Write: %v", err)
		}
		data, _ := srv.only(t)["data"].(map[string]any)
		if dataBytes(data) > defaultMaxDataBytes {
			t.Errorf("truncated payload is still %d bytes", dataBytes(data))
		}
		if data["id"] != "ord-1" {
			t.Error("truncation dropped the envelope; the scalars are what make it identifiable")
		}
	})

	t.Run("drop sends the notification without the data", func(t *testing.T) {
		srv := newFCMServer(t)
		sink := pointAt(t, srv, Config{Topic: "orders", OnOversize: OversizeDrop, Title: "t", Body: "b"})
		msg := testMessage()
		msg.payload = []byte(`"` + big + `"`)

		if err := sink.Write(context.Background(), msg); err != nil {
			t.Fatalf("Write: %v", err)
		}
		got := srv.only(t)
		if _, present := got["data"]; present {
			t.Error("data survived under the drop policy")
		}
		if got["notification"] == nil {
			t.Error("the notification was dropped too; only the data is oversized")
		}
	})
}

// --- platform configuration -------------------------------------------------

func TestPlatformConfig(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{
		Topic: "orders",
		Title: "t",
		Body:  "b",
		Android: AndroidConfig{
			Priority:              "high",
			TTL:                   90 * time.Second,
			CollapseKey:           "orders-{{.id}}",
			RestrictedPackageName: "com.example.app",
			ChannelID:             "orders",
			Sound:                 "ding",
			Icon:                  "ic_order",
			Color:                 "#ff8800",
			Tag:                   "order-{{.id}}",
			ClickAction:           "OPEN_ORDER",
			NotificationPriority:  "high",
		},
		APNS: APNSConfig{
			Priority:         "10",
			Expiration:       2 * time.Minute,
			CollapseID:       "order-{{.id}}",
			Sound:            "ding.caf",
			Badge:            "{{.unread}}",
			MutableContent:   true,
			ContentAvailable: true,
			Category:         "ORDER",
			ThreadID:         "orders",
		},
		Webpush: WebpushConfig{
			Link:  "https://app.example.com/orders/{{.id}}",
			Icon:  "https://cdn.example.com/icon.png",
			Badge: "https://cdn.example.com/badge.png",
			TTL:   time.Hour,
		},
		AnalyticsLabel: "orders_v2",
	})

	if err := sink.Write(context.Background(), testMessage()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := srv.only(t)

	android, _ := got["android"].(map[string]any)
	if android["priority"] != "high" {
		t.Errorf("android.priority = %v", android["priority"])
	}
	if android["ttl"] != "90s" {
		t.Errorf("android.ttl = %v, want 90s", android["ttl"])
	}
	if android["collapse_key"] != "orders-ord-1" {
		t.Errorf("android.collapse_key = %v", android["collapse_key"])
	}
	if android["restricted_package_name"] != "com.example.app" {
		t.Errorf("android.restricted_package_name = %v", android["restricted_package_name"])
	}
	an, _ := android["notification"].(map[string]any)
	for k, want := range map[string]string{
		"channel_id":            "orders",
		"sound":                 "ding",
		"icon":                  "ic_order",
		"color":                 "#ff8800",
		"tag":                   "order-ord-1",
		"click_action":          "OPEN_ORDER",
		"notification_priority": "PRIORITY_HIGH",
	} {
		if an[k] != want {
			t.Errorf("android.notification.%s = %v, want %q", k, an[k], want)
		}
	}

	apns, _ := got["apns"].(map[string]any)
	headers, _ := apns["headers"].(map[string]any)
	if headers["apns-priority"] != "10" {
		t.Errorf("apns-priority = %v", headers["apns-priority"])
	}
	if headers["apns-collapse-id"] != "order-ord-1" {
		t.Errorf("apns-collapse-id = %v", headers["apns-collapse-id"])
	}
	if headers["apns-expiration"] == nil {
		t.Error("apns-expiration missing")
	}
	payload, _ := apns["payload"].(map[string]any)
	aps, _ := payload["aps"].(map[string]any)
	if aps["sound"] != "ding.caf" {
		t.Errorf("aps.sound = %v", aps["sound"])
	}
	if aps["badge"] != float64(3) {
		t.Errorf("aps.badge = %v, want the rendered 3", aps["badge"])
	}
	if aps["mutable-content"] != float64(1) {
		t.Errorf("aps.mutable-content = %v", aps["mutable-content"])
	}
	if aps["content-available"] != float64(1) {
		t.Errorf("aps.content-available = %v", aps["content-available"])
	}
	if aps["category"] != "ORDER" {
		t.Errorf("aps.category = %v", aps["category"])
	}
	if aps["thread-id"] != "orders" {
		t.Errorf("aps.thread-id = %v", aps["thread-id"])
	}

	webpush, _ := got["webpush"].(map[string]any)
	wopts, _ := webpush["fcm_options"].(map[string]any)
	if wopts["link"] != "https://app.example.com/orders/ord-1" {
		t.Errorf("webpush link = %v", wopts["link"])
	}
	wn, _ := webpush["notification"].(map[string]any)
	if wn["icon"] != "https://cdn.example.com/icon.png" {
		t.Errorf("webpush icon = %v", wn["icon"])
	}
	wh, _ := webpush["headers"].(map[string]any)
	if wh["TTL"] != "3600" {
		t.Errorf("webpush TTL header = %v, want 3600 seconds", wh["TTL"])
	}

	opts, _ := got["fcm_options"].(map[string]any)
	if opts["analytics_label"] != "orders_v2" {
		t.Errorf("fcm_options.analytics_label = %v", opts["analytics_label"])
	}
}

// --- error classification ---------------------------------------------------

func TestErrorClassification(t *testing.T) {
	tests := []struct {
		name          string
		status        string
		code          string
		wantPermanent bool
	}{
		{"unregistered token", "NOT_FOUND", "UNREGISTERED", true},
		{"invalid argument", "INVALID_ARGUMENT", "INVALID_ARGUMENT", true},
		{"sender id mismatch", "PERMISSION_DENIED", "SENDER_ID_MISMATCH", true},
		{"third party auth", "UNAUTHENTICATED", "THIRD_PARTY_AUTH_ERROR", true},
		{"server unavailable", "UNAVAILABLE", "UNAVAILABLE", false},
		{"quota exceeded", "RESOURCE_EXHAUSTED", "QUOTA_EXCEEDED", false},
		{"internal", "INTERNAL", "INTERNAL", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFCMServer(t)
			srv.respond = func(_ int, w http.ResponseWriter) {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, errorBody(tt.status, tt.code))
			}
			sink := pointAt(t, srv, Config{Topic: "orders"})

			err := sink.Write(context.Background(), testMessage())
			if err == nil {
				t.Fatal("Write = nil error, want the server's refusal")
			}
			if got := errors.Is(err, ErrPermanent); got != tt.wantPermanent {
				t.Errorf("errors.Is(err, ErrPermanent) = %v, want %v (error: %v)", got, tt.wantPermanent, err)
			}
		})
	}
}

// A token FCM says is dead is worth surfacing on its own: it is the signal an
// operator needs to prune a device registration table.
func TestUnregisteredTokenIsIdentifiable(t *testing.T) {
	srv := newFCMServer(t)
	srv.respond = func(_ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, errorBody("NOT_FOUND", "UNREGISTERED"))
	}
	sink := pointAt(t, srv, Config{Token: "dead-device"})

	err := sink.Write(context.Background(), testMessage())
	var dead *UnregisteredTokenError
	if !errors.As(err, &dead) {
		t.Fatalf("error %v is not an *UnregisteredTokenError", err)
	}
	if dead.Token != "dead-device" {
		t.Errorf("Token = %q, want dead-device", dead.Token)
	}
	if !errors.Is(err, ErrPermanent) {
		t.Error("a dead token is permanent")
	}
}

// --- dry run, ping, close ---------------------------------------------------

func TestDryRunNeverDelivers(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Topic: "orders", DryRun: true})
	if err := sink.Write(context.Background(), testMessage()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if v := srv.calls()[0]["validate_only"]; v != true {
		t.Errorf("validate_only = %v, want true", v)
	}
}

// Ping used to issue a real send. It cost quota and, with a token that happened
// to be live, would have delivered a notification to a real device.
func TestPingValidatesWithoutDelivering(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Topic: "orders"})

	if err := sink.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	calls := srv.calls()
	if len(calls) != 1 {
		t.Fatalf("Ping made %d calls, want 1", len(calls))
	}
	if calls[0]["validate_only"] != true {
		t.Errorf("Ping sent validate_only = %v, want true", calls[0]["validate_only"])
	}
}

// Credentials that FCM rejects are the thing Ping exists to find. A refusal
// about the *message* is not one: it proves the round trip worked.
func TestPingDistinguishesAuthFromMessageRefusals(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		code    string
		wantErr bool
	}{
		{"bad message reaches FCM, so the connection is fine", "INVALID_ARGUMENT", "INVALID_ARGUMENT", false},
		{"unregistered token also proves the round trip", "NOT_FOUND", "UNREGISTERED", false},
		{"wrong credentials is what Ping is for", "UNAUTHENTICATED", "THIRD_PARTY_AUTH_ERROR", true},
		{"sender id mismatch means the wrong project", "PERMISSION_DENIED", "SENDER_ID_MISMATCH", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFCMServer(t)
			srv.respond = func(_ int, w http.ResponseWriter) {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, errorBody(tt.status, tt.code))
			}
			sink := pointAt(t, srv, Config{Topic: "orders"})
			err := sink.Ping(context.Background())
			if (err != nil) != tt.wantErr {
				t.Errorf("Ping error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// --- batching ---------------------------------------------------------------

func TestBatchSinkIsOptIn(t *testing.T) {
	// FCM has no idempotency key, so a retry of a partly-failed batch
	// re-notifies every device that already received it. Being a plain Sink is
	// what makes the engine retry one message at a time.
	plain := newTestSink(t, Config{Topic: "orders"})
	if _, ok := any(plain).(hermod.BatchSink); ok {
		t.Error("the default sink implements BatchSink; whole-batch retries duplicate notifications")
	}

	batching, err := NewBatching(Config{CredentialsJSON: serviceAccountJSON, Topic: "orders"})
	if err != nil {
		t.Fatalf("NewBatching: %v", err)
	}
	t.Cleanup(func() { _ = batching.Close() })
	if _, ok := any(batching).(hermod.BatchSink); !ok {
		t.Error("NewBatching does not implement BatchSink")
	}
}

func TestWriteBatchSendsEveryMessage(t *testing.T) {
	srv := newFCMServer(t)
	cfg := Config{Topic: "orders", Endpoint: srv.URL, HTTPClient: srv.Client(), CredentialsJSON: serviceAccountJSON}
	sink, err := NewBatching(cfg)
	if err != nil {
		t.Fatalf("NewBatching: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	msgs := make([]hermod.Message, 3)
	for i := range msgs {
		m := testMessage()
		m.id = fmt.Sprintf("ord-%d", i)
		msgs[i] = m
	}
	if err := sink.WriteBatch(context.Background(), msgs); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if n := len(srv.calls()); n != 3 {
		t.Errorf("made %d sends for 3 messages", n)
	}
}

// A batch in which nothing could ever succeed must not be retried.
func TestWriteBatchAllPermanentIsPermanent(t *testing.T) {
	srv := newFCMServer(t)
	srv.respond = func(_ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, errorBody("NOT_FOUND", "UNREGISTERED"))
	}
	sink, err := NewBatching(Config{
		CredentialsJSON: serviceAccountJSON, Topic: "orders",
		Endpoint: srv.URL, HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewBatching: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	err = sink.WriteBatch(context.Background(), []hermod.Message{testMessage(), testMessage()})
	if err == nil {
		t.Fatal("WriteBatch = nil error, want the refusals")
	}
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("error %q is not permanent even though every message failed permanently", err)
	}
}

// --- multicast --------------------------------------------------------------

func TestMulticastFansOutOverTokens(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Token: "{{.devices}}", Title: "t", Body: "b"})

	msg := testMessage()
	msg.data = map[string]any{"devices": "tok-a, tok-b ,tok-c"}

	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	calls := srv.calls()
	if len(calls) != 3 {
		t.Fatalf("got %d sends for 3 tokens", len(calls))
	}
	seen := map[string]bool{}
	for _, c := range calls {
		m, _ := c["message"].(map[string]any)
		seen[fmt.Sprint(m["token"])] = true
	}
	for _, want := range []string{"tok-a", "tok-b", "tok-c"} {
		if !seen[want] {
			t.Errorf("no send for token %q; got %v", want, seen)
		}
	}
}

// --- topic management -------------------------------------------------------

// recordingTopicClient stands in for the FCM client's topic-management half.
// The SDK hard-codes the instance-id endpoint, so unlike the send path it
// cannot be pointed at a local server; an injected client is the seam.
type recordingTopicClient struct {
	noSendClient
	subscribed   []string
	unsubscribed []string
	topic        string
}

func (f *recordingTopicClient) SubscribeToTopic(_ context.Context, tokens []string, topic string) (*messaging.TopicManagementResponse, error) {
	f.subscribed, f.topic = tokens, topic
	return &messaging.TopicManagementResponse{SuccessCount: len(tokens)}, nil
}

func (f *recordingTopicClient) UnsubscribeFromTopic(_ context.Context, tokens []string, topic string) (*messaging.TopicManagementResponse, error) {
	f.unsubscribed, f.topic = tokens, topic
	return &messaging.TopicManagementResponse{SuccessCount: len(tokens)}, nil
}

// noSendClient satisfies the send half of the client interface for tests that
// only exercise topic management.
type noSendClient struct{}

func (noSendClient) Send(context.Context, *messaging.Message) (string, error) { return "", nil }
func (noSendClient) SendDryRun(context.Context, *messaging.Message) (string, error) {
	return "", nil
}
func (noSendClient) SendEach(context.Context, []*messaging.Message) (*messaging.BatchResponse, error) {
	return &messaging.BatchResponse{}, nil
}
func (noSendClient) SendEachDryRun(context.Context, []*messaging.Message) (*messaging.BatchResponse, error) {
	return &messaging.BatchResponse{}, nil
}

func TestTopicSubscription(t *testing.T) {
	for _, tt := range []struct {
		action Action
		want   string
	}{{ActionSubscribe, "subscribe"}, {ActionUnsubscribe, "unsubscribe"}} {
		t.Run(string(tt.action), func(t *testing.T) {
			sink := newTestSink(t, Config{Action: tt.action, Topic: "orders", Token: "{{.devices}}"})
			fake := &recordingTopicClient{}
			sink.setClientForTest(fake)

			msg := testMessage()
			msg.data = map[string]any{"devices": "tok-a,tok-b"}
			if err := sink.Write(context.Background(), msg); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got := fake.subscribed
			if tt.action == ActionUnsubscribe {
				got = fake.unsubscribed
			}
			if len(got) != 2 || got[0] != "tok-a" || got[1] != "tok-b" {
				t.Errorf("tokens = %v, want [tok-a tok-b]", got)
			}
			if fake.topic != "orders" {
				t.Errorf("topic = %q", fake.topic)
			}
		})
	}
}

// --- back compatibility -----------------------------------------------------

// The metadata keys the sink shipped with stay honoured: workflows already set
// them from a transformer.
func TestLegacyMetadataKeys(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{})

	msg := testMessage()
	msg.metadata = map[string]string{
		"fcm_token":              "legacy-token",
		"fcm_notification_title": "Legacy title",
		"fcm_notification_body":  "Legacy body",
	}
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := srv.only(t)
	if got["token"] != "legacy-token" {
		t.Errorf("token = %v", got["token"])
	}
	n, _ := got["notification"].(map[string]any)
	if n["title"] != "Legacy title" || n["body"] != "Legacy body" {
		t.Errorf("notification = %+v", n)
	}
	// The default data shape is the one the old sink sent.
	data, _ := got["data"].(map[string]any)
	for _, k := range []string{"payload", "id", "operation", "table", "schema"} {
		if _, present := data[k]; !present {
			t.Errorf("data[%s] missing; the envelope shape is what existing apps parse", k)
		}
	}
}

func TestWriteNilMessage(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Topic: "orders"})
	if err := sink.Write(context.Background(), nil); err != nil {
		t.Errorf("Write(nil) = %v, want nil", err)
	}
	if n := len(srv.calls()); n != 0 {
		t.Errorf("a nil message produced %d sends", n)
	}
}

// A registration token is a capability: whoever holds it can push to that
// device. The column the destination is read from must not travel back inside
// the payload.
//
// Under DataFields the row's columns become data keys, and the token column is
// a column like any other — so a push addressed by {{.device_token}} carried
// that token to the device it was addressed to. A multicast made it worse: the
// field holds every recipient's token, so each device received the whole list.
func TestDestinationColumnsStayOutOfTheDataPayload(t *testing.T) {
	t.Run("single device", func(t *testing.T) {
		srv := newFCMServer(t)
		sink := pointAt(t, srv, Config{
			Token:    "{{.device_token}}",
			DataMode: DataFields,
			Title:    "Order {{.id}}",
			Body:     "for {{.customer}}",
		})

		msg := testMessage()
		msg.data = map[string]any{
			"id": "ord-1", "customer": "Ada", "device_token": "dev-ada-001",
		}
		if err := sink.Write(context.Background(), msg); err != nil {
			t.Fatalf("Write: %v", err)
		}

		got := srv.only(t)
		data, _ := got["data"].(map[string]any)
		if _, present := data["device_token"]; present {
			t.Errorf("the data payload carries device_token back to the device: %v", data["device_token"])
		}
		// Excluding it from the payload must not hide it from the templates:
		// it is still the row's data, it is just not cargo.
		if got["token"] != "dev-ada-001" {
			t.Errorf("token = %v; the field is still the destination", got["token"])
		}
		if data["customer"] != "Ada" {
			t.Errorf("data[customer] = %v; only the destination field is withheld", data["customer"])
		}
	})

	t.Run("multicast", func(t *testing.T) {
		srv := newFCMServer(t)
		sink := pointAt(t, srv, Config{Token: "{{.devices}}", DataMode: DataFields, Title: "t"})

		msg := testMessage()
		msg.data = map[string]any{"id": "ord-2", "devices": "dev-a,dev-b,dev-c"}
		if err := sink.Write(context.Background(), msg); err != nil {
			t.Fatalf("Write: %v", err)
		}

		calls := srv.calls()
		if len(calls) != 3 {
			t.Fatalf("got %d sends, want 3", len(calls))
		}
		for i, c := range calls {
			m, _ := c["message"].(map[string]any)
			data, _ := m["data"].(map[string]any)
			for k, v := range data {
				if strings.Contains(fmt.Sprint(v), "dev-") {
					t.Errorf("send %d carries %s=%v: one device learns the others' tokens", i, k, v)
				}
			}
		}
	})

	t.Run("a topic field is withheld too", func(t *testing.T) {
		srv := newFCMServer(t)
		sink := pointAt(t, srv, Config{Topic: "{{.audience}}", DataMode: DataFields, Title: "t"})

		msg := testMessage()
		msg.data = map[string]any{"id": "ord-3", "audience": "vip-customers"}
		if err := sink.Write(context.Background(), msg); err != nil {
			t.Fatalf("Write: %v", err)
		}
		data, _ := srv.only(t)["data"].(map[string]any)
		if _, present := data["audience"]; present {
			t.Error("the topic field travels in the payload")
		}
	})

	t.Run("an operator who wants it can ask for it back", func(t *testing.T) {
		srv := newFCMServer(t)
		sink := pointAt(t, srv, Config{
			Token:    "{{.device_token}}",
			DataMode: DataFields,
			Title:    "t",
			Data:     map[string]string{"registered_as": "{{.device_token}}"},
		})

		msg := testMessage()
		msg.data = map[string]any{"id": "ord-4", "device_token": "dev-ada-001"}
		if err := sink.Write(context.Background(), msg); err != nil {
			t.Fatalf("Write: %v", err)
		}
		data, _ := srv.only(t)["data"].(map[string]any)
		if data["registered_as"] != "dev-ada-001" {
			t.Errorf("data[registered_as] = %v; withholding is a default, not a prohibition", data["registered_as"])
		}
	})
}
