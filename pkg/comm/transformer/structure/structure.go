// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

// Package structure holds the transformers that change the shape of a record
// rather than its values: flatten and unflatten, parse_field, template_render
// and field_diff.
package structure

import (
	"maps"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// configInt reads a whole number the editor saves as text and an imported
// bundle as a number. Zero or less, or anything unreadable, is def.
func configInt(config map[string]any, key string, def int) int {
	n, ok := evaluator.ToInt64(config[key])
	if !ok || n <= 0 {
		return def
	}
	return int(n)
}

// configString reads a string setting, trimmed, falling back to def when it is
// empty.
func configString(config map[string]any, key, def string) string {
	if s := strings.TrimSpace(core.GetConfigString(config, key)); s != "" {
		return s
	}
	return def
}

// payloadInvalidator is DefaultMessage's way of dropping its marshalled
// payload. It is not on hermod.Message, so it is asked for.
type payloadInvalidator interface{ ClearCachedPayload() }

// replaceData makes data the whole of the message's data map.
//
// The writes go straight into the map rather than through SetData, because
// SetData reads a dot in a key as a path and nests it: flatten with "." as its
// separator would be undone by the very call that stored its result. The CDC
// envelope (operation, before-image, metadata) is left alone, which
// ClearPayloads would not do.
func replaceData(msg hermod.Message, data map[string]any) {
	ref := msg.DataRef()
	if ref == nil {
		for k, v := range data {
			msg.SetData(k, v)
		}
		return
	}
	clear(ref)
	maps.Copy(ref, data)
	if p, ok := msg.(payloadInvalidator); ok {
		p.ClearCachedPayload()
	}
}
