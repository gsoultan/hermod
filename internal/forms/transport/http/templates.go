// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

type TemplateDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
	Data        any    `json:"data"`
}

func (h *FormHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	templatesDir := "examples/templates"
	files, err := os.ReadDir(templatesDir)
	if err != nil {
		h.JsonError(w, "Failed to read templates directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var templates []TemplateDef
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".json" {
			data, err := os.ReadFile(filepath.Join(templatesDir, f.Name()))
			if err != nil {
				continue
			}

			var template TemplateDef
			if err := json.Unmarshal(data, &template); err != nil {
				// Try to unmarshal as just the workflow data if it doesn't have the wrapper
				var workflowData any
				if err := json.Unmarshal(data, &workflowData); err == nil {
					template = TemplateDef{
						Name:        f.Name(),
						Description: "Imported from " + f.Name(),
						Icon:        "IconGitBranch",
						Color:       "blue",
						Data:        workflowData,
					}
				} else {
					continue
				}
			}
			templates = append(templates, template)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(templates)
}
