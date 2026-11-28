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
	"fmt"
	"net/http"

	"github.com/gsoultan/hermod/pkg/infra/httpclient"
)

// statusError is the error for a response outside 2xx, or nil for one inside.
//
// 429 Too Many Requests and 503 Service Unavailable mean the receiver is up
// and has no room, so they are reported as busy (hermod.Busy) with whatever
// Retry-After asked for: the engine waits them out instead of spending retries
// or tripping the breaker. Every other status keeps the error it always had.
func statusError(resp *http.Response, what string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return httpclient.MarkBusy(resp, fmt.Errorf("%s: %d", what, resp.StatusCode))
}
