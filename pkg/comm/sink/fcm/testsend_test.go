// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package fcm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Test connection proves the credentials and nothing about the message, so it
// passed for sinks whose every real message FCM refused. A test send builds the
// message from a row with the code a run uses and really delivers it, so
// "sent" means a device should show it.
func TestSendTestDeliversOneRealMessage(t *testing.T) {
	srv := newFCMServer(t)
	// Dry run is the sink's setting for runs; a test send is asked for
	// explicitly and must reach the device.
	sink := pointAt(t, srv, Config{Token: "{{.push_token}}", Title: "Order {{.id}}", DryRun: true})

	got, err := sink.SendTest(context.Background(), rowWith(map[string]any{"id": "7", "push_token": "row-device"}), "my-phone")
	if err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	calls := srv.calls()
	if len(calls) != 1 {
		t.Fatalf("got %d sends, want 1", len(calls))
	}
	if v, ok := calls[0]["validate_only"]; ok && v == true {
		t.Error("a test send was sent as validate_only; nothing would reach the device")
	}
	msg := calls[0]["message"].(map[string]any)
	if msg["token"] != "my-phone" {
		t.Errorf("token = %v, want the device the test names", msg["token"])
	}
	if title := msg["notification"].(map[string]any)["title"]; title != "Order 7" {
		t.Errorf("title = %v, want the row's", title)
	}
	if got.MessageID != "projects/demo-project/messages/0" {
		t.Errorf("MessageID = %q, want FCM's name for the message", got.MessageID)
	}
}

// With no device named, the row's own destination is used — once: a test does
// not notify every device on a multicast list.
func TestSendTestUsesTheRowsFirstDevice(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Token: "{{.tokens}}", Title: "x"})

	got, err := sink.SendTest(context.Background(), rowWith(map[string]any{"tokens": "a,b,c"}), "")
	if err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	calls := srv.calls()
	if len(calls) != 1 || calls[0]["message"].(map[string]any)["token"] != "a" {
		t.Fatalf("sends = %v, want one to the first device", calls)
	}
	if !strings.Contains(got.To, "1 device") {
		t.Errorf("To = %q, want it to say one device", got.To)
	}
}

// A named device replaces a topic too: the test is about one phone in hand.
func TestSendTestDeviceReplacesTopic(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Topic: "everyone", Title: "x"})

	if _, err := sink.SendTest(context.Background(), testMessage(), "my-phone"); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	msg := srv.only(t)
	if msg["token"] != "my-phone" || msg["topic"] != nil {
		t.Errorf("message = %v, want only the named device", msg)
	}
}

func TestSendTestExplainsARefusal(t *testing.T) {
	srv := newFCMServer(t)
	srv.respond = func(_ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, fcmRefusal("The registration token is not a valid FCM registration token", ""))
	}
	sink := pointAt(t, srv, Config{Token: "{{.user_id}}", Title: "x"})

	_, err := sink.SendTest(context.Background(), rowWith(map[string]any{"user_id": "42"}), "")

	mustFieldError(t, err, "device_token", `"42"`, "{{.user_id}}")
}

func TestSendTestRefusesTheTopicActions(t *testing.T) {
	sink := newTestSink(t, Config{Action: ActionSubscribe, Token: "{{.t}}", Topic: "news"})
	if _, err := sink.SendTest(context.Background(), testMessage(), "x"); err == nil {
		t.Fatal("a sink that moves devices between topics has no message to test")
	}
}
