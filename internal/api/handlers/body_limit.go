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
	"net/http"
	"os"
	"strconv"
	"strings"
)

// defaultMaxRequestBodyBytes is the largest request body the API accepts
// unless HERMOD_MAX_REQUEST_BODY_BYTES says otherwise. It sits above every
// limit a handler sets for itself (predict 8 MiB, uploads and webhooks
// 10 MiB), so it only ever bounds handlers that had none.
const defaultMaxRequestBodyBytes int64 = 16 << 20

// maxRequestBodyBytes is the configured ceiling on a request body.
func maxRequestBodyBytes() int64 {
	if v, err := strconv.ParseInt(os.Getenv("HERMOD_MAX_REQUEST_BODY_BYTES"), 10, 64); err == nil && v > 0 {
		return v
	}
	return defaultMaxRequestBodyBytes
}

// bodyLimitExempt reports the routes that set a larger limit of their own:
// a dataset file upload (200 MiB, ml/transport/http/training.go).
func bodyLimitExempt(r *http.Request) bool {
	return r.Method == http.MethodPut &&
		strings.HasPrefix(r.URL.Path, "/api/vhosts/") &&
		strings.Contains(r.URL.Path, "/ml/datasets/") &&
		strings.HasSuffix(r.URL.Path, "/file")
}

// BodyLimitMiddleware caps every request body at maxRequestBodyBytes. Many
// handlers decode r.Body with no limit of their own, login and the public
// form endpoint among them; without this one request could make the server
// buffer as much as the client cared to send. A handler that wraps the body
// in a tighter http.MaxBytesReader still gets its own limit, since the
// tighter of two nested readers wins.
func (h *Handler) BodyLimitMiddleware(next http.Handler) http.Handler {
	return h.BodyLimitMiddlewareWithLimit(maxRequestBodyBytes())(next)
}

// BodyLimitMiddlewareWithLimit is BodyLimitMiddleware with an explicit limit.
func (h *Handler) BodyLimitMiddlewareWithLimit(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody && !bodyLimitExempt(r) {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
