package transformer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("api_lookup", &APILookupTransformer{})
}

type APILookupTransformer struct{}

func (t *APILookupTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	registry, ok := ctx.Value("registry").(interface {
		GetLookupCache(key string) (any, bool)
		SetLookupCache(key string, value any, ttl time.Duration)
	})

	if !ok {
		return msg, fmt.Errorf("registry not found in context")
	}

	method := getConfigString(config, "method")
	if method == "" {
		method = "GET"
	}
	rawURL := getConfigString(config, "url")
	headersStr := getConfigString(config, "headers")
	bodyTemp := getConfigString(config, "body")
	responsePath := getConfigString(config, "responsePath")
	targetField := getConfigString(config, "targetField")
	timeoutStr := getConfigString(config, "timeout")
	maxRetriesStr := getConfigString(config, "maxRetries")
	retryDelayStr := getConfigString(config, "retryDelay")
	ttlStr := getConfigString(config, "ttl")
	queryParamsStr := getConfigString(config, "queryParams")
	authType := getConfigString(config, "authType") // "basic", "bearer"
	token := getConfigString(config, "token")
	username := getConfigString(config, "username")
	password := getConfigString(config, "password")
	defaultValue := getConfigString(config, "defaultValue")

	if rawURL == "" || targetField == "" {
		return msg, nil
	}

	data := msg.Data()
	resolvedURL := evaluator.ResolveTemplate(rawURL, data)

	// Append query parameters
	if queryParamsStr != "" {
		var qParams map[string]any
		if err := json.Unmarshal([]byte(queryParamsStr), &qParams); err == nil {
			u, err := url.Parse(resolvedURL)
			if err == nil {
				q := u.Query()
				for k, v := range qParams {
					vStr := fmt.Sprintf("%v", v)
					if vs, ok := v.(string); ok {
						vStr = evaluator.ResolveTemplate(vs, data)
					}
					q.Set(k, vStr)
				}
				u.RawQuery = q.Encode()
				resolvedURL = u.String()
			}
		}
	}

	resolvedBody := ""
	if bodyTemp != "" {
		resolvedBody = evaluator.ResolveTemplate(bodyTemp, data)
	}

	// Cache check
	bodyHash := ""
	if resolvedBody != "" {
		h := sha256.New()
		h.Write([]byte(resolvedBody))
		bodyHash = hex.EncodeToString(h.Sum(nil))
	}
	cacheKey := fmt.Sprintf("api:%s:%s:%s", method, resolvedURL, bodyHash)

	if cached, found := registry.GetLookupCache(cacheKey); found {
		msg.SetData(targetField, cached)
		return msg, nil
	}

	// Execute API call with retries
	timeout := 10 * time.Second
	if timeoutStr != "" {
		if t, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = t
		}
	}

	maxRetries := 0
	if maxRetriesStr != "" {
		maxRetries, _ = strconv.Atoi(maxRetriesStr)
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

		if headersStr != "" {
			var headers map[string]any
			if err := json.Unmarshal([]byte(headersStr), &headers); err == nil {
				for k, v := range headers {
					vStr := fmt.Sprintf("%v", v)
					if vs, ok := v.(string); ok {
						vStr = evaluator.ResolveTemplate(vs, data)
					}
					req.Header.Set(k, vStr)
				}
			}
		}

		// Auth
		if authType == "basic" {
			req.SetBasicAuth(evaluator.ResolveTemplate(username, data), evaluator.ResolveTemplate(password, data))
		} else if authType == "bearer" {
			req.Header.Set("Authorization", "Bearer "+evaluator.ResolveTemplate(token, data))
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			cancel()
			lastErr = fmt.Errorf("api lookup returned status %d", resp.StatusCode)
			if resp.StatusCode >= 500 || resp.StatusCode == 429 {
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
		if defaultValue != "" {
			msg.SetData(targetField, defaultValue)
			return msg, nil
		}
		return msg, fmt.Errorf("failed to execute api lookup after %d retries: %w", maxRetries, lastErr)
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

	if resultVal == nil && defaultValue != "" {
		resultVal = defaultValue
	}

	// Update cache
	if resultVal != nil {
		var ttl time.Duration
		if ttlStr != "" {
			ttl, _ = time.ParseDuration(ttlStr)
		}
		registry.SetLookupCache(cacheKey, resultVal, ttl)
		msg.SetData(targetField, resultVal)
	}

	return msg, nil
}
