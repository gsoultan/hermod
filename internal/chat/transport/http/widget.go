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
