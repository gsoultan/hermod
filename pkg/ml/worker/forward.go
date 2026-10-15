// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package worker

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gsoultan/hermod/pkg/infra/httpclient"
)

// Remote is the hermod-ml worker the client reaches over HTTP: the client
// itself for a worker, the worker an in-process engine routes to when it
// routes to one (it has a Remote method, as the built-in engine's router
// does), else nil.
func (c *Client) Remote() *Client {
	if c.local == nil {
		return c
	}
	if r, ok := c.local.(interface{ Remote() *Client }); ok {
		return r.Remote()
	}
	return nil
}

// Forward sends a request for one of the worker's routes on to the worker,
// with the worker's token in place of any the request carried, and writes
// the worker's reply to w as it came. The request's context bounds the call.
// A worker that does not answer is a 502.
func (c *Client) Forward(w http.ResponseWriter, r *http.Request) {
	target := c.URL + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body) //nolint:gosec // G704: the worker's own address, which the administrator configured.
	if err != nil {
		forwardFailed(w, err)
		return
	}
	req.ContentLength = r.ContentLength
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http.Do(req) //nolint:gosec // G704: the worker's own address, which the administrator configured.
	if err != nil {
		forwardFailed(w, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for _, h := range []string{"Content-Type", "Content-Disposition", "X-Content-Type-Options"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func forwardFailed(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "calling the ML worker: " + httpclient.RedactURLError(err).Error(),
	})
}
