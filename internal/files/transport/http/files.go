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
	"net/http"

	"github.com/gsoultan/hermod/internal/api/handlers"
)

type FileHandler struct {
	*handlers.Handler
}

func NewFileHandler(h *handlers.Handler) *FileHandler {
	return &FileHandler{Handler: h}
}

func (h *FileHandler) RegisterFileRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/files/upload", h.EditorOnly(h.UploadFile))
}
