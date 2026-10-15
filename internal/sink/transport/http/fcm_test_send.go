// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
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
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sinkfcm "github.com/gsoultan/hermod/pkg/comm/sink/fcm"
)

// fcmTestSendTimeout bounds a test send: building, authenticating and one
// call to FCM.
const fcmTestSendTimeout = 20 * time.Second

// fcmTestSendError is a refused test send, with the setting it is about so
// the editor can say it under that field.
type fcmTestSendError struct {
	Error string `json:"error"`
	Field string `json:"field,omitempty"`
	Fix   string `json:"fix,omitempty"`
}

// SendFcmTestMessage builds the message a sample row becomes and really sends
// it, to the device named in the request or to the row's own destination.
//
// Test connection only proves the credentials; it passed for sinks whose every
// real message FCM refused. This goes through the sink a run would build —
// secrets resolved the same way — so a success means a device should show the
// notification, and a refusal is the one the workflow would have met.
func (h *SinkHandler) SendFcmTestMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type   string           `json:"type"`
		VHost  string           `json:"vhost"`
		Config hermod.StringMap `json:"config"`
		Sample map[string]any   `json:"sample"`
		// Device is a registration token to send to instead of the row's.
		Device string `json:"device"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFcmPreviewBytes)).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Type != "fcm" {
		h.JsonError(w, "a test message can only be sent through a Firebase (FCM) sink", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), fcmTestSendTimeout)
	defer cancel()

	// Secrets resolve exactly as they do when a run builds the sink.
	resolved, err := h.Registry.ResolveSecrets(ctx, req.VHost, req.Config)
	if err != nil {
		writeFcmTestError(w, err)
		return
	}
	snk, err := factory.CreateFCMSink(factory.SinkConfig{Type: req.Type, VHost: req.VHost, Config: resolved})
	if err != nil {
		writeFcmTestError(w, err)
		return
	}
	defer func() { _ = snk.Close() }()

	_, msg := fcmSampleMessage(req.Sample)
	defer message.ReleaseMessage(msg)

	result, err := snk.SendTest(ctx, msg, strings.TrimSpace(req.Device))
	if err != nil {
		writeFcmTestError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func writeFcmTestError(w http.ResponseWriter, err error) {
	body := fcmTestSendError{Error: strings.TrimPrefix(err.Error(), sinkfcm.ErrPermanent.Error()+": ")}
	var fe *sinkfcm.FieldError
	if errors.As(err, &fe) {
		body.Field, body.Fix = fe.Key, fe.Fix
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(body)
}
