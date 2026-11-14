// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package handlers

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/gsoultan/hermod/internal/storage/configsecrets"
)

// redactedValue stands in for a credential in the audit trail.
const redactedValue = "[REDACTED]"

// auditSensitiveExact are credential names configsecrets does not know,
// because they are never keys of a connector's flat configuration: request
// headers on HTTP nodes, and names written without a separator.
var auditSensitiveExact = map[string]bool{
	"authorization":       true,
	"proxy_authorization": true,
	"cookie":              true,
	"set_cookie":          true,
	"apikey":              true,
}

// redactAuditPayload returns the JSON document an audit row keeps for data,
// with every credential in it replaced by redactedValue.
//
// Credentials are encrypted in the tables that own them, but the audit trail
// is plain text that every reader of the activity log is shown, that is kept
// for the retention period and that is copied into every backup. Data that
// cannot be read as JSON is not an object with settings in it and is
// returned as it came.
func redactAuditPayload(raw []byte) []byte {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // a 64-bit id must not come back as a rounded float
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return raw
	}
	if !redactInPlace(doc) {
		return raw
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return out
}

// redactInPlace walks a decoded JSON value and reports whether it changed it.
func redactInPlace(v any) bool {
	changed := false
	switch node := v.(type) {
	case map[string]any:
		for k, val := range node {
			if isAuditSensitiveKey(k) {
				if holdsCredential(val) {
					node[k] = redactedValue
					changed = true
				}
				continue
			}
			if redactInPlace(val) {
				changed = true
			}
		}
	case []any:
		for _, item := range node {
			if redactInPlace(item) {
				changed = true
			}
		}
	}
	return changed
}

// holdsCredential reports whether the value under a credential's name is one.
// An empty value and a flag are not. A {{ }} reference names a vhost secret
// rather than holding it, and the trail is more useful for showing which
// secret was chosen.
func holdsCredential(v any) bool {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val) != "" && !strings.Contains(val, "{{")
	case map[string]any:
		return len(val) > 0
	case []any:
		return len(val) > 0
	default:
		return false
	}
}

// isAuditSensitiveKey reports whether a JSON key names a credential. Node
// settings are camelCase (apiKey) and headers are hyphenated (X-Api-Key);
// both are read as the snake_case names configsecrets judges.
func isAuditSensitiveKey(key string) bool {
	k := snakeCase(key)
	return auditSensitiveExact[k] || configsecrets.IsSensitive(k)
}

func snakeCase(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	prevLower := false
	for _, r := range s {
		switch {
		case r == '-' || r == ' ' || r == '.':
			b.WriteByte('_')
			prevLower = false
		case unicode.IsUpper(r):
			if prevLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			prevLower = false
		default:
			b.WriteRune(r)
			prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
		}
	}
	return b.String()
}
