package fcm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The refusal used to end "set on_oversize to truncate or drop, or narrow the
// payload with a transformer". on_oversize is a key name the form never shows,
// and nothing said which value was too big — so the operator who met it had a
// number, a limit and no idea which of forty fields to reach for.
func TestOversizeErrorNamesTheCulpritAndTheFix(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Topic: "orders"})
	msg := testMessage()
	msg.payload = []byte(`"` + strings.Repeat("x", 5000) + `"`)

	err := sink.Write(context.Background(), msg)
	if err == nil {
		t.Fatal("Write of an oversized payload = nil error")
	}
	for _, want := range []string{
		`"payload"`,                // the value that is too big
		"5009 bytes",               // and what it costs: the key and its value
		"Data sent to the app",     // where in the form to send less
		"If the data does not fit", // the field's label
		"on_oversize",              // and its key, for a sink saved through the API
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s:\n%s", want, err)
		}
	}
}

func TestPreviewShowsTheMessageWithoutCredentials(t *testing.T) {
	// No credentials: a preview renders, it does not authenticate, and the
	// editor should not have to send a private key to ask what a title looks like.
	got, err := PreviewMessage(Config{
		Token:    "{{.device}}",
		Title:    "Order {{.id}}",
		Body:     "{{.customer}} — {{.total}}",
		DataMode: DataNone,
		Data:     map[string]string{"deeplink": "app://orders/{{.id}}"},
	}, withDevice(testMessage(), "tok-a, tok-b"))
	if err != nil {
		t.Fatalf("PreviewMessage: %v", err)
	}
	if got.Refused != "" {
		t.Fatalf("a message that fits was refused: %s", got.Refused)
	}
	if got.Recipients != 2 {
		t.Errorf("recipients = %d, want the 2 tokens the column holds", got.Recipients)
	}

	wire := wireJSON(t, got)
	if wire["token"] != "tok-a" {
		t.Errorf("token = %v, want the first recipient", wire["token"])
	}
	notification, _ := wire["notification"].(map[string]any)
	if notification["title"] != "Order ord-1" || notification["body"] != "Ada — 42.00" {
		t.Errorf("notification = %v", notification)
	}
	data, _ := wire["data"].(map[string]any)
	if len(data) != 1 || data["deeplink"] != "app://orders/ord-1" {
		t.Errorf("data = %v, want only the listed value", data)
	}

	want := len("deeplink") + len("app://orders/ord-1")
	if got.DataBytes != want || got.SentDataBytes != want {
		t.Errorf("data bytes = %d, sent = %d, want %d for both", got.DataBytes, got.SentDataBytes, want)
	}
	if got.Limit != defaultMaxDataBytes {
		t.Errorf("limit = %d, want FCM's %d", got.Limit, defaultMaxDataBytes)
	}
}

// The question the preview exists to answer before a run does: does this row
// fit, and if not, what is taking the room.
func TestPreviewOfAnOversizedRow(t *testing.T) {
	big := []byte(`"` + strings.Repeat("x", 5000) + `"`)

	t.Run("the default policy refuses it and says what is big", func(t *testing.T) {
		msg := testMessage()
		msg.payload = big
		got, err := PreviewMessage(Config{Topic: "orders"}, msg)
		if err != nil {
			t.Fatalf("PreviewMessage: %v", err)
		}
		if got.Refused == "" {
			t.Fatal("an oversized row previewed as deliverable; the run would dead-letter it")
		}
		if strings.Contains(got.Refused, ErrPermanent.Error()) {
			t.Errorf("the refusal carries the retry classification, which means nothing to the reader: %s", got.Refused)
		}
		if got.Message != nil {
			t.Error("a refused message was still returned as what would be sent")
		}
		if got.DataBytes <= got.Limit {
			t.Errorf("data bytes = %d against a limit of %d; the size is the whole point", got.DataBytes, got.Limit)
		}
		if len(got.Largest) == 0 || got.Largest[0].Key != "payload" || got.Largest[0].Bytes != len("payload")+len(big) {
			t.Errorf("largest = %+v, want payload first with its size", got.Largest)
		}
	})

	t.Run("truncate reports both the size it was and the size it went as", func(t *testing.T) {
		msg := testMessage()
		msg.payload = big
		got, err := PreviewMessage(Config{Topic: "orders", OnOversize: OversizeTruncate}, msg)
		if err != nil {
			t.Fatalf("PreviewMessage: %v", err)
		}
		if got.Refused != "" {
			t.Fatalf("refused under truncate: %s", got.Refused)
		}
		if got.DataBytes <= got.Limit {
			t.Errorf("data bytes = %d; the original size was lost", got.DataBytes)
		}
		if got.SentDataBytes > got.Limit || got.SentDataBytes == 0 {
			t.Errorf("sent data bytes = %d against a limit of %d", got.SentDataBytes, got.Limit)
		}
	})

	t.Run("drop sends the notification with no data", func(t *testing.T) {
		msg := testMessage()
		msg.payload = big
		got, err := PreviewMessage(Config{Topic: "orders", OnOversize: OversizeDrop, Title: "t"}, msg)
		if err != nil {
			t.Fatalf("PreviewMessage: %v", err)
		}
		if got.Refused != "" || got.SentDataBytes != 0 {
			t.Errorf("refused = %q, sent data bytes = %d; want a notification with no data", got.Refused, got.SentDataBytes)
		}
		if _, present := wireJSON(t, got)["data"]; present {
			t.Error("data survived under the drop policy")
		}
	})
}

// A template naming a column the row does not have fails the message at run
// time. The preview says so against the sample, which is when it is cheap.
func TestPreviewReportsAMissingColumn(t *testing.T) {
	got, err := PreviewMessage(Config{Token: "{{.no_such_column}}", Title: "t"}, testMessage())
	if err != nil {
		t.Fatalf("PreviewMessage: %v", err)
	}
	if !strings.Contains(got.Refused, "no_such_column") {
		t.Errorf("refusal %q does not name the missing column", got.Refused)
	}
}

// What cannot be previewed is a configuration error, not a refused message:
// no row would fare differently.
func TestPreviewRefusesAConfigurationItCannotBuild(t *testing.T) {
	cases := map[string]Config{
		"a template that does not parse": {Topic: "orders", Title: "{{.id"},
		"two destinations":               {Topic: "orders", Token: "tok"},
		"an action that sends nothing":   {Action: ActionSubscribe, Topic: "orders", Token: "tok"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := PreviewMessage(cfg, testMessage()); err == nil {
				t.Error("PreviewMessage = nil error")
			}
		})
	}
}

func withDevice(msg *mockMessage, tokens string) *mockMessage {
	msg.data["device"] = tokens
	return msg
}

// wireJSON is the previewed message as FCM would receive it.
func wireJSON(t *testing.T, p Preview) map[string]any {
	t.Helper()
	if p.Message == nil {
		t.Fatal("the preview carries no message")
	}
	encoded, err := json.Marshal(p.Message)
	if err != nil {
		t.Fatalf("encoding the previewed message: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decoding the previewed message: %v", err)
	}
	return out
}
