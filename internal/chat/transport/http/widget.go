// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package http

import (
	_ "embed"
	"net/http"
)

// widgetJS is the embeddable chat widget. It posts to a chat source with the
// source's public widget key, which the endpoint accepts only from the
// source's allowed origins.
//
//go:embed widget.js
var widgetJS []byte

// ServeWidget serves the widget script to any page.
func (h *ChatHandler) ServeWidget(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	// Loaded by other sites' pages, including ones that require every
	// cross-origin resource to opt in.
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	_, _ = w.Write(widgetJS)
}
