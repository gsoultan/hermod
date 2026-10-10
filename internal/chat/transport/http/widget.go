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
