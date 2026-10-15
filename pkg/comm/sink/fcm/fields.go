// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package fcm

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gsoultan/hermod"
)

// fieldLabels is what the editor calls each setting, by the key it is stored
// under. A refusal names a setting by this label so the reader can find it on
// the form; the key follows in brackets for whoever reads the stored config.
//
// The labels are the form's own (FcmSinkConfig.tsx, FcmDataSection.tsx and
// FcmPlatformOptions.tsx), with the folded section a setting lives in first.
var fieldLabels = map[string]string{
	"device_token": "Device token",
	"topic":        "Topic name",
	"condition":    "Condition",
	"title":        "Title",
	"body":         "Body",
	"image_url":    "Image URL",
	"data_mode":    "App data",
	"data_json":    "App data",

	"android_priority":                "Android options → Priority",
	"android_ttl":                     "Android options → Time to live",
	"android_collapse_key":            "Android options → Collapse key",
	"android_restricted_package_name": "Android options → Restricted package name",
	"android_channel_id":              "Android options → Notification channel",
	"android_sound":                   "Android options → Sound",
	"android_icon":                    "Android options → Icon",
	"android_color":                   "Android options → Colour",
	"android_tag":                     "Android options → Tag",
	"android_click_action":            "Android options → Click action",
	"android_notification_priority":   "Android options → Notification priority",

	"apns_priority":          "Apple (APNs) options → Priority",
	"apns_expiration":        "Apple (APNs) options → Expiration",
	"apns_collapse_id":       "Apple (APNs) options → Collapse ID",
	"apns_sound":             "Apple (APNs) options → Sound",
	"apns_badge":             "Apple (APNs) options → Badge",
	"apns_category":          "Apple (APNs) options → Category",
	"apns_thread_id":         "Apple (APNs) options → Thread ID",
	"apns_content_available": "Apple (APNs) options → Background push",
	"apns_mutable_content":   "Apple (APNs) options → Allow the app to rewrite the alert",

	"webpush_link":  "Web push options → Link",
	"webpush_ttl":   "Web push options → Time to live",
	"webpush_icon":  "Web push options → Icon URL",
	"webpush_badge": "Web push options → Badge URL",

	"analytics_label": "Delivery options → Analytics label",
}

// labelOf is the form's name for a setting, or the key itself for one the form
// does not show.
func labelOf(key string) string {
	if label, ok := fieldLabels[key]; ok {
		return label
	}
	return key
}

// FieldError is a refusal about one setting of the sink, said the way the form
// says it: which setting, what this row put in it and where that came from,
// what is wrong with it and what to change.
//
// It exists because the refusals people actually met were FCM's own one-liners
// — "Request contains an invalid argument" — which name neither the setting
// nor the row, so the only way to find the cause was to guess.
//
// Every FieldError is permanent: a setting the row or the form gets wrong is
// wrong on every retry, so the engine dead-letters the message at once.
type FieldError struct {
	// Key is the configuration key, which is also how the editor finds the
	// field to put the message under. Empty when the cause could not be tied
	// to one setting.
	Key string
	// MessageID is the message refused. Empty for a refusal at save time.
	MessageID string
	// Template is the setting as configured, when the value came from one.
	Template string
	// Value is what this row put in the setting, when that is the problem.
	Value    string
	HasValue bool
	// Problem says what is wrong, Fix what to change.
	Problem string
	Fix     string
	// Err is FCM's own refusal, kept for its words and for errors.As.
	Err error
}

// maxShownValue bounds a value quoted back in a refusal. A registration token
// is a capability and a row value can be a whole document; the start of either
// is enough to recognise it.
const maxShownValue = 60

func (e *FieldError) Error() string {
	var b strings.Builder
	b.WriteString("fcm sink: ")
	switch {
	case e.Key != "" && e.MessageID != "":
		fmt.Fprintf(&b, "message %s: %s (%s) ", e.MessageID, labelOf(e.Key), e.Key)
	case e.Key != "":
		fmt.Fprintf(&b, "%s (%s) ", labelOf(e.Key), e.Key)
	case e.MessageID != "":
		fmt.Fprintf(&b, "message %s ", e.MessageID)
	default:
		b.WriteString("the message ")
	}
	b.WriteString(e.Problem)
	if e.HasValue {
		fmt.Fprintf(&b, ". This row gave %q", shorten(e.Value))
		if e.Template != "" && e.Template != e.Value {
			fmt.Fprintf(&b, " from %s", e.Template)
		}
	} else if e.Template != "" {
		fmt.Fprintf(&b, ". The setting is %s", e.Template)
	}
	if e.Fix != "" {
		b.WriteString(". To fix: ")
		b.WriteString(e.Fix)
	}
	if e.Err != nil {
		fmt.Fprintf(&b, ". FCM said: %v", e.Err)
	}
	return b.String()
}

func (e *FieldError) Unwrap() error { return e.Err }

// Is makes every FieldError permanent to the retry machinery.
func (e *FieldError) Is(target error) bool {
	return target == ErrPermanent || target == hermod.ErrPermanent
}

func shorten(s string) string {
	if len(s) <= maxShownValue {
		return s
	}
	cut := maxShownValue
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// rowKeys lists the fields a template can read, for a refusal about one it
// read and did not find. Bounded: a wide row listed back in full buries the
// sentence it is attached to.
func rowKeys(data map[string]any) string {
	const most = 20
	keys := make([]string, 0, len(data))
	for k := range data {
		switch k {
		case "metadata", "meta", "after", "before":
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) > most {
		return strings.Join(keys[:most], ", ") + fmt.Sprintf(" and %d more", len(keys)-most)
	}
	return strings.Join(keys, ", ")
}
