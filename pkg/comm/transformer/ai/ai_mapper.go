// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package ai

import (
	"context"
	"fmt"
	"maps"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

func init() {
	transformer.Register("ai_mapper", &AIMapper{})
}

// AIMapper uses LLMs to map unstructured data to a structured target schema.
type AIMapper struct {
	ai AITransformer
}

func (t *AIMapper) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	targetSchema, _ := config["targetSchema"].(string) // JSON representation of the target schema
	hints, _ := config["hints"].(string)               // Optional hints for the AI

	// Prepare a specialized prompt for mapping
	prompt := fmt.Sprintf(`Map the following unstructured data to the target schema. 
Return ONLY a JSON object that strictly follows the schema.
Target Schema: %s
%s`, targetSchema, hints)

	// Reuse AITransformer logic
	mapperConfig := make(map[string]any)
	maps.Copy(mapperConfig, config)
	mapperConfig["prompt"] = prompt
	mapperConfig["targetField"] = "" // We want to merge the JSON result

	return t.ai.Transform(ctx, msg, mapperConfig)
}
