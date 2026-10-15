// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package http

import (
	"github.com/gsoultan/hermod/internal/api/handlers"
)

type SSEHandler struct {
	*handlers.Handler
}

func NewSSEHandler(h *handlers.Handler) *SSEHandler {
	return &SSEHandler{Handler: h}
}
