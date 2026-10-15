// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package logic

import (
	"context"
	"fmt"
	"maps"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

// MulticastTransformer explicitly clones the current message into an array under _fanout
// with optional shallow projections per branch.
// Config:
// - branches: [ { select: ["a","b"], prefix: "x_" }, { } ]
//   - select: optional list of keys to keep
//   - prefix: optional prefix to add to keys
//
// Result is written to targetField (default: _fanout)
func init() { transformer.Register("multicast", &MulticastTransformer{}) }

type MulticastTransformer struct{}

func (t *MulticastTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	branches, _ := config["branches"].([]any)
	if len(branches) == 0 {
		// default: clone once
		branches = []any{map[string]any{}}
	}
	targetField, _ := config["targetField"].(string)
	if targetField == "" {
		targetField = "_fanout"
	}

	src := msg.Data()
	out := make([]any, 0, len(branches))
	for _, br := range branches {
		bm, _ := br.(map[string]any)
		// Selection
		selected := map[string]any{}
		if sel, ok := bm["select"].([]any); ok && len(sel) > 0 {
			for _, k := range sel {
				ks := toString(k)
				if v, ok := src[ks]; ok {
					selected[ks] = v
				}
			}
		} else {
			maps.Copy(selected, src)
		}
		// Prefix
		if pfx, _ := bm["prefix"].(string); pfx != "" {
			withPfx := make(map[string]any, len(selected))
			for k, v := range selected {
				withPfx[pfx+k] = v
			}
			selected = withPfx
		}
		out = append(out, selected)
	}
	msg.SetData(targetField, out)
	return msg, nil
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
