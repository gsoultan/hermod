package lookup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/httpclient"
	"golang.org/x/sync/singleflight"
)

func init() {
	transformer.Register("api_lookup", &APILookupTransformer{
		sf: &singleflight.Group{},
	})
}

type APILookupTransformer struct {
	sf *singleflight.Group
}

// configInt reads a whole number that the editor may have saved as a number or
// as text.
//
// "Max Retries" is a NumberInput, so it round-trips through JSON as a float64,
// and every reader here went through GetConfigString, which returns "" for
// anything that is not a string. The retry count an operator set in the editor
// was therefore always read as zero: a control that cannot fire, which is worse
// than no control because someone believes it is there.
func configInt(config map[string]any, key string) int {
	switch v := config[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

// resolveHeaderTemplates renders the configured header JSON against a message.
//
// A malformed value is an error rather than an empty map. Dropping the headers
// silently sent the request anyway — to a real endpoint, with the Authorization
// header the operator wrote sitting unparsed in the config — and reported it
// nowhere.
func resolveHeaderTemplates(headersStr string, data map[string]any) (map[string]string, error) {
	if headersStr == "" {
		return nil, nil
	}
	var headers map[string]any
	if err := json.Unmarshal([]byte(headersStr), &headers); err != nil {
		return nil, fmt.Errorf("api_lookup: headers is not valid JSON: %w", err)
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if vs, ok := v.(string); ok {
			out[k] = evaluator.ResolveTemplate(vs, data)
			continue
		}
		out[k] = fmt.Sprintf("%v", v)
	}
	return out, nil
}

// applyQueryParams renders the configured query-param JSON onto the URL. Same
// reasoning as resolveHeaderTemplates: a typo used to mean the request went out
// unfiltered, which is a different query against a real API, not a no-op.
func applyQueryParams(resolvedURL, queryParamsStr string, data map[string]any) (string, error) {
	if queryParamsStr == "" {
		return resolvedURL, nil
	}
	var qParams map[string]any
	if err := json.Unmarshal([]byte(queryParamsStr), &qParams); err != nil {
		return "", fmt.Errorf("api_lookup: queryParams is not valid JSON: %w", err)
	}
	u, err := url.Parse(resolvedURL)
	if err != nil {
		return "", fmt.Errorf("api_lookup: url %q is not parseable: %w", resolvedURL, err)
	}
	q := u.Query()
	for k, v := range qParams {
		vStr := fmt.Sprintf("%v", v)
		if vs, ok := v.(string); ok {
			vStr = evaluator.ResolveTemplate(vs, data)
		}
		q.Set(k, vStr)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// defaultAPILookupTTL bounds how long an HTTP response is reused when the node
// does not say.
//
// The alternative is what this used to do: SetLookupCache reads ttl <= 0 as "no
// expiry", and the editor leaves Cache TTL empty by default, so an API response
// was kept for the lifetime of the process. Of everything Hermod caches, a
// remote HTTP response is the one with the least claim to being permanent.
const defaultAPILookupTTL = 5 * time.Minute

// apiErrorPolicy decides what a failed HTTP call means.
//
// It differs from resolveMissPolicy in one place, and only one: with nothing
// configured at all it fails rather than passing through. A lookup that found
// no row is an ordinary event that a pipeline may reasonably ignore; a request
// that did not complete is not, and reporting it is what api_lookup already did
// before onMiss existed. Keeping that default is what lets onMiss be added
// without changing any pipeline that has not asked for it.
func apiErrorPolicy(config map[string]any, hasDefault bool) missPolicy {
	switch strings.ToLower(strings.TrimSpace(core.GetConfigString(config, "onMiss"))) {
	case "fail":
		return missFail
	case "default":
		return missDefault
	case "passthrough":
		return missPassthrough
	}
	if hasDefault {
		return missDefault
	}
	return missFail
}

// apiMissError describes a lookup that reached the endpoint and still could not
// produce a value, naming the path that came back empty.
func apiMissError(method, resolvedURL, responsePath string) error {
	if responsePath == "" || responsePath == "." {
		return fmt.Errorf("api_lookup: %s %s returned no usable body", method, resolvedURL)
	}
	return fmt.Errorf("api_lookup: %s %s returned a response with nothing at path %q",
		method, resolvedURL, responsePath)
}

// apiLookupCacheKey identifies the response this lookup will store.
//
// Everything that selects that response has to be in it. The URL and the body
// were already resolved before being keyed on -- which is why api_lookup never
// had db_lookup's total key collapse -- but three inputs were applied *after*
// the key was built and so never reached it:
//
//   - templated headers, so two tenants sharing an endpoint shared an entry;
//   - the templated credential, so a per-user token returned whatever the
//     first user's token had fetched;
//   - responsePath, which decides what is extracted and therefore what is
//     stored, so two nodes reading different fields of one endpoint collided.
//
// The credential is only ever mixed into the digest, never written into the key
// in the clear: the lookup cache is an in-memory map whose keys are iterated
// during eviction and printed by anything debugging it, and a bearer token does
// not belong in either. Hashing the rest with it also bounds the key, which
// message-derived header values otherwise do not.
//
// The "api:<method>:<url>" prefix is kept readable so a key remains
// recognisable and prefix-matchable, the way LookupCacheKeyPrefix keeps
// db_lookup's entries invalidatable per source.
func apiLookupCacheKey(method, resolvedURL, resolvedBody, responsePath, authType, credential string, headers map[string]string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "body\x00%s\x00path\x00%s\x00auth\x00%s\x00cred\x00%s\x00",
		resolvedBody, responsePath, authType, credential)

	// Sorted: Go randomises map iteration, so an unsorted walk would give one
	// request a different key on every message and defeat the cache entirely.
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		_, _ = fmt.Fprintf(h, "hdr\x00%s\x00%s\x00", k, headers[k])
	}

	return fmt.Sprintf("api:%s:%s:%s", method, resolvedURL, hex.EncodeToString(h.Sum(nil)))
}

func (t *APILookupTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	registry, ok := ctx.Value(hermod.RegistryKey).(interface {
		GetLookupCache(key string) (any, bool)
		SetLookupCache(key string, value any, ttl time.Duration)
	})

	if !ok {
		return msg, errors.New("registry not found in context")
	}

	method := core.GetConfigString(config, "method")
	if method == "" {
		method = "GET"
	}
	rawURL := core.GetConfigString(config, "url")
	headersStr := core.GetConfigString(config, "headers")
	bodyTemp := core.GetConfigString(config, "body")
	responsePath := core.GetConfigString(config, "responsePath")
	targetField := core.GetConfigString(config, "targetField")
	timeoutStr := core.GetConfigString(config, "timeout")
	retryDelayStr := core.GetConfigString(config, "retryDelay")
	ttlStr := core.GetConfigString(config, "ttl")
	queryParamsStr := core.GetConfigString(config, "queryParams")
	authType := core.GetConfigString(config, "authType") // "basic", "bearer"
	token := core.GetConfigString(config, "token")
	username := core.GetConfigString(config, "username")
	password := core.GetConfigString(config, "password")
	defaultValue := core.GetConfigString(config, "defaultValue")
	maxRetries := configInt(config, "maxRetries")

	// Like db_lookup: a lookup that cannot enrich used to return success, so the
	// sink could not tell an enriched message from an un-enriched one. onMiss
	// makes that an explicit choice. A misconfiguration is deliberately not
	// routed through it -- see the ttl and JSON errors below.
	onMiss := resolveMissPolicy(config, defaultValue != "")

	if rawURL == "" || targetField == "" {
		return msg, applyMissPolicy(msg, onMiss, targetField, defaultValue,
			fmt.Errorf("api_lookup: incomplete config (url=%q, targetField=%q)", rawURL, targetField))
	}

	// Parsed before the request, not after it: a ttl that cannot be parsed is a
	// cache that never expires, and finding that out only once the response is
	// in hand means the bad entry is already stored.
	ttl, err := resolveLookupTTL(ttlStr, defaultAPILookupTTL)
	if err != nil {
		return msg, fmt.Errorf("api_lookup: %w", err)
	}

	data := msg.Data()
	resolvedURL, err := applyQueryParams(evaluator.ResolveTemplate(rawURL, data), queryParamsStr, data)
	if err != nil {
		return msg, err
	}

	resolvedBody := ""
	if bodyTemp != "" {
		resolvedBody = evaluator.ResolveTemplate(bodyTemp, data)
	}

	// Resolve the headers and the credential up front rather than inside the
	// retry loop. Both are templated per message, so both help select the
	// response and both have to reach the cache key; resolving them here is
	// what makes that possible, and it also stops the loop re-parsing the same
	// JSON on every attempt.
	resolvedHeaders, err := resolveHeaderTemplates(headersStr, data)
	if err != nil {
		return msg, err
	}
	credential := ""
	switch authType {
	case "basic":
		credential = evaluator.ResolveTemplate(username, data) + "\x00" + evaluator.ResolveTemplate(password, data)
	case "bearer":
		credential = evaluator.ResolveTemplate(token, data)
	}

	cacheKey := apiLookupCacheKey(method, resolvedURL, resolvedBody, responsePath, authType, credential, resolvedHeaders)

	if cached, found := registry.GetLookupCache(cacheKey); found {
		msg.SetData(targetField, cached)
		return msg, nil
	}

	// Execute API call with singleflight to avoid redundant concurrent requests
	res, err, _ := t.sf.Do(cacheKey, func() (any, error) {
		// Execute API call with retries
		timeout := 10 * time.Second
		if timeoutStr != "" {
			if t, err := time.ParseDuration(timeoutStr); err == nil {
				timeout = t
			}
		}

		retryDelay := 1 * time.Second
		if retryDelayStr != "" {
			if d, err := time.ParseDuration(retryDelayStr); err == nil {
				retryDelay = d
			}
		}

		var respData any
		var lastErr error

		for i := 0; i <= maxRetries; i++ {
			if i > 0 {
				time.Sleep(retryDelay)
			}

			var reqBody io.Reader
			if resolvedBody != "" {
				reqBody = strings.NewReader(resolvedBody)
			}

			apiCtx, cancel := context.WithTimeout(ctx, timeout)
			req, err := http.NewRequestWithContext(apiCtx, method, resolvedURL, reqBody)
			if err != nil {
				cancel()
				lastErr = err
				continue
			}

			for k, v := range resolvedHeaders {
				req.Header.Set(k, v)
			}

			// Auth, from the credential resolved once above so the request and
			// the cache key cannot disagree about who is asking.
			switch authType {
			case "basic":
				user, pass, _ := strings.Cut(credential, "\x00")
				req.SetBasicAuth(user, pass)
			case "bearer":
				req.Header.Set("Authorization", "Bearer "+credential)
			}

			resp, err := httpclient.DataClient.Do(req)
			if err != nil {
				cancel()
				lastErr = err
				continue
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				resp.Body.Close()
				cancel()
				lastErr = fmt.Errorf("api lookup returned status %d", resp.StatusCode)
				if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
					continue // Retryable
				}
				break // Non-retryable
			}

			respBytes, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			cancel()
			if err != nil {
				lastErr = err
				continue
			}

			if err := json.Unmarshal(respBytes, &respData); err != nil {
				respData = string(respBytes)
			}

			lastErr = nil
			break
		}

		if lastErr != nil {
			return nil, lastErr
		}

		var resultVal any
		if responsePath != "" && responsePath != "." {
			if m, ok := respData.(map[string]any); ok {
				resultVal = evaluator.GetValByPath(m, responsePath)
			} else {
				resultVal = respData
			}
		} else {
			resultVal = respData
		}

		return resultVal, nil
	})

	if err != nil {
		// The request itself failed. apiErrorPolicy defaults to failing where
		// resolveMissPolicy defaults to passing through, so an existing node
		// keeps reporting this exactly as it did -- but "fill in empty
		// responses, yet still fail the message on a 500" is now sayable, which
		// it was not while a defaultValue unconditionally swallowed the error.
		return msg, applyMissPolicy(msg, apiErrorPolicy(config, defaultValue != ""),
			targetField, defaultValue, fmt.Errorf("api lookup failed: %w", err))
	}

	if res == nil {
		// The call succeeded and produced nothing usable: the responsePath
		// matched no part of the body, or the body was empty. Same event
		// db_lookup calls a miss, and it is not cached -- a miss is a fact
		// about this message, not about the endpoint.
		return msg, applyMissPolicy(msg, onMiss, targetField, defaultValue,
			apiMissError(method, resolvedURL, responsePath))
	}

	if ttl.cache {
		registry.SetLookupCache(cacheKey, res, ttl.duration)
	}
	msg.SetData(targetField, res)

	return msg, nil
}
