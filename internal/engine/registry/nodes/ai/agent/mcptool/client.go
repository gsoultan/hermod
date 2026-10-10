package mcptool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/version"
	"github.com/gsoultan/hermod/pkg/infra/httpclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// MaxResult caps the text of one remote tool result.
	MaxResult = 16 << 10
	// maxEventBytes caps one server-sent event the transport buffers.
	maxEventBytes = 1 << 20
	// maxListPages bounds how far tools/list is paged to find the tool.
	maxListPages = 20

	defaultTTL         = 5 * time.Minute
	defaultMaxEntries  = 256
	defaultCallTimeout = 30 * time.Second
)

// Remote is what the agent uses of a remote tool's description.
type Remote struct {
	// Description is the server's description, capped at MaxDescription.
	Description string
	// Schema is the server's input schema, sanitized.
	Schema map[string]any
	// ReadOnly is true only when the server annotates the tool read-only and
	// not destructive. A tool without annotations is not read-only.
	ReadOnly bool
}

// Result is a remote tool call's outcome as text for the model.
type Result struct {
	Text    string
	IsError bool
}

// Options configure a Client. Zero values take the defaults.
type Options struct {
	// Transport carries the HTTP requests; nil uses Hermod's data client
	// transport (dial, TLS and response-header timeouts, no SSRF check, as
	// for any other operator-configured destination).
	Transport http.RoundTripper
	// TTL is how long a tool description is cached.
	TTL time.Duration
	// MaxEntries bounds the description cache.
	MaxEntries int
	// CallTimeout bounds each list or call, connection included.
	CallTimeout time.Duration
}

// Client talks to remote MCP servers. It is safe for concurrent use.
type Client struct {
	transport   http.RoundTripper
	cache       *cache
	callTimeout time.Duration
}

// New builds a Client.
func New(o Options) *Client {
	if o.Transport == nil {
		o.Transport = httpclient.DataClient.Transport
	}
	if o.TTL <= 0 {
		o.TTL = defaultTTL
	}
	if o.MaxEntries <= 0 {
		o.MaxEntries = defaultMaxEntries
	}
	if o.CallTimeout <= 0 {
		o.CallTimeout = defaultCallTimeout
	}
	return &Client{transport: o.Transport, cache: newCache(o.TTL, o.MaxEntries), callTimeout: o.CallTimeout}
}

var defaultClient = New(Options{})

// Default is the process-wide client, whose description cache all agent
// nodes share.
func Default() *Client { return defaultClient }

// Describe finds the named tool on the server. Only that tool's description
// is kept; the server's other tools are never shown to anyone.
func (c *Client) Describe(ctx context.Context, ep Endpoint, tool string) (Remote, error) {
	key := cacheKey(ep, tool)
	if r, ok := c.cache.get(key); ok {
		return r, nil
	}
	var found *mcp.Tool
	err := c.session(ctx, ep, func(ctx context.Context, cs *mcp.ClientSession) error {
		params := &mcp.ListToolsParams{}
		for range maxListPages {
			res, err := cs.ListTools(ctx, params)
			if err != nil {
				return err
			}
			for _, t := range res.Tools {
				if t != nil && t.Name == tool {
					found = t
					return nil
				}
			}
			if res.NextCursor == "" {
				return nil
			}
			params = &mcp.ListToolsParams{Cursor: res.NextCursor}
		}
		return nil
	})
	if err != nil {
		return Remote{}, fmt.Errorf("listing the tools of the MCP server: %s", redact(err, ep))
	}
	if found == nil {
		return Remote{}, fmt.Errorf("the MCP server has no tool named %q", tool)
	}
	schema, err := sanitizeSchema(found.InputSchema)
	if err != nil {
		return Remote{}, fmt.Errorf("remote tool %q: %w", tool, err)
	}
	r := Remote{
		Description: Clip(strings.TrimSpace(found.Description), MaxDescription),
		Schema:      schema,
		ReadOnly:    readOnly(found.Annotations),
	}
	c.cache.put(key, r)
	return r, nil
}

// readOnly is the conservative reading of the annotations: they are hints,
// and their defaults (readOnlyHint false, destructiveHint true) mean a tool
// that says nothing may change things.
func readOnly(a *mcp.ToolAnnotations) bool {
	if a == nil || !a.ReadOnlyHint {
		return false
	}
	return a.DestructiveHint == nil || !*a.DestructiveHint
}

// Call runs the named remote tool with args. A tool that reports an error is
// a Result with IsError; err is for a call that did not complete.
func (c *Client) Call(ctx context.Context, ep Endpoint, tool string, args map[string]any) (Result, error) {
	if args == nil {
		args = map[string]any{}
	}
	var res *mcp.CallToolResult
	err := c.session(ctx, ep, func(ctx context.Context, cs *mcp.ClientSession) error {
		var err error
		res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("calling the MCP server: %s", redact(err, ep))
	}
	if len(res.InputRequests) > 0 {
		return Result{IsError: true, Text: "the remote tool asked for further input, which this agent cannot give"}, nil
	}
	return Result{Text: Clip(resultText(res), MaxResult), IsError: res.IsError}, nil
}

// resultText joins the result's text parts. A result with no text but
// structured content is given as that content's JSON.
func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, part := range res.Content {
		if t, ok := part.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
			if b.Len() > MaxResult {
				break
			}
		}
	}
	if b.Len() == 0 && res.StructuredContent != nil {
		if raw, err := json.Marshal(res.StructuredContent); err == nil {
			return string(raw)
		}
	}
	return b.String()
}

// session opens a session for one request and closes it afterwards. Each is
// bounded by the call timeout inside ctx's own deadline (the node's).
func (c *Client) session(ctx context.Context, ep Endpoint, fn func(context.Context, *mcp.ClientSession) error) error {
	ctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	hc := &http.Client{
		Transport: headerTransport{base: c.transport, headers: ep.Headers},
		// A redirect could carry the configured credentials to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:             ep.URL,
		HTTPClient:           hc,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
		MaxEventSize:         maxEventBytes,
	}
	// No sampling, elicitation or roots handlers: the server cannot make
	// this client do anything but answer the one request.
	client := mcp.NewClient(&mcp.Implementation{Name: "hermod-ai-agent", Version: version.Version}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return err
	}
	defer func() {
		if ctx.Err() != nil {
			// Close sends the session DELETE, which a server still busy with
			// the abandoned request may hold for the SDK's own bound (5s).
			// The caller's deadline has passed, so it does not wait for it.
			go func() { _ = cs.Close() }()
			return
		}
		_ = cs.Close()
	}()
	if err := fn(ctx, cs); err != nil {
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timed out: %w", err)
		}
		return err
	}
	return nil
}

// headerTransport adds the configured headers to every request of a session.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(t.headers) > 0 {
		req = req.Clone(req.Context())
		for k, v := range t.headers {
			req.Header.Set(k, v)
		}
	}
	return t.base.RoundTrip(req)
}

// redact removes the endpoint's URL (which may carry a key in its query) and
// header values from an error before it reaches the model or a transcript.
func redact(err error, ep Endpoint) string {
	s := err.Error()
	secrets := []string{ep.URL}
	if u, perr := url.Parse(ep.URL); perr == nil {
		secrets = append(secrets, u.RawQuery, u.Host)
		if u.User != nil {
			secrets = append(secrets, u.User.String())
		}
	}
	for _, v := range ep.Headers {
		secrets = append(secrets, v)
		if _, after, ok := strings.Cut(v, " "); ok {
			secrets = append(secrets, after)
		}
	}
	for _, secret := range secrets {
		if len(secret) >= 4 {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return s
}
