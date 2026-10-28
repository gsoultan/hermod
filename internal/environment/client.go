// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package environment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// The peer API: what one instance calls on another. Every path but /pair
// takes the peer's token as a bearer token.
const (
	PathPair        = "/api/environments/pair"
	PathPeerInfo    = "/api/environments/peer/info"
	PathPeerConns   = "/api/environments/peer/connections"
	PathPeerCheck   = "/api/environments/peer/check"
	PathPeerPromote = "/api/environments/peer/promotions"
	PathPeerUnpair  = "/api/environments/peer/self"
)

// PairRequest redeems a pairing code.
type PairRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// PairResult is what the receiving instance hands back for a good code.
type PairResult struct {
	PeerID   string   `json:"peer_id"`
	Token    string   `json:"token"`
	Instance Instance `json:"instance"`
	Version  string   `json:"version"`
}

// PeerInfo describes the receiving instance to a linked one.
type PeerInfo struct {
	Instance Instance `json:"instance"`
	Rules    Rules    `json:"rules"`
	Version  string   `json:"version"`
}

// Connection is a source or sink as a linked instance sees it: never its
// configuration.
type Connection struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	VHost string `json:"vhost"`
}

// Connections lists the receiving instance's sources and sinks.
type Connections struct {
	Sources []Connection `json:"sources"`
	Sinks   []Connection `json:"sinks"`
}

// CheckRequest asks the receiving instance whether a workflow would land.
type CheckRequest struct {
	WorkflowID string   `json:"workflow_id"`
	VHost      string   `json:"vhost"`
	Sources    []string `json:"sources"`
	Sinks      []string `json:"sinks"`
	Secrets    []string `json:"secrets"`
	Envs       []string `json:"envs"`
}

// CurrentWorkflow is the version of a workflow the receiving instance runs.
type CurrentWorkflow struct {
	Version  int              `json:"version"`
	Workflow storage.Workflow `json:"workflow"`
}

// CheckResult is the receiving instance's answer to a CheckRequest.
type CheckResult struct {
	MissingSources []string         `json:"missing_sources"`
	MissingSinks   []string         `json:"missing_sinks"`
	MissingSecrets []string         `json:"missing_secrets"`
	MissingEnvs    []string         `json:"missing_envs"`
	Current        *CurrentWorkflow `json:"current,omitempty"`
}

// Submission is a promotion as it is sent.
type Submission struct {
	Workflow      storage.Workflow `json:"workflow"`
	FromVersion   int              `json:"from_version"`
	FromVersionAt time.Time        `json:"from_version_at"`
	Sender        string           `json:"sender"`
	Note          string           `json:"note,omitempty"`
	Start         bool             `json:"start"`
	AutoRollback  bool             `json:"auto_rollback"`
}

// Remote is how this instance calls a linked one.
type Remote interface {
	Pair(ctx context.Context, baseURL string, req PairRequest) (PairResult, error)
	Info(ctx context.Context, baseURL, token string) (PeerInfo, error)
	Connections(ctx context.Context, baseURL, token string) (Connections, error)
	Check(ctx context.Context, baseURL, token string, req CheckRequest) (CheckResult, error)
	Submit(ctx context.Context, baseURL, token string, sub Submission) (Promotion, error)
	Unpair(ctx context.Context, baseURL, token string) error
}

// ValidateURL checks a linked instance's address and returns it without
// surrounding space or a trailing slash. It must be an absolute http or https
// URL with a host and nothing after the path: the token travels in a header.
func ValidateURL(raw string) (string, error) {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(s)
	if s == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" {
		return "", fmt.Errorf("%w: the address must start with http:// or https://, like https://hermod.example.com", ErrInvalid)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(s, "#") {
		return "", fmt.Errorf("%w: the address must not hold a user, a query or a fragment", ErrInvalid)
	}
	return s, nil
}

// RemoteError is a linked instance's refusal, with its status and message.
type RemoteError struct {
	Status  int
	Message string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("the linked instance answered %d: %s", e.Status, e.Message)
}

// maxPeerResponse bounds what is read back from a linked instance.
const maxPeerResponse = 8 << 20

// HTTPRemote is Remote over HTTP.
type HTTPRemote struct {
	Client *http.Client
}

// NewHTTPRemote builds an HTTPRemote whose every call is bounded: the
// addresses are typed by an admin and may be internal, so there is no SSRF
// guard, but nothing waits forever on an instance that stopped answering.
func NewHTTPRemote() *HTTPRemote {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &HTTPRemote{Client: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
		},
		// A redirect would carry the bearer token to wherever it points.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *HTTPRemote) do(ctx context.Context, method, baseURL, path, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxPeerResponse))
	if err != nil {
		return fmt.Errorf("reading the answer from %s: %w", baseURL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &RemoteError{Status: resp.StatusCode, Message: remoteMessage(raw)}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New("the address answered, but not as a Hermod instance does")
	}
	return nil
}

// remoteMessage reads the error field Hermod writes, or the start of
// whatever else came back.
func remoteMessage(raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func (c *HTTPRemote) Pair(ctx context.Context, baseURL string, req PairRequest) (PairResult, error) {
	var out PairResult
	err := c.do(ctx, http.MethodPost, baseURL, PathPair, "", req, &out)
	return out, err
}

func (c *HTTPRemote) Info(ctx context.Context, baseURL, token string) (PeerInfo, error) {
	var out PeerInfo
	err := c.do(ctx, http.MethodGet, baseURL, PathPeerInfo, token, nil, &out)
	return out, err
}

func (c *HTTPRemote) Connections(ctx context.Context, baseURL, token string) (Connections, error) {
	var out Connections
	err := c.do(ctx, http.MethodGet, baseURL, PathPeerConns, token, nil, &out)
	return out, err
}

func (c *HTTPRemote) Check(ctx context.Context, baseURL, token string, req CheckRequest) (CheckResult, error) {
	var out CheckResult
	err := c.do(ctx, http.MethodPost, baseURL, PathPeerCheck, token, req, &out)
	return out, err
}

func (c *HTTPRemote) Submit(ctx context.Context, baseURL, token string, sub Submission) (Promotion, error) {
	var out Promotion
	err := c.do(ctx, http.MethodPost, baseURL, PathPeerPromote, token, sub, &out)
	return out, err
}

func (c *HTTPRemote) Unpair(ctx context.Context, baseURL, token string) error {
	return c.do(ctx, http.MethodDelete, baseURL, PathPeerUnpair, token, nil, nil)
}
