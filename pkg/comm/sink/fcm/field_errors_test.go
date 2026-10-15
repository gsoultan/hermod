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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// rowWith is testMessage with its row replaced, for the cases that turn on one
// column's value.
func rowWith(data map[string]any) *mockMessage {
	m := testMessage()
	m.data = data
	return m
}

// mustFieldError asserts err is a permanent *FieldError about the given form
// key, and that its text carries every one of wants. It is what a reader of the
// workflow log has to go on, so the text is the contract.
func mustFieldError(t *testing.T, err error, key string, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("err = nil, want a refusal naming the field to fix")
	}
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error %q is not a *FieldError", err)
	}
	if fe.Key != key {
		t.Errorf("FieldError.Key = %q, want %q (error: %v)", fe.Key, key, err)
	}
	if !errors.Is(err, ErrPermanent) || !errors.Is(err, hermod.ErrPermanent) {
		t.Errorf("a field the row or the form gets wrong will be wrong on every retry; want permanent: %v", err)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q:\n%v", want, err)
		}
	}
}

// A nullable token column used to render as Go's "<no value>", which went to
// FCM as the registration token and came back as a bare INVALID_ARGUMENT —
// the refusal people could not act on. A row with no token is now said to be
// one, naming the column, before anything is sent.
func TestNullTokenColumnIsNamedNotSent(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Token: "{{.push_token}}", Title: "Hi"})

	err := sink.Write(context.Background(), rowWith(map[string]any{"id": "u-7", "push_token": nil}))

	mustFieldError(t, err, "device_token", "Device token", "push_token", "empty")
	if n := len(srv.calls()); n != 0 {
		t.Errorf("sent %d requests for a row with no token, want 0", n)
	}
}

// The same "<no value>" landed in what people read on their lock screen.
func TestNullColumnRendersAsNothing(t *testing.T) {
	tests := []struct {
		name, title string
	}{
		{"a field", "Order {{.note}}"},
		{"the optional-field form", `Order {{index . "missing"}}`},
		{"through the change event's image", "Order {{.after.note}}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFCMServer(t)
			sink := pointAt(t, srv, Config{Topic: "orders", Title: tt.title, DataMode: DataNone})

			if err := sink.Write(context.Background(), rowWith(map[string]any{"note": nil})); err != nil {
				t.Fatalf("Write: %v", err)
			}
			title := srv.only(t)["notification"].(map[string]any)["title"]
			if title != "Order " {
				t.Errorf("title = %q, want %q", title, "Order ")
			}
		})
	}
}

