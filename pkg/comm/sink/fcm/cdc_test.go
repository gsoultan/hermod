package fcm

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// cdcMessage is a change event built the way pkg/comm/source/postgres builds
// one: the after-image is the payload, the before-image is raw bytes, and
// nothing is put in the data map by hand. The data map a sink then reads is
// the one the message hydrates from that payload.
//
// The mock in fcm_test.go cannot stand in for this: its Before() is always nil
// and its data map is whatever the test wrote, which is how every template test
// passed while a real update or delete failed.
func cdcMessage(t *testing.T, op hermod.Operation, before, after string) hermod.Message {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(func() { message.ReleaseMessage(msg) })
	msg.SetID("0/16B3748")
	msg.SetOperation(op)
	msg.SetTable("devices")
	msg.SetSchema("public")
	msg.SetMetadata("source", "postgres")
	if before != "" {
		msg.SetBefore([]byte(before))
	}
	if after != "" {
		msg.SetAfter([]byte(after))
	}
	return msg
}

// The editor offers a CDC row's columns as `after.<column>` and
// `before.<column>`, because that is the shape of the sample it captured. The
// sink used to render templates over the data map alone, which has neither key,
// so every field the picker inserted from a change event failed every message
// with "map has no entry for key after".
func TestTemplatesReadTheChangeEventsImages(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{
		Token: "{{.after.fcm_token}}",
		Title: "{{.before.status}} → {{.after.status}}",
		Body:  "from {{.meta.source}}",
	})

	msg := cdcMessage(t, hermod.OpUpdate,
		`{"fcm_token":"tok-1","status":"paid"}`,
		`{"fcm_token":"tok-1","status":"shipped"}`)
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := srv.only(t)
	if got["token"] != "tok-1" {
		t.Errorf("token = %v, want tok-1", got["token"])
	}
	notification, _ := got["notification"].(map[string]any)
	if notification["title"] != "paid → shipped" {
		t.Errorf("title = %v, want %q", notification["title"], "paid → shipped")
	}
	if notification["body"] != "from postgres" {
		t.Errorf("body = %v, want %q", notification["body"], "from postgres")
	}
}

// A delete carries its row only as a before-image (postgres.go handleDelete
// calls SetBefore and nothing else), so its data map is empty. The form
// describes unsubscribing on a delete as the reason the action exists, and
// `{{.fcm_token}}` refused exactly that message.
func TestADeleteIsAddressedFromItsBeforeImage(t *testing.T) {
	sink := newTestSink(t, Config{Action: ActionUnsubscribe, Topic: "orders", Token: "{{.fcm_token}}"})
	fake := &recordingTopicClient{}
	sink.setClientForTest(fake)

	msg := cdcMessage(t, hermod.OpDelete, `{"id":7,"fcm_token":"tok-gone"}`, "")
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(fake.unsubscribed) != 1 || fake.unsubscribed[0] != "tok-gone" {
		t.Errorf("unsubscribed = %v, want [tok-gone]", fake.unsubscribed)
	}
}

// The same delete sent as a message: the device is told which row went, in the
// title and in the data, rather than receiving a push about nothing.
func TestADeleteSendsTheRowItRemoved(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{
		Token:    "{{.fcm_token}}",
		Title:    "Order {{.id}} cancelled",
		DataMode: DataFields,
	})

	msg := cdcMessage(t, hermod.OpDelete, `{"id":7,"fcm_token":"tok-1","sku":"A-17"}`, "")
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := srv.only(t)
	notification, _ := got["notification"].(map[string]any)
	if notification["title"] != "Order 7 cancelled" {
		t.Errorf("title = %v, want the before-image's id, not the LSN", notification["title"])
	}
	data, _ := got["data"].(map[string]any)
	if data["sku"] != "A-17" {
		t.Errorf("data.sku = %v, want A-17 from the before-image", data["sku"])
	}
	if data["operation"] != "delete" {
		t.Errorf("data.operation = %v, want delete", data["operation"])
	}
	if _, leaked := data["fcm_token"]; leaked {
		t.Error("the destination column was sent to the device it addresses")
	}
}

// Addressing by `{{.after.fcm_token}}` names the fcm_token column just as
// `{{.fcm_token}}` does. Withholding was keyed on the template's first word,
// which here is "after" — no column at all — so the token went out in the data.
func TestAnEnvelopePathStillWithholdsTheDestinationColumn(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Token: "{{.after.fcm_token}}", DataMode: DataFields})

	msg := cdcMessage(t, hermod.OpCreate, "", `{"fcm_token":"tok-1","sku":"A-17"}`)
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, _ := srv.only(t)["data"].(map[string]any)
	if _, leaked := data["fcm_token"]; leaked {
		t.Errorf("data = %v: the destination column was sent to the device it addresses", data)
	}
	if data["sku"] != "A-17" {
		t.Errorf("data.sku = %v, want A-17", data["sku"])
	}
}
