// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/httpclient"
	sdk "github.com/gsoultan/metis-sdk"
)

func init() {
	transformer.Register("metis_decide", NewMetisDecideTransformer())
}

// MetisDecideTransformer is metis_decide: it runs one of a Metis project's DMN
// decision tables on the record and writes the outputs into it, through
// metis-sdk's EvaluateDecision. The business rules stay in Metis, where they are
// edited and versioned; a workflow applies them to every record in flight and
// routes on the answer.
//
// Evaluating a decision changes nothing on the server, so a retry is safe and a
// dry run or preview calls Metis like a live run does.
//
// Config:
//   - url: the Metis origin. token, or username and password: the account.
//     organizationId: only for an account in more than one organization. All
//     resolve for the record's vhost only ({{secret.NAME}}), so row data cannot
//     choose where a credential goes. Plain http is refused off loopback.
//   - projectId: the project the table belongs to. Required.
//   - decisionKey: the table's key, a template over the record. Required.
//   - version: a version to evaluate instead of the live one.
//   - inputs: [{name, field}], the table's inputs read from the record. Empty
//     sends the whole record.
//   - targetField: where the result goes, default "decision": {"values",
//     "matched", "matched_rules", "matched_rule_ids", "decision": {"key",
//     "name", "version"}}. mergeOutputs also copies each output to the top
//     level of the record.
//   - onNoMatch: "keep" (default) passes the record on with matched false,
//     "drop" drops it, "fail" fails it.
//   - timeout: how long one evaluation may take, default 10s.
//
// A setting Metis cannot work with, or a refusal from Metis (a 4xx: an unknown
// table, a missing input, bad credentials), is a permanent error: retrying the
// same record cannot fix it, so it goes to the dead-letter path at once. A
// network failure or 5xx is retried by the node's retry policy.
type MetisDecideTransformer struct {
	mu      sync.Mutex
	clients map[string]*metisClient
}

// NewMetisDecideTransformer builds the transformer with an empty client cache.
func NewMetisDecideTransformer() *MetisDecideTransformer {
	return &MetisDecideTransformer{clients: map[string]*metisClient{}}
}

const (
	defaultMetisDecideField   = "decision"
	defaultMetisDecideTimeout = 10 * time.Second

	// metisMaxClients bounds the client cache. Each entry is one server and
	// account; a rotated secret mints a new one, so the old ones are dropped.
	metisMaxClients = 32

	// metisMaxTimeout bounds the timeout a node may set: a record waits on
	// the call, and so does every record behind it.
	metisMaxTimeout = 2 * time.Minute

	// metisLoginBackoff is how long a refused sign-in is remembered. Without
	// it every record signs in again with the same wrong password, which is
	// how an account gets locked.
	metisLoginBackoff = 30 * time.Second

	// metisMaxErrorText bounds what Metis's answer adds to an error, which
	// goes to the workflow log and the dead letter.
	metisMaxErrorText = 512

	metisNoMatchKeep = "keep"
	metisNoMatchDrop = "drop"
	metisNoMatchFail = "fail"
)

// metisDecideRequest is a node's config, resolved for one record.
type metisDecideRequest struct {
	baseURL, token, username, password, orgID string
	projectID, decisionKey                    string
	version                                   int
	inputs                                    []metisInput
	targetField                               string
	mergeOutputs                              bool
	onNoMatch                                 string
	timeout                                   time.Duration
}

type metisInput struct{ name, field string }

// metisClient is one SDK client, shared by every record of the nodes that use
// the same server and account, so connections and a login token are reused.
type metisClient struct {
	client *sdk.Client

	authMu      sync.Mutex
	authed      bool
	loginErr    error
	loginFailed time.Time
}

// metisSettingsError marks a setting problem: permanent, since every retry of
// the record reads the same settings.
func metisSettingsError(format string, args ...any) error {
	return fmt.Errorf("metis_decide: "+format+": %w", append(args, hermod.ErrPermanent)...)
}

func resolveMetisDecideRequest(config map[string]any, msg hermod.Message) (metisDecideRequest, error) {
	scoped := func(key string) string {
		return strings.TrimSpace(evaluator.ResolveTemplateScoped(core.GetConfigString(config, key), msg))
	}
	req := metisDecideRequest{
		baseURL:   scoped("url"),
		token:     scoped("token"),
		username:  scoped("username"),
		password:  scoped("password"),
		orgID:     scoped("organizationId"),
		projectID: scoped("projectId"),
		// Which table decides may follow the record: it is not a credential.
		decisionKey:  strings.TrimSpace(evaluator.ResolveTemplateMsg(core.GetConfigString(config, "decisionKey"), msg)),
		targetField:  strings.TrimSpace(core.GetConfigString(config, "targetField")),
		mergeOutputs: configBool(config, "mergeOutputs"),
		onNoMatch:    strings.ToLower(strings.TrimSpace(core.GetConfigString(config, "onNoMatch"))),
		timeout:      defaultMetisDecideTimeout,
		inputs:       metisInputs(config["inputs"]),
	}
	if req.targetField == "" {
		req.targetField = defaultMetisDecideField
	}
	if err := req.checkTarget(); err != nil {
		return req, err
	}
	if err := req.readOptions(config); err != nil {
		return req, err
	}
	return req, nil
}

