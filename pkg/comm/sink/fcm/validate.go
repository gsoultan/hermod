// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package fcm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"firebase.google.com/go/v4/errorutils"
	"firebase.google.com/go/v4/messaging"
)

// The rules below are FCM's own, as its v1 API states them
// (google.golang.org/api/fcm/v1) and as the Admin SDK enforces them
// (firebase.google.com/go/v4/messaging/messaging_utils.go). They are checked
// here, before a send, so a message FCM would refuse is refused with the
// setting and the value named, instead of by FCM with one generic sentence.
var (
	topicPattern = regexp.MustCompile(`^[a-zA-Z0-9\-_.~%]+$`)
	colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// maxAndroidTTL is the longest FCM keeps a message for an offline device.
const maxAndroidTTL = 4 * 7 * 24 * time.Hour

// reservedDataKey reports whether FCM refuses key in the data map: "from",
// "message_type", and anything starting with "google." or "gcm.notification.".
func reservedDataKey(key string) bool {
	return key == "from" || key == "message_type" ||
		strings.HasPrefix(key, "google.") || strings.HasPrefix(key, "gcm.notification.")
}

const topicChars = "letters, digits and - _ . ~ %"

// validateStatic refuses, at save time, the settings that do not depend on the
// row and that FCM would refuse on every message.
func validateStatic(cfg Config) error {
	if c := cfg.Android.Color; c != "" && !colorPattern.MatchString(c) {
		return &FieldError{Key: "android_color", Value: c, HasValue: true,
			Problem: "is not a colour FCM accepts",
			Fix:     "write it as #rrggbb, for example #ff6600"}
	}
	if cfg.Android.TTL > maxAndroidTTL {
		return &FieldError{Key: "android_ttl", Value: cfg.Android.TTL.String(), HasValue: true,
			Problem: "is longer than FCM keeps a message",
			Fix:     "use at most 4 weeks (672h), or leave it empty for FCM's default of 4 weeks"}
	}
	if t := cfg.Topic; t != "" && !strings.Contains(t, "{{") {
		if err := checkTopic(t, ""); err != nil {
			return err
		}
	}
	return nil
}

func checkTopic(topic, template string) error {
	if topicPattern.MatchString(strings.TrimPrefix(topic, "/topics/")) {
		return nil
	}
	return &FieldError{Key: "topic", Template: template, Value: topic, HasValue: true,
		Problem: "is not a topic name FCM accepts",
		Fix:     "a topic name may contain only " + topicChars + "; no spaces or slashes"}
}

// validate checks a built message against FCM's rules, so that what FCM would
// refuse is refused here with the setting named.
func (s *Sink) validate(m *messaging.Message) error {
	if m.Topic != "" {
		if err := checkTopic(m.Topic, s.topicTemplate()); err != nil {
			return err
		}
	}
	if m.Notification != nil && m.Notification.ImageURL != "" {
		if !isURL(m.Notification.ImageURL, "http", "https") {
			return &FieldError{Key: "image_url", Template: s.cfg.ImageURL,
				Value: m.Notification.ImageURL, HasValue: true,
				Problem: "is not a full web address",
				Fix:     "the image must be an absolute URL such as https://cdn.example.com/item.png"}
		}
	}
	if m.Webpush != nil && m.Webpush.FCMOptions != nil && m.Webpush.FCMOptions.Link != "" {
		if !isURL(m.Webpush.FCMOptions.Link, "https") {
			return &FieldError{Key: "webpush_link", Template: s.cfg.Webpush.Link,
				Value: m.Webpush.FCMOptions.Link, HasValue: true,
				Problem: "is not an HTTPS address",
				Fix:     "FCM requires HTTPS for a web push link: start it with https://"}
		}
	}
	if err := s.checkDataKeys(m.Data); err != nil {
		return err
	}
	if m.Notification == nil && s.cfg.APNS.Priority == "10" {
		return &FieldError{Key: "apns_priority", Value: "10", HasValue: true,
			Problem: "is 10, and FCM accepts only 5 for a data-only push to Apple devices",
			Fix:     "set the priority to 5, or give the message a Title or Body so it is not data-only"}
	}
	return nil
}

func (s *Sink) topicTemplate() string {
	if strings.Contains(s.cfg.Topic, "{{") {
		return s.cfg.Topic
	}
	return ""
}

// checkDataKeys refuses a data key FCM reserves, naming where it came from: a
// key the operator added, or a column "All fields" turned into a key.
func (s *Sink) checkDataKeys(data map[string]string) error {
	for key := range data {
		if !reservedDataKey(key) {
			continue
		}
		if _, added := s.cfg.Data[key]; added {
			return &FieldError{Key: "data_json",
				Problem: fmt.Sprintf("has the key %q, a name FCM keeps reserved for itself", key),
				Fix:     `rename the key; reserved are "from", "message_type" and anything starting with "google." or "gcm.notification."`}
		}
		return &FieldError{Key: "data_mode",
			Problem: fmt.Sprintf("sends the column %q as a data key, and that name is reserved by FCM", key),
			Fix: fmt.Sprintf(`choose "Selected fields" and send the column under another name, such as %q, or rename the column before this sink`,
				strings.ReplaceAll(strings.TrimPrefix(strings.TrimPrefix(key, "google."), "gcm.notification."), ".", "_")+"_value")}
	}
	return nil
}

func isURL(raw string, schemes ...string) bool {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Host == "" {
		return false
	}
	for _, s := range schemes {
		if strings.EqualFold(u.Scheme, s) {
			return true
		}
	}
	return false
}

// fieldViolation is one entry of the google.rpc.BadRequest detail FCM attaches
// to an INVALID_ARGUMENT when it can say which field it objects to. The SDK
// does not surface it, so it is read back out of the response body.
type fieldViolation struct {
	Field       string `json:"field"`
	Description string `json:"description"`
}

func violations(err error) []fieldViolation {
	resp := errorutils.HTTPResponse(err)
	if resp == nil || resp.Body == nil {
		return nil
	}
	// The SDK hands back a copy over a buffer it already read; closing it
	// releases nothing, but leaves no body unclosed for whoever reads next.
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil
	}
	var parsed struct {
		Error struct {
			Details []struct {
				Type            string           `json:"@type"`
				FieldViolations []fieldViolation `json:"fieldViolations"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return nil
	}
	var out []fieldViolation
	for _, d := range parsed.Error.Details {
		if strings.HasSuffix(d.Type, "google.rpc.BadRequest") {
			out = append(out, d.FieldViolations...)
		}
	}
	return out
}

// wireFields maps where FCM says a problem is, in its message's JSON, to the
// setting that put it there. Longest prefix wins.
var wireFields = []struct{ path, key string }{
	{"message.token", "device_token"},
	{"message.topic", "topic"},
	{"message.condition", "condition"},
	{"message.notification.title", "title"},
	{"message.notification.body", "body"},
	{"message.notification.image", "image_url"},
	{"message.data", "data_json"},
	{"message.android.ttl", "android_ttl"},
	{"message.android.priority", "android_priority"},
	{"message.android.collapse_key", "android_collapse_key"},
	{"message.android.restricted_package_name", "android_restricted_package_name"},
	{"message.android.notification.channel_id", "android_channel_id"},
	{"message.android.notification.sound", "android_sound"},
	{"message.android.notification.icon", "android_icon"},
	{"message.android.notification.color", "android_color"},
	{"message.android.notification.tag", "android_tag"},
	{"message.android.notification.click_action", "android_click_action"},
	{"message.android.notification.notification_priority", "android_notification_priority"},
	{"message.apns.payload.aps.badge", "apns_badge"},
	{"message.apns.payload.aps.sound", "apns_sound"},
	{"message.apns.payload.aps.category", "apns_category"},
	{"message.apns.payload.aps.thread-id", "apns_thread_id"},
	{"message.webpush.fcm_options.link", "webpush_link"},
	{"message.webpush.headers", "webpush_ttl"},
	{"message.fcm_options.analytics_label", "analytics_label"},
}

func keyForPath(path string) string {
	// FCM's paths use either spelling: fcm_options or fcmOptions.
	path = camelToSnake(path)
	best, bestLen := "", 0
	for _, w := range wireFields {
		if strings.HasPrefix(path, w.path) && len(w.path) > bestLen {
			best, bestLen = w.key, len(w.path)
		}
	}
	return best
}

func camelToSnake(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// settingOf is the configured setting and the value this message carries for a
// key, so a refusal can show both.
func (s *Sink) settingOf(key string, m *messaging.Message) (template, value string, has bool) {
	read, ok := settingReaders[key]
	if !ok {
		return "", "", false
	}
	return read(s.cfg, m)
}

type settingReader func(cfg Config, m *messaging.Message) (template, value string, has bool)

// settingReaders read one setting back out of a built message, by its key.
var settingReaders = map[string]settingReader{
	"device_token": func(c Config, m *messaging.Message) (string, string, bool) { return c.Token, m.Token, true },
	"topic":        func(c Config, m *messaging.Message) (string, string, bool) { return c.Topic, m.Topic, true },
	"condition":    func(c Config, m *messaging.Message) (string, string, bool) { return c.Condition, m.Condition, true },
	"title":        notificationReader(func(c Config, n *messaging.Notification) (string, string) { return c.Title, n.Title }),
	"body":         notificationReader(func(c Config, n *messaging.Notification) (string, string) { return c.Body, n.Body }),
	"image_url":    notificationReader(func(c Config, n *messaging.Notification) (string, string) { return c.ImageURL, n.ImageURL }),
	"android_sound": androidReader(func(c Config, n *messaging.AndroidNotification) (string, string) {
		return c.Android.Sound, n.Sound
	}),
	"android_icon": androidReader(func(c Config, n *messaging.AndroidNotification) (string, string) {
		return c.Android.Icon, n.Icon
	}),
	"android_color": androidReader(func(c Config, n *messaging.AndroidNotification) (string, string) {
		return c.Android.Color, n.Color
	}),
	"android_channel_id": androidReader(func(c Config, n *messaging.AndroidNotification) (string, string) {
		return c.Android.ChannelID, n.ChannelID
	}),
	"android_click_action": androidReader(func(c Config, n *messaging.AndroidNotification) (string, string) {
		return c.Android.ClickAction, n.ClickAction
	}),
	"android_tag": androidReader(func(c Config, n *messaging.AndroidNotification) (string, string) {
		return c.Android.Tag, n.Tag
	}),
	"android_collapse_key": func(c Config, m *messaging.Message) (string, string, bool) {
		if m.Android == nil {
			return "", "", false
		}
		return c.Android.CollapseKey, m.Android.CollapseKey, true
	},
	"webpush_link": func(c Config, m *messaging.Message) (string, string, bool) {
		if m.Webpush == nil || m.Webpush.FCMOptions == nil {
			return "", "", false
		}
		return c.Webpush.Link, m.Webpush.FCMOptions.Link, true
	},
}

func notificationReader(read func(Config, *messaging.Notification) (string, string)) settingReader {
	return func(c Config, m *messaging.Message) (string, string, bool) {
		if m.Notification == nil {
			return "", "", false
		}
		t, v := read(c, m.Notification)
		return t, v, true
	}
}

func androidReader(read func(Config, *messaging.AndroidNotification) (string, string)) settingReader {
	return func(c Config, m *messaging.Message) (string, string, bool) {
		if m.Android == nil || m.Android.Notification == nil {
			return "", "", false
		}
		t, v := read(c, m.Android.Notification)
		return t, v, true
	}
}

// likelyCauses is said when FCM refuses a message as invalid without naming a
// field: the mistakes that produce that refusal, most common first.
const likelyCauses = "FCM did not say which setting it means. The usual causes are: " +
	"a Device token that is not a registration token (a user id or an email instead of the token your app got from getToken()); " +
	"a Topic name with characters other than " + topicChars + "; " +
	"an Image URL or Web push link that is not a full https:// address; " +
	"a notification and data together over 4,096 bytes. " +
	"Open the sink's Preview with this row to see the message exactly as FCM receives it"

// explainInvalid turns FCM's INVALID_ARGUMENT into a refusal that names the
// setting, the value this message carried and what to change.
func (s *Sink) explainInvalid(err error, m *messaging.Message, msgID string) error {
	fe := &FieldError{MessageID: msgID, Err: err}
	vs := violations(err)
	fe.Key, fe.Problem = attribute(err, vs)

	if fe.Key == "" {
		fe.Problem = "was refused by FCM as invalid. " + likelyCauses
		if len(vs) > 0 {
			fe.Problem = fmt.Sprintf("was refused by FCM as invalid (at %s). %s", vs[0].Field, likelyCauses)
		}
		return fe
	}

	switch fe.Key {
	case "device_token":
		fe.Fix = "Device token must hold the registration token your app got from Firebase on the device " +
			"(getToken() in the Firebase SDK), stored per device. Check that the field it reads holds that token " +
			"and not a user id, a phone number or an email"
	case "data_json":
		if s.cfg.DataMode != DataNone || len(s.cfg.Data) == 0 {
			fe.Key = "data_mode"
		}
		fe.Fix = `keys must be text and must not be "from", "message_type" or start with "google." or "gcm.notification."`
	}
	fe.Template, fe.Value, fe.HasValue = s.settingOf(fe.Key, m)
	if fe.Template == fe.Value {
		fe.Template = ""
	}
	return fe
}

// attribute is the setting an FCM refusal is about, and how to say so: from
// the field FCM names when it names one, and otherwise from its words.
func attribute(err error, vs []fieldViolation) (key, problem string) {
	for _, v := range vs {
		if key := keyForPath(v.Field); key != "" {
			return key, fmt.Sprintf("was refused by FCM (at %s)", v.Field)
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "registration token") {
		return "device_token", "is not an FCM registration token"
	}
	return "", ""
}

// asFieldError reports err's FieldError, if it has one.
func asFieldError(err error) (*FieldError, bool) {
	var fe *FieldError
	ok := errors.As(err, &fe)
	return fe, ok
}