// FCM's own rule for data keys: not "from", not "message_type", nothing under
// "google." or "gcm.notification.". A table with a `from` column sent under
// "All fields" was refused whole, and nothing said which key.
func TestReservedDataKeyIsNamed(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		row    map[string]any
		key    string
		naming string
	}{
		{
			name:   "a column under All fields",
			cfg:    Config{Topic: "chat", DataMode: DataFields},
			row:    map[string]any{"from": "ada", "text": "hi"},
			key:    "data_mode",
			naming: `"from"`,
		},
		{
			name:   "a key the operator added",
			cfg:    Config{Topic: "chat", DataMode: DataNone, Data: map[string]string{"google.sent": "{{.text}}"}},
			row:    map[string]any{"text": "hi"},
			key:    "data_json",
			naming: `"google.sent"`,
		},
		{
			name:   "message_type",
			cfg:    Config{Topic: "chat", DataMode: DataNone, Data: map[string]string{"message_type": "x"}},
			row:    map[string]any{"text": "hi"},
			key:    "data_json",
			naming: `"message_type"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFCMServer(t)
			sink := pointAt(t, srv, tt.cfg)

			err := sink.Write(context.Background(), rowWith(tt.row))

			mustFieldError(t, err, tt.key, tt.naming, "reserved", "App data")
			if n := len(srv.calls()); n != 0 {
				t.Errorf("sent %d requests, want 0", n)
			}
		})
	}
}

// The SDK checks a few things itself before sending, and used to hand back
// errors such as "malformed topic name" — retryable, naming no field and no
// value. Each is now refused with the form's label and the value the row gave.
func TestRenderedValuesFCMWouldRefuse(t *testing.T) {
	tests := []struct {
		name  string
		cfg   Config
		row   map[string]any
		key   string
		wants []string
	}{
		{
			name:  "a topic with a space",
			cfg:   Config{Topic: "orders-{{.region}}", Title: "x"},
			row:   map[string]any{"region": "east coast"},
			key:   "topic",
			wants: []string{"Topic name", `"orders-east coast"`, "{{.region}}"},
		},
		{
			name:  "an image that is not a URL",
			cfg:   Config{Topic: "orders", Title: "x", ImageURL: "{{.picture}}"},
			row:   map[string]any{"picture": "cat.png"},
			key:   "image_url",
			wants: []string{"Image URL", `"cat.png"`, "https://"},
		},
		{
			name:  "a web push link over http",
			cfg:   Config{Topic: "orders", Title: "x", Webpush: WebpushConfig{Link: "http://shop/{{.id}}"}},
			row:   map[string]any{"id": "7"},
			key:   "webpush_link",
			wants: []string{"HTTPS", `"http://shop/7"`},
		},
		{
			name:  "a data-only push at APNs priority 10",
			cfg:   Config{Topic: "orders", DataMode: DataNone, Data: map[string]string{"k": "v"}, APNS: APNSConfig{Priority: "10"}},
			row:   map[string]any{"id": "7"},
			key:   "apns_priority",
			wants: []string{"5", "data-only"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFCMServer(t)
			sink := pointAt(t, srv, tt.cfg)

			err := sink.Write(context.Background(), rowWith(tt.row))

			mustFieldError(t, err, tt.key, tt.wants...)
			if n := len(srv.calls()); n != 0 {
				t.Errorf("sent %d requests, want 0", n)
			}
		})
	}
}

// Settings that do not depend on the row are refused when the sink is saved,
// not on the first message.
func TestStaticSettingsFCMWouldRefuse(t *testing.T) {
	tests := []struct {
		name  string
		cfg   Config
		key   string
		wants []string
	}{
		{
			name:  "a colour that is not #rrggbb",
			cfg:   Config{Topic: "orders", Android: AndroidConfig{Color: "red"}},
			key:   "android_color",
			wants: []string{"Colour", `"red"`, "#rrggbb"},
		},
		{
			name:  "an Android time to live over four weeks",
			cfg:   Config{Topic: "orders", Android: AndroidConfig{TTL: 30 * 24 * time.Hour}},
			key:   "android_ttl",
			wants: []string{"Time to live", "4 weeks"},
		},
		{
			name:  "a fixed topic with a slash",
			cfg:   Config{Topic: "orders/eu"},
			key:   "topic",
			wants: []string{"Topic name", `"orders/eu"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.CredentialsJSON = serviceAccountJSON
			_, err := New(tt.cfg)
			mustFieldError(t, err, tt.key, tt.wants...)
		})
	}
}

// fcmRefusal is a 400 the way FCM writes one: its own sentence, the FCM error
// code, and — when it can say — the field it objects to.
func fcmRefusal(message, field string) string {
	violations := ""
	if field != "" {
		violations = fmt.Sprintf(`,{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":%q,"description":%q}]}`, field, message)
	}
	return fmt.Sprintf(`{"error":{"code":400,"status":"INVALID_ARGUMENT","message":%q,"details":[
		{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"}%s]}}`,
		message, violations)
}