// checkTarget refuses a request that names no server, account, project or
// table, and plain http off loopback.
func (req metisDecideRequest) checkTarget() error {
	if req.baseURL == "" {
		return metisSettingsError("enter the Metis server's address")
	}
	if err := metisRefusePlaintext(req.baseURL); err != nil {
		return err
	}
	if req.token == "" && (req.username == "" || req.password == "") {
		return metisSettingsError("Metis needs a token, or a username and password: name the secret that holds it")
	}
	if req.projectID == "" {
		return metisSettingsError("choose the Metis project the decision belongs to")
	}
	if req.decisionKey == "" {
		return metisSettingsError("enter the decision table's key")
	}
	return nil
}

// readOptions reads the version, the no-match behaviour and the timeout.
func (req *metisDecideRequest) readOptions(config map[string]any) error {
	if s := strings.TrimSpace(core.GetConfigString(config, "version")); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			return metisSettingsError("version %q is not a version number (leave it empty for the live version)", s)
		}
		req.version = v
	}

	switch req.onNoMatch {
	case "":
		req.onNoMatch = metisNoMatchKeep
	case metisNoMatchKeep, metisNoMatchDrop, metisNoMatchFail:
	default:
		return metisSettingsError("when no rule matches is %q, %q or %q, not %q",
			metisNoMatchKeep, metisNoMatchDrop, metisNoMatchFail, req.onNoMatch)
	}

	if s := strings.TrimSpace(core.GetConfigString(config, "timeout")); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return metisSettingsError("timeout %q is not a duration (e.g. %q)", s, "10s")
		}
		if d > metisMaxTimeout {
			return metisSettingsError("timeout %q is longer than %s", s, metisMaxTimeout)
		}
		req.timeout = d
	}
	return nil
}

// metisInputs reads the editor's [{name, field}] rows, skipping half-filled ones.
func metisInputs(raw any) []metisInput {
	list, _ := raw.([]any)
	var inputs []metisInput
	for _, item := range list {
		m, _ := item.(map[string]any)
		name := strings.TrimSpace(fmt.Sprint(valueOr(m["name"], "")))
		field := strings.TrimSpace(fmt.Sprint(valueOr(m["field"], "")))
		if name != "" && field != "" {
			inputs = append(inputs, metisInput{name: name, field: field})
		}
	}
	return inputs
}

func valueOr(v, def any) any {
	if v == nil {
		return def
	}
	return v
}

// metisRefusePlaintext keeps the token off the wire in the clear, the rule the
// Metis sink applies: http is allowed to loopback only.
func metisRefusePlaintext(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		// The address is not repeated: it may hold a credential.
		return metisSettingsError("the Metis address is not a URL like https://metis.example.com")
	}
	if parsed.User != nil {
		return metisSettingsError("the Metis address carries a user or password; put the token or password in its own setting")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return metisSettingsError("the Metis address must start with https://")
	}
	if parsed.Scheme != "http" {
		return nil
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return metisSettingsError("refusing plain http to %q: the token travels in a header, so use https", host)
}

// errMetisRedirect is what a redirect from the Metis server becomes.
var errMetisRedirect = errors.New("the Metis server answered with a redirect, which is not followed: check the server address")

// metisHTTPClient is Hermod's data client without redirects. A redirect
// carries the request, and on the same host the token, to wherever the server
// points, and the Metis API has no reason to send one. It is refused rather
// than returned: the SDK reads a 3xx with an empty body as an empty answer.
var metisHTTPClient = &http.Client{
	Transport: httpclient.DataClient.Transport,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errMetisRedirect
	},
}

// clientFor is the shared client for the request's server and account. The
// credentials are only ever inside the digest.
func (t *MetisDecideTransformer) clientFor(req metisDecideRequest) *metisClient {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "url\x00%s\x00token\x00%s\x00user\x00%s\x00pass\x00%s\x00org\x00%s\x00",
		req.baseURL, req.token, req.username, req.password, req.orgID)
	key := hex.EncodeToString(h.Sum(nil))

	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok := t.clients[key]; ok {
		return c
	}
	if len(t.clients) >= metisMaxClients {
		// Old servers and rotated secrets. Dropping them all is simpler than
		// an LRU, and costs at most one sign-in per node still in use.
		clear(t.clients)
	}
	opts := []sdk.Option{sdk.WithHTTPClient(metisHTTPClient)}
	if req.token != "" {
		opts = append(opts, sdk.WithToken(req.token))
	}
	c := &metisClient{client: sdk.NewClient(req.baseURL, opts...)}
	if req.orgID != "" {
		c.client.SetOrganization(req.orgID)
	}
	t.clients[key] = c
	return c
}

