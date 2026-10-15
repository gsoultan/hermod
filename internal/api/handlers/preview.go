// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// The editor's preview and sample endpoints run a workflow, a node or a source
// on a body the browser sends. Without a cap the body is decoded whatever its
// size, and without a deadline a node stuck on an endpoint that never answers
// holds the request for as long as the node's own timeout allows.
//
// Variables rather than constants so tests can shorten them.
var (
	// PreviewMaxBodyBytes caps the request body of the editor's preview and
	// sample endpoints.
	PreviewMaxBodyBytes int64 = 4 << 20
	// PreviewTimeout bounds how long one preview or sample request may run.
	PreviewTimeout = 30 * time.Second
)

// DecodeJSONBody decodes at most limit bytes of r's body into dst. On failure
// it writes the error response itself and returns false: 413 when the body is
// over limit, 400 when it is not valid JSON, with prefix before the decoder's
// message.
func (h *Handler) DecodeJSONBody(w http.ResponseWriter, r *http.Request, limit int64, dst any, prefix string) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(dst)
	if err == nil {
		return true
	}
	if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok {
		h.JsonError(w, fmt.Sprintf("Request body exceeds the %d-byte limit", tooLarge.Limit),
			http.StatusRequestEntityTooLarge)
		return false
	}
	h.JsonError(w, prefix+err.Error(), http.StatusBadRequest)
	return false
}

// WithPreviewDeadline returns r bounded by PreviewTimeout. The caller must call
// the returned cancel.
func WithPreviewDeadline(r *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), PreviewTimeout)
	return r.WithContext(ctx), cancel
}