// "Request contains an invalid argument" on its own told nobody what to change.
// The refusal now says which setting FCM objected to, what this row put in it
// and where that came from, and keeps FCM's own words.
func TestInvalidArgumentNamesTheSetting(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		row     map[string]any
		message string
		field   string
		key     string
		wants   []string
	}{
		{
			name:    "a value that is not a registration token",
			cfg:     Config{Token: "{{.user_id}}", Title: "x"},
			row:     map[string]any{"user_id": "42"},
			message: "The registration token is not a valid FCM registration token",
			key:     "device_token",
			wants:   []string{"Device token", "{{.user_id}}", `"42"`, "not a valid FCM registration token", "getToken"},
		},
		{
			name:    "a field FCM names",
			cfg:     Config{Topic: "orders", Title: "x", Android: AndroidConfig{ChannelID: "alerts", Sound: "ding"}},
			row:     map[string]any{"id": "1"},
			message: "Invalid value at 'message.android.notification.sound'",
			field:   "message.android.notification.sound",
			key:     "android_sound",
			wants:   []string{"Sound", "Android", `"ding"`, "message.android.notification.sound"},
		},
		{
			name:    "a data entry FCM names",
			cfg:     Config{Topic: "orders", DataMode: DataNone, Data: map[string]string{"k": "v"}},
			row:     map[string]any{"id": "1"},
			message: "Invalid data payload",
			field:   "message.data[0].key",
			key:     "data_json",
			wants:   []string{"App data", "Invalid data payload"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFCMServer(t)
			srv.respond = func(_ int, w http.ResponseWriter) {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, fcmRefusal(tt.message, tt.field))
			}
			sink := pointAt(t, srv, tt.cfg)

			err := sink.Write(context.Background(), rowWith(tt.row))

			mustFieldError(t, err, tt.key, tt.wants...)
		})
	}
}

// When FCM names nothing the refusal still lists what usually causes it, rather
// than leaving the reader with FCM's one line.
func TestUnattributedInvalidArgumentListsLikelyCauses(t *testing.T) {
	srv := newFCMServer(t)
	srv.respond = func(_ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, fcmRefusal("Request contains an invalid argument.", ""))
	}
	sink := pointAt(t, srv, Config{Topic: "orders", Title: "x"})

	err := sink.Write(context.Background(), testMessage())
	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("want permanent: %v", err)
	}
	for _, want := range []string{"Request contains an invalid argument.", "Preview"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q:\n%v", want, err)
		}
	}
}

// The preview reports which setting a refusal is about, so the form can put the
// message under that field.
func TestPreviewNamesTheFieldToFix(t *testing.T) {
	p, err := PreviewMessage(Config{Token: "{{.push_token}}", Title: "Hi"},
		rowWith(map[string]any{"push_token": ""}))
	if err != nil {
		t.Fatalf("PreviewMessage: %v", err)
	}
	if p.RefusedField != "device_token" {
		t.Errorf("RefusedField = %q, want device_token (refused: %s)", p.RefusedField, p.Refused)
	}
	if p.RefusedLabel != "Device token" {
		t.Errorf("RefusedLabel = %q, want the form's label", p.RefusedLabel)
	}
	if !strings.Contains(p.RefusedFix, "filter") {
		t.Errorf("RefusedFix = %q, want what to change", p.RefusedFix)
	}
}

// An object or list column picked under "Selected fields" used to be rendered by
// Go's %v as map[k:v], which nothing on a handset can parse. The form writes
// such a column as {{json .column}}, and it arrives as the JSON "All fields"
// would have sent.
func TestJSONCarriesAnObjectColumn(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Topic: "orders", DataMode: DataNone,
		Data: map[string]string{"items": "{{json .items}}", "note": "{{json .note}}"}})

	row := map[string]any{"items": []any{map[string]any{"sku": "A1", "qty": 2}}, "note": nil}
	if err := sink.Write(context.Background(), rowWith(row)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data := srv.only(t)["data"].(map[string]any)
	if data["items"] != `[{"qty":2,"sku":"A1"}]` {
		t.Errorf("items = %q, want the JSON of the list", data["items"])
	}
	if _, sent := data["note"]; sent {
		t.Errorf("a null column sent as %q; want it left out like any empty value", data["note"])
	}
}

// A null column is often left out of the row entirely, so the template finds no
// such field. That is said as what it means to the reader, not as Go's
// "map has no entry for key".
func TestMissingColumnIsNamed(t *testing.T) {
	srv := newFCMServer(t)
	sink := pointAt(t, srv, Config{Token: "{{.push_token}}", Title: "Hi"})

	err := sink.Write(context.Background(), rowWith(map[string]any{"id": "u-7", "email": "a@b"}))

	mustFieldError(t, err, "device_token", `"push_token"`, "missing or null", "email, id")
	if n := len(srv.calls()); n != 0 {
		t.Errorf("sent %d requests, want 0", n)
	}
}
