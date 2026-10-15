// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package ml

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/httpclient"
)

const (
	// callbackTimeout bounds one delivery of a callback.
	callbackTimeout = 10 * time.Second
	// maxCallbackAttempts is how many times a callback is sent before it is
	// given up.
	maxCallbackAttempts = 8
	// firstCallbackRetry is the wait after the first failed delivery; it
	// doubles after each one, up to maxCallbackRetry.
	firstCallbackRetry = 30 * time.Second
	maxCallbackRetry   = time.Hour
	// callbackClaimTTL is how long a Hermod holds a callback while it sends
	// it: well over callbackTimeout.
	callbackClaimTTL = time.Minute
	// callbacksPerRound bounds the callbacks one round sends, so a backlog
	// does not keep the runner from its jobs.
	callbacksPerRound = 10
)

// The headers a callback is signed with.
const (
	CallbackTimestampHeader = "X-Hermod-Timestamp"
	CallbackSignatureHeader = "X-Hermod-Signature-256"
	CallbackJobHeader       = "X-Hermod-Job-Id"
)

// errGiveUp marks a callback that no retry can deliver.
var errGiveUp = errors.New("not retried")

// CallbackPayload is the body a job's callback posts. Result is the answer
// when it is small enough to be kept on the job; otherwise ResultURL is where
// it is read, with the same credentials as StatusURL.
type CallbackPayload struct {
	JobID      string          `json:"job_id"`
	VHost      string          `json:"vhost"`
	Model      string          `json:"model"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Error      string          `json:"error,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	StatusURL  string          `json:"status_url"`
	ResultURL  string          `json:"result_url,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

// SignCallback is the signature of a callback body sent at timestamp (Unix
// seconds): "sha256=" and the hex HMAC-SHA256, keyed with the vhost's
// ML_CALLBACK_SECRET, of the timestamp, a full stop and the body. Signing the
// timestamp lets a receiver refuse a replayed callback by its age.
func SignCallback(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// sendCallbacks sends the callbacks that are due, claimed one at a time so
// each is sent by one Hermod.
func (r *JobRunner) sendCallbacks(ctx context.Context, svc *Service) {
	js, err := svc.Jobs()
	if err != nil {
		return
	}
	for range callbacksPerRound {
		if ctx.Err() != nil {
			return
		}
		j, ok, err := js.ClaimMLJobCallback(ctx, r.owner, callbackClaimTTL, r.now())
		if err != nil || !ok {
			return
		}
		attempts := j.CallbackAttempts + 1
		err = r.deliver(ctx, svc, j)
		status, next, lastErr := storage.MLCallbackDelivered, time.Time{}, ""
		switch {
		case err == nil:
			callbacksTotal.WithLabelValues("delivered").Inc()
		case errors.Is(err, errGiveUp) || attempts >= maxCallbackAttempts:
			status, lastErr = storage.MLCallbackFailed, err.Error()
			callbacksTotal.WithLabelValues("failed").Inc()
			r.warn("Gave up sending an ML job's callback", "vhost", j.VHost, "job", j.ID, "attempts", attempts, "error", err)
		default:
			status, lastErr = storage.MLCallbackPending, err.Error()
			next = r.now().Add(min(firstCallbackRetry<<(attempts-1), maxCallbackRetry))
			callbacksTotal.WithLabelValues("retry").Inc()
		}
		if err := js.RecordMLJobCallback(context.WithoutCancel(ctx), j.ID, r.owner, status, attempts, next, lastErr); err != nil {
			r.warn("Recording an ML job's callback failed", "vhost", j.VHost, "job", j.ID, "error", err)
		}
	}
}

// deliver posts the job's callback, signed. The URL is checked again, since
// the rules may have changed since the job was queued; the client refuses a
// private address whatever the name resolves to now.
func (r *JobRunner) deliver(ctx context.Context, svc *Service, j storage.MLJob) error {
	if err := svc.checkCallback(j.CallbackURL); err != nil {
		return fmt.Errorf("%w: %w", errGiveUp, err)
	}
	secret, err := svc.callbackSecret(ctx, j.VHost)
	if err != nil {
		return fmt.Errorf("reading the callback signing secret: %w", err)
	}
	if secret == "" {
		return fmt.Errorf("%w: vhost %q no longer holds the secret %s the callback is signed with", errGiveUp, j.VHost, CallbackSecretName)
	}
	payload := CallbackPayload{
		JobID: j.ID, VHost: j.VHost, Model: j.Model, Kind: j.Kind, Status: j.Status, Error: j.Error,
		FinishedAt: j.FinishedAt, StatusURL: JobStatusPath(j), Result: j.Result,
	}
	if j.ResultRef == storage.MLJobResultStored {
		payload.ResultURL = payload.StatusURL + "/result"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: encoding the callback: %w", errGiveUp, err)
	}
	ts := r.now().Unix()
	ctx, cancel := context.WithTimeout(ctx, callbackTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.CallbackURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %w", errGiveUp, httpclient.RedactURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Hermod-ML-Callback")
	req.Header.Set(CallbackTimestampHeader, strconv.FormatInt(ts, 10))
	req.Header.Set(CallbackSignatureHeader, SignCallback(secret, ts, body))
	req.Header.Set(CallbackJobHeader, j.ID)
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending the callback: %w", httpclient.RedactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the callback URL answered %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return nil
}
