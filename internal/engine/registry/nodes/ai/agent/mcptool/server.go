// Package mcptool lets the ai_agent node use one tool of a remote MCP server
// over the Streamable HTTP transport.
//
// The node's configuration names the server and the single remote tool; the
// model only ever sees that tool, under the node's own name for it. Nothing a
// remote server sends — its tool descriptions, schemas or results — is trusted:
// text is capped, schemas are checked, and results are returned as data.
package mcptool

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// reservedHeaders are set by the MCP transport itself. A configured header
// may not replace them: the session and protocol negotiation are not the
// workflow's to steer.
var reservedHeaders = map[string]bool{
	"Accept": true, "Content-Type": true, "Content-Length": true, "Host": true,
	"Mcp-Session-Id": true, "Mcp-Protocol-Version": true, "Last-Event-Id": true,
	"Connection": true, "Transfer-Encoding": true,
}

// Server is a remote MCP server as the node configures it. URL and header
// values are templates, resolved per message with ResolveTemplateScoped, so
// {{secret("NAME")}} works and the message's own data cannot choose them.
type Server struct {
	URL     string
	Headers map[string]string
}

// Endpoint is a Server resolved for one message.
type Endpoint struct {
	URL     string
	Headers map[string]string
}

// ParseServer reads the {url, headers} object of an mcp tool.
func ParseServer(raw any) (Server, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return Server{}, errors.New("server must be an object with a url")
	}
	var s Server
	s.URL, _ = m["url"].(string)
	s.URL = strings.TrimSpace(s.URL)
	if s.URL == "" {
		return s, errors.New("server.url is required")
	}
	if !strings.Contains(s.URL, "{{") {
		if err := CheckURL(s.URL); err != nil {
			return s, err
		}
	}
	headers, err := parseHeaders(m["headers"])
	if err != nil {
		return s, err
	}
	s.Headers = headers
	return s, nil
}

// parseHeaders reads the server's headers: names are checked and
// canonicalised, values may not contain control characters.
func parseHeaders(raw any) (map[string]string, error) {
	out := map[string]string{}
	if raw == nil {
		return out, nil
	}
	hm, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("server.headers must be an object of name to value")
	}
	for name, v := range hm {
		value, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("server header %q must be a string", name)
		}
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		switch {
		case !validHeaderName(canonical):
			return nil, fmt.Errorf("server header name %q is not valid", name)
		case reservedHeaders[canonical]:
			return nil, fmt.Errorf("server header %q is set by the MCP transport and cannot be configured", canonical)
		case !validHeaderValue(value):
			return nil, fmt.Errorf("server header %q has a value with control characters", canonical)
		}
		out[canonical] = value
	}
	return out, nil
}

// Resolve fills the server's templates for msg. Only functions such as
// secret() resolve; a field of the message resolves to nothing.
func (s Server) Resolve(msg hermod.Message) (Endpoint, error) {
	ep := Endpoint{URL: strings.TrimSpace(evaluator.ResolveTemplateScoped(s.URL, msg))}
	if err := CheckURL(ep.URL); err != nil {
		return Endpoint{}, fmt.Errorf("server url, once resolved: %w", err)
	}
	if len(s.Headers) > 0 {
		ep.Headers = make(map[string]string, len(s.Headers))
		for k, v := range s.Headers {
			value := evaluator.ResolveTemplateScoped(v, msg)
			if !validHeaderValue(value) {
				return Endpoint{}, fmt.Errorf("server header %q resolves to a value with control characters", k)
			}
			ep.Headers[k] = value
		}
	}
	return ep, nil
}

// CheckURL accepts only absolute http and https URLs with a host.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("server url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("server url scheme %q is not allowed; use http or https", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("server url has no host")
	}
	return nil
}

// validHeaderName reports whether name is an RFC 9110 token.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r > 0x7e || r <= ' ' || strings.ContainsRune(`"(),/:;<=>?@[\\]{}`, r) {
			return false
		}
	}
	return true
}

// validHeaderValue rejects control characters, which could split a header.
func validHeaderValue(v string) bool {
	for _, r := range v {
		if (r < ' ' && r != '\t') || r == 0x7f {
			return false
		}
	}
	return true
}