// login signs in with the request's username and password, once unless force.
// A static token needs none.
func (c *metisClient) login(ctx context.Context, req metisDecideRequest, force bool) error {
	if req.token != "" {
		return nil
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.authed && !force {
		return nil
	}
	if c.loginErr != nil && time.Since(c.loginFailed) < metisLoginBackoff {
		return c.loginErr
	}
	if err := c.client.Login(ctx, req.username, req.password); err != nil {
		c.authed = false
		var apiErr *sdk.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			// Metis refused the account: remember it, so the next records
			// do not try the same password again.
			c.loginErr, c.loginFailed = err, time.Now()
		}
		return err
	}
	c.authed, c.loginErr = true, nil
	return nil
}

func (c *metisClient) evaluate(ctx context.Context, req metisDecideRequest, vars sdk.Variables) (*sdk.DecisionResult, error) {
	if err := c.login(ctx, req, false); err != nil {
		return nil, err
	}
	var opts []sdk.EvaluateOption
	if req.version > 0 {
		opts = append(opts, sdk.WithDecisionVersion(req.version))
	}
	res, err := c.client.EvaluateDecision(ctx, req.projectID, req.decisionKey, vars, opts...)
	if err != nil && sdk.IsUnauthorized(err) && req.token == "" {
		// The login token expired: sign in again, once, and ask again.
		if lerr := c.login(ctx, req, true); lerr != nil {
			return nil, lerr
		}
		res, err = c.client.EvaluateDecision(ctx, req.projectID, req.decisionKey, vars, opts...)
	}
	return res, err
}

// metisCallError says what Metis answered, marked permanent when Metis stated
// a refusal: a 4xx is about this record or these settings, and asking again
// gets the same answer, and a redirect is a server sending the token elsewhere. A
// transport failure or 5xx is left retryable. Metis's text is cut to
// metisMaxErrorText, since the error goes to logs and dead letters.
func metisCallError(err error) error {
	if errors.Is(err, errMetisRedirect) {
		return &metisError{msg: "metis_decide: " + errMetisRedirect.Error(), err: err, permanent: true}
	}
	var apiErr *sdk.APIError
	if errors.As(err, &apiErr) {
		if code := apiErr.StatusCode; code < 500 && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests {
			return &metisError{msg: "metis_decide: Metis refused the decision: " + cutText(err.Error()), err: err, permanent: true}
		}
	}
	return &metisError{msg: "metis_decide: Metis did not answer: " + cutText(err.Error()), err: err}
}

// metisError keeps the SDK's error in the chain while its text is bounded.
type metisError struct {
	msg       string
	err       error
	permanent bool
}

func (e *metisError) Error() string { return e.msg }

func (e *metisError) Unwrap() []error {
	if e.permanent {
		return []error{e.err, hermod.ErrPermanent}
	}
	return []error{e.err}
}

func cutText(s string) string {
	if len(s) <= metisMaxErrorText {
		return s
	}
	cut := s[:metisMaxErrorText]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "... (cut)"
}

func (req metisDecideRequest) variables(msg hermod.Message) sdk.Variables {
	if len(req.inputs) == 0 {
		return sdk.Variables(msg.Data())
	}
	vars := make(sdk.Variables, len(req.inputs))
	for _, in := range req.inputs {
		vars[in.name] = evaluator.GetMsgValByPath(msg, in.field)
	}
	return vars
}

// Transform implements transformer.Transformer.
func (t *MetisDecideTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	req, err := resolveMetisDecideRequest(config, msg)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, req.timeout)
	defer cancel()
	res, err := t.clientFor(req).evaluate(ctx, req, req.variables(msg))
	if err != nil {
		return nil, metisCallError(err)
	}

	if !res.Matched() {
		switch req.onNoMatch {
		case metisNoMatchDrop:
			return nil, nil
		case metisNoMatchFail:
			return nil, fmt.Errorf("metis_decide: no rule of %q matched the record: %w", req.decisionKey, hermod.ErrPermanent)
		}
	}

	result := metisResult(res)
	msg.SetData(req.targetField, result)
	if values, ok := result["values"].(map[string]any); ok && req.mergeOutputs {
		for k, v := range values {
			// Hermod's own markers say whether a record failed or was dead
			// lettered; a decision table must not be able to set them.
			if strings.HasPrefix(strings.ToLower(k), "_hermod") {
				continue
			}
			msg.SetData(k, v)
		}
	}
	return msg, nil
}

// metisResult is what the target field receives.
func metisResult(res *sdk.DecisionResult) map[string]any {
	values := map[string]any(res.Values)
	if values == nil {
		values = map[string]any{}
	}
	rules := make([]any, 0, len(res.MatchedRules))
	for _, r := range res.MatchedRules {
		rules = append(rules, r)
	}
	ruleIDs := make([]any, 0, len(res.MatchedRuleIDs))
	for _, id := range res.MatchedRuleIDs {
		ruleIDs = append(ruleIDs, id)
	}
	return map[string]any{
		"values":           values,
		"matched":          res.Matched(),
		"matched_rules":    rules,
		"matched_rule_ids": ruleIDs,
		"decision": map[string]any{
			"key":     res.Decision.Key,
			"name":    res.Decision.Name,
			"version": res.Decision.Version,
		},
	}
}
