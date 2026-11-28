// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package llm

import "github.com/gsoultan/hermod"

// noRoom reports whether the provider answered that it is up and has no room
// for this request: rate limited (429), unavailable (503) or, Anthropic's own
// code, overloaded (529) -- unless its code says waiting will not help.
func (e *APIError) noRoom() bool {
	return (e.Status == 429 || e.Status == 503 || e.Status == 529) && !lasting(e.Code)
}

// Is matches hermod.ErrBusy for a no-room answer, so a workflow step that
// meets one -- after WithRetry has given up -- is waited out by the engine
// rather than failing its message.
func (e *APIError) Is(target error) bool {
	return target == hermod.ErrBusy && e.noRoom()
}

// As lets hermod.RetryAfter read the provider's Retry-After from a no-room
// answer, through the *hermod.Busy it looks for.
func (e *APIError) As(target any) bool {
	b, ok := target.(**hermod.Busy)
	if !ok || !e.noRoom() {
		return false
	}
	*b = &hermod.Busy{Err: e, RetryAfter: e.RetryAfter}
	return true
}

// OutOfQuota reports whether an error body says the account has run out of
// credit (OpenAI's insufficient_quota), which arrives as a 429 like a rate
// limit but does not end by waiting. For callers that read a provider's error
// body themselves rather than through PostJSON.
func OutOfQuota(body []byte) bool {
	return lasting(errorCode(body))
}
