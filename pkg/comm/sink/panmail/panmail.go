// Package panmail sends each message as an email through a panmail gateway.
//
// It is a sibling of the SMTP sink and reaches the same gateway, but through
// the gateway's own API rather than its SMTP door. What that buys is the
// message id every send returns — the handle delivery events and webhooks are
// keyed by — and refusals that say which refusal they are, rather than an SMTP
// reply code that has to be guessed at.
//
// # Templates
//
// Every string setting is a Go template over the message — see renderData for
// what is in scope. That includes the four that decide where a message goes:
// the gateway url, the api key, the provider id and the stored template id.
//
// The provider id and the template id are refused when they render empty rather
// than sent: an empty provider sends from whatever the gateway picks, and an
// empty template id would fall through to the body fallback, so a typo in a
// field name would mail the wrong thing rather than fail.
//
// The gateway url and the api key are a security decision, because between them
// they choose where a tenant-wide credential is sent — and templated, a row
// chooses it. Config.AllowedHosts is therefore required when either is
// templated, and New refuses to start without it.
//
// # Retries and duplicate mail
//
// Sending is not idempotent and the gateway has no de-duplication key, so a
// retry after a timeout is a retry of a message that may already be on its way
// to the recipient. The SDK refuses to make that call for you: it never repeats
// a send whose outcome it does not know.
//
// Hermod's RetrySink has no such discrimination — it retries every error the
// same way. This sink therefore makes the distinction itself, using the
// idempotency claim as the lever:
//
//   - The gateway *stated* a refusal (rate limit, full backlog, bad key, bad
//     argument). The message was definitively not accepted, so the claim is
//     released and a retry is free to take it.
//   - The outcome is *unknown* (a transport error, a dropped connection). The
//     claim is kept, so the retry that follows finds the key taken and does
//     nothing instead of mailing the recipient a second time.
//
// With idempotency off there is nothing to hold the claim, and a retry after an
// unknown outcome may deliver twice. The error says so rather than looking
// routine.
package panmail

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"net"
	"net/url"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gsoultan/hermod"
	sdk "github.com/gsoultan/panmail-sdk"
)

// Config is everything the sink needs. The string fields marked as templated
// are Go templates over the message — see renderData for what is in scope.
type Config struct {
	// BaseURL is the gateway's origin, e.g. https://mail.example.com. Not a
	// path to a procedure. Plaintext http is refused unless the host is
	// loopback, because the api key travels in a header.
	//
	// Templated, and the one templated field that is a security decision: it
	// chooses where the api key is sent, so a row is choosing that. AllowedHosts
	// is required when it is templated, and bounds the choice to hosts you name.
	BaseURL string

	// APIKey carries the tenant and needs the email:send scope. Templated, so
	// one sink can send on behalf of many tenants; AllowedHosts is required when
	// it is, for the same reason as BaseURL.
	APIKey string

	// ProviderID is the configured sending provider. Required: the gateway
	// will not guess which of a tenant's providers a message goes out through,
	// because the wrong guess is mail sent from the wrong domain. Templated —
	// a rendering that is empty is refused rather than sent unrouted.
	ProviderID string

	// AllowedHosts bounds the hosts a templated BaseURL may resolve to. Each
	// entry is a hostname, or a single leading "*." wildcard over a domain with
	// at least two labels ("*.mail.example.com"). A rendered host that matches
	// none of them is refused before anything is sent.
	//
	// Required when BaseURL or APIKey is templated, and ignored otherwise.
	// Without it, any row that reaches this sink could name its own gateway and
	// be handed the api key.
	AllowedHosts []string

	// From must be an address the provider is authorised to send as. Templated.
	From string

	// To, Cc and Bcc are templated. A rendered entry containing commas is split,
	// so one template variable can carry several addresses.
	To  []string
	Cc  []string
	Bcc []string

	// Subject is templated. Ignored when TemplateID is set and the stored
	// template supplies one.
	Subject string

	// HTML and Text are the two bodies, both templated. Send both when you can:
	// the text part is what recipients with images off and spam filters read.
	HTML string
	Text string

	// TemplateID renders a template stored in the gateway instead of the bodies
	// above. The message's data map is passed as the template data. Templated,
	// so one stream can pick "welcome" or "receipt" per row; a rendering that is
	// empty is refused rather than quietly falling back to a body.
	TemplateID string

	// RateLimitRetries waits out up to n rate-limit refusals inside a single
	// Write, sleeping for the delay the gateway asks for. Off by default: it
	// makes Write block for as long as the gateway asks, and Hermod's own retry
	// is usually the better place for that.
	RateLimitRetries int

	// Timeout bounds a single send. Zero uses the SDK's 30s default.
	Timeout time.Duration
}

// IdempotencyStore guards against sending the same mail twice. It is the same
// shape the SMTP sink uses, so one store implementation serves both.
type IdempotencyStore interface {
	// Claim returns true if this call created the record (we own it), false if
	// it already exists.
	Claim(ctx context.Context, key string) (bool, error)
	// MarkSent records a successful completion for the key.
	MarkSent(ctx context.Context, key string) error
	// Release gives up a claim whose work did not complete, so a retry can take
	// it again. Releasing a key already marked sent must do nothing, and
	// releasing one never claimed must not be an error.
	Release(ctx context.Context, key string) error
}

// Sink writes each message to a panmail gateway as one email.
type Sink struct {
	cfg       Config
	formatter hermod.Formatter

	// opts rebuilds a client for a gateway the first message names. Kept because
	// a templated BaseURL or APIKey has no client until a message supplies one.
	opts    []sdk.Option
	allowed []hostRule
	// routed is true when BaseURL or APIKey is templated, which is what turns on
	// the allowlist check and puts the gateway into the idempotency key.
	routed bool

	idemStore         IdempotencyStore
	enableIdempotency bool
	idemKeyTemplate   string

	mu sync.Mutex
	// clients is keyed by values a message rendered, so with a wildcard
	// allowlist the row chooses how many entries there are. Bounded and evicted
	// least-recently-used for that reason — see maxCachedClients.
	clients           map[string]*list.Element
	lru               *list.List
	lastMessageID     string
	lastWriteDedup    bool
	lastWriteConflict bool
}

// hostRule is one entry of Config.AllowedHosts.
type hostRule struct {
	// host is the hostname to match, or for a wildcard the suffix under it,
	// without the leading dot. Always lowercase.
	host     string
	wildcard bool
}

func (r hostRule) matches(host string) bool {
	if r.wildcard {
		// "example.com" itself is not under "*.example.com": a wildcard covers
		// the subdomains you delegate, not the apex you may not control.
		return strings.HasSuffix(host, "."+r.host)
	}
	return host == r.host
}

// parseAllowedHosts turns the configured entries into rules, refusing the ones
// that would not bound anything.
func parseAllowedHosts(entries []string) ([]hostRule, error) {
	rules := make([]hostRule, 0, len(entries))
	for _, raw := range entries {
		entry := strings.ToLower(strings.TrimSpace(raw))
		if entry == "" {
			continue
		}
		if suffix, ok := strings.CutPrefix(entry, "*."); ok {
			// "*.com" would allow every gateway on the internet under .com,
			// which is not a bound.
			if !strings.Contains(suffix, ".") {
				return nil, fmt.Errorf("panmail sink: allowed host %q is too broad; a wildcard "+
					"needs at least two labels under it, e.g. *.mail.example.com", raw)
			}
			rules = append(rules, hostRule{host: suffix, wildcard: true})
			continue
		}
		if strings.Contains(entry, "*") {
			return nil, fmt.Errorf("panmail sink: allowed host %q may only use a wildcard as a "+
				"leading \"*.\" label", raw)
		}
		rules = append(rules, hostRule{host: entry})
	}
	if len(rules) == 0 {
		return nil, errors.New("panmail sink: the allowed hosts list has no usable entries")
	}
	return rules, nil
}

var (
	_ hermod.Sink                = (*Sink)(nil)
	_ hermod.BatchSink           = (*Sink)(nil)
	_ hermod.IdempotencyReporter = (*Sink)(nil)
)

// New builds the sink. Configuration mistakes are refused here rather than on
// the first message, so a sink that cannot work never reports itself healthy.
func New(cfg Config, formatter hermod.Formatter) (*Sink, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("panmail sink: a gateway base url is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("panmail sink: an api key is required")
	}
	if strings.TrimSpace(cfg.ProviderID) == "" {
		return nil, errors.New("panmail sink: a provider id is required; the gateway will not " +
			"guess which provider to send through, because the wrong guess sends from the wrong domain")
	}
	if strings.TrimSpace(cfg.From) == "" {
		return nil, errors.New("panmail sink: a from address is required")
	}
	if len(cfg.To)+len(cfg.Cc)+len(cfg.Bcc) == 0 {
		return nil, errors.New("panmail sink: at least one recipient is required")
	}
	// A formatter stands in for the bodies: with one configured, the formatted
	// message becomes the text part. Without one, something has to say what the
	// mail says.
	if cfg.HTML == "" && cfg.Text == "" && cfg.TemplateID == "" && formatter == nil {
		return nil, errors.New("panmail sink: a message needs an html body, a text body or a template id")
	}

	opts := []sdk.Option{}
	if cfg.RateLimitRetries > 0 {
		opts = append(opts, sdk.WithRateLimitRetries(cfg.RateLimitRetries))
	}
	if cfg.Timeout > 0 {
		opts = append(opts, sdk.WithTimeout(cfg.Timeout))
	}

	s := &Sink{
		cfg:       cfg,
		formatter: formatter,
		opts:      opts,
		routed:    templated(cfg.BaseURL) || templated(cfg.APIKey),
		clients:   map[string]*list.Element{},
		lru:       list.New(),
	}

	if !s.routed {
		// The gateway is fixed, so its mistakes are configuration mistakes and
		// belong here rather than on the first message. This is also where the
		// SDK refuses plaintext off loopback.
		client, err := sdk.New(cfg.BaseURL, cfg.APIKey, opts...)
		if err != nil {
			return nil, fmt.Errorf("panmail sink: %w", err)
		}
		s.cacheClient(clientKey(cfg.BaseURL, cfg.APIKey), client)
		return s, nil
	}

	// A templated gateway or key means a row decides where a tenant-wide
	// credential is sent. Refusing to start without an allowlist is what keeps
	// that from being an accident.
	if len(cfg.AllowedHosts) == 0 {
		return nil, errors.New("panmail sink: a templated gateway url or api key needs an allowed " +
			"hosts list; without one, any row reaching this sink could name its own gateway and be " +
			"handed the api key")
	}
	allowed, err := parseAllowedHosts(cfg.AllowedHosts)
	if err != nil {
		return nil, err
	}
	s.allowed = allowed
	return s, nil
}

// templated reports whether a configured value is a template rather than a
// literal. It is the same test render makes, so the two cannot disagree about
// which fields are rendered.
func templated(v string) bool { return strings.Contains(v, "{{") }

// clientKey identifies a gateway-and-tenant pair in the client cache. The api
// key is part of the identity — two tenants on one gateway are two clients —
// but never part of anything that is returned or logged.
func clientKey(baseURL, apiKey string) string { return baseURL + "\x00" + apiKey }

// maxCachedClients bounds the client cache.
//
// The allowlist bounds which hosts are reachable, not how many: one "*." rule
// admits every subdomain under it, and nothing bounds the api key at all. Both
// halves of the key are rendered from the message, so an upstream table decides
// how many entries exist — which is the definition of a map that needs a bound
// and an eviction.
//
// A client is an endpoint, a key and an http.Client, so the cost of the bound
// is a cold connection pool for whatever falls out of it. Generous enough that
// a real fleet of gateways stays hot, small enough that a hostile table cannot
// grow this without limit.
const maxCachedClients = 32

// cachedClient is one entry, holding its own key so eviction can find it.
type cachedClient struct {
	key    string
	client *sdk.Client
}

// lookupClient returns a cached client, promoting it to most-recently-used.
// Callers hold s.mu.
func (s *Sink) lookupClient(key string) (*sdk.Client, bool) {
	el, ok := s.clients[key]
	if !ok {
		return nil, false
	}
	entry, ok := el.Value.(*cachedClient)
	if !ok {
		return nil, false
	}
	s.lru.MoveToFront(el)
	return entry.client, true
}

// cacheClient stores a client, evicting the least recently used one when the
// cache is full. Callers hold s.mu.
func (s *Sink) cacheClient(key string, client *sdk.Client) {
	if s.lru == nil {
		s.lru = list.New()
	}
	s.clients[key] = s.lru.PushFront(&cachedClient{key: key, client: client})
	for s.lru.Len() > maxCachedClients {
		oldest := s.lru.Back()
		if oldest == nil {
			return
		}
		s.lru.Remove(oldest)
		if entry, ok := oldest.Value.(*cachedClient); ok {
			delete(s.clients, entry.key)
		}
	}
}

// clientFor resolves the gateway this message goes to, building and caching a
// client the first time each one is named.
//
// A client per message would be correct and would also leak a connection pool
// per message, so they are kept. The cache is bounded by the allowlist: a
// rendered host that matches no rule never reaches it.
func (s *Sink) clientFor(msg hermod.Message) (*sdk.Client, error) {
	if !s.routed {
		s.mu.Lock()
		defer s.mu.Unlock()
		client, _ := s.lookupClient(clientKey(s.cfg.BaseURL, s.cfg.APIKey))
		return client, nil
	}

	data := renderData(msg)
	baseURL, err := s.renderRequired("gateway url", s.cfg.BaseURL, data)
	if err != nil {
		return nil, err
	}
	apiKey, err := s.renderRequired("api key", s.cfg.APIKey, data)
	if err != nil {
		return nil, err
	}
	if err := s.checkHost(baseURL); err != nil {
		return nil, err
	}

	key := clientKey(baseURL, apiKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	if client, ok := s.lookupClient(key); ok {
		return client, nil
	}
	// sdk.New is what refuses a missing scheme, credentials in the url, and
	// plaintext off loopback — the checks a static gateway gets in New.
	client, err := sdk.New(baseURL, apiKey, s.opts...)
	if err != nil {
		return nil, fmt.Errorf("panmail sink: %w", err)
	}
	s.cacheClient(key, client)
	return client, nil
}

// checkHost is the bound on a templated gateway url.
func (s *Sink) checkHost(baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("panmail sink: the gateway url rendered to something that is not a url: %w", err)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("panmail sink: the gateway url rendered to %q, which has no host", baseURL)
	}
	for _, rule := range s.allowed {
		if rule.matches(host) {
			return nil
		}
	}
	// Names the host and nothing else: this error is logged, and the api key
	// that would have gone to that host must not travel with it.
	return fmt.Errorf("panmail sink: the gateway url rendered to host %q, which is not on the "+
		"allowlist, so the send was refused rather than sending the api key there", host)
}

// renderRequired renders a field that cannot be empty, treating text/template's
// "<no value>" — what a missing key renders as — as empty rather than as a
// value, because sending it is worse than refusing.
func (s *Sink) renderRequired(field, tmpl string, data map[string]any) (string, error) {
	rendered, err := s.render(field, tmpl, data)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(rendered)
	if trimmed == "" || trimmed == noValue {
		return "", fmt.Errorf("panmail sink: the %s template %q rendered empty for this message, "+
			"so there is nothing to send with", field, tmpl)
	}
	return trimmed, nil
}

// noValue is what text/template writes for a key the message does not have.
const noValue = "<no value>"

// EnableIdempotency turns the duplicate guard on. It needs a store to be of any
// use; without one the sink cannot suppress a repeat.
func (s *Sink) EnableIdempotency(on bool) { s.enableIdempotency = on }

// SetIdempotencyStore injects the store implementation.
func (s *Sink) SetIdempotencyStore(store IdempotencyStore) { s.idemStore = store }

// SetIdempotencyKeyTemplate overrides the derived key with a template over the
// message — an order id, say, so two different renderings of the same event
// still count as one mail.
func (s *Sink) SetIdempotencyKeyTemplate(tmpl string) {
	s.idemKeyTemplate = strings.TrimSpace(tmpl)
}

// LastMessageID is the id the gateway gave the most recent accepted send.
// Delivery events, webhooks and the gateway's analytics are all keyed by it,
// and it is the only handle on the message once Write has returned.
func (s *Sink) LastMessageID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastMessageID
}

// LastWriteIdempotent implements hermod.IdempotencyReporter.
func (s *Sink) LastWriteIdempotent() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastWriteDedup, s.lastWriteConflict
}

// Write sends one message as one email.
//
// A nil error means the gateway wrote the message to its outbox, not that it
// was delivered — and not even that it will be. A filter rule can quarantine a
// message for review, and the gateway answers a held message with the same id
// and the same pending status as an accepted one. Subscribe to MAIL_HELD and
// MAIL_EXPIRED on the gateway if that distinction matters.
func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}

	s.mu.Lock()
	s.lastWriteDedup = false
	s.lastWriteConflict = false
	s.mu.Unlock()

	mail, err := s.compose(msg)
	if err != nil {
		return err
	}

	// Resolved before the claim is taken: a message that names a gateway it is
	// not allowed to reach must be refused outright, not left holding a claim
	// that suppresses the retry of a send that never happened.
	client, err := s.clientFor(msg)
	if err != nil {
		return err
	}

	if !s.enableIdempotency || s.idemStore == nil {
		result, err := client.Send(ctx, mail)
		if err != nil {
			return s.unguardedError(err)
		}
		s.recordAccepted(result)
		return nil
	}

	key, err := s.idempotencyKey(msg, mail)
	if err != nil {
		return err
	}
	return s.writeGuarded(ctx, client, key, mail)
}

// writeGuarded sends under an idempotency claim, and decides what the claim
// means afterwards. That decision is the whole reason this sink exists rather
// than a bare SDK call — see the package documentation.
func (s *Sink) writeGuarded(ctx context.Context, client *sdk.Client, key string, mail sdk.Message) error {
	claimed, err := s.idemStore.Claim(ctx, key)
	if err != nil {
		return fmt.Errorf("panmail sink: claim idempotency key: %w", err)
	}
	if !claimed {
		// Already handled — either sent, or held by the claim of a send whose
		// outcome nobody knows. Both mean: do not send this again.
		s.mu.Lock()
		s.lastWriteDedup = true
		s.mu.Unlock()
		return nil
	}

	result, sendErr := client.Send(ctx, mail)
	if sendErr != nil {
		if !stated(sendErr) {
			// The outcome is unknown. Keeping the claim is what stops the retry
			// that follows from mailing the recipient a second time; the cost is
			// a message that may never have been sent staying unsent.
			return fmt.Errorf("panmail sink: the send did not complete and its outcome is "+
				"not known, so the idempotency claim on %q is being kept to stop a retry "+
				"sending the same mail twice; release it to re-drive: %w", key, sendErr)
		}
		// The gateway said plainly it did not take the message, so a retry
		// cannot duplicate anything.
		if rerr := s.idemStore.Release(ctx, key); rerr != nil {
			return fmt.Errorf("panmail sink: the gateway refused the send (%w) and the "+
				"idempotency claim could not be released (%v), so a retry of this message "+
				"will be suppressed", sendErr, rerr)
		}
		return fmt.Errorf("panmail sink: %w", sendErr)
	}

	if err := s.idemStore.MarkSent(ctx, key); err != nil {
		return fmt.Errorf("panmail sink: mark sent: %w", err)
	}
	s.recordAccepted(result)
	return nil
}

// WriteBatch sends each message in turn.
//
// There is no batch send: one message is one email, and the gateway accepts
// them one at a time. Grouping here would only hide which one failed.
func (s *Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if err := s.Write(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

// Ping reports whether the gateway's host accepts a connection.
//
// It deliberately does not send anything: the gateway's only write procedure is
// a send, and a health check that mails somebody is not a health check. A
// successful ping therefore says the gateway is reachable, not that the api key
// or the provider id are good — those are only learned from a real send.
func (s *Sink) Ping(ctx context.Context) error {
	if s.routed {
		return s.pingAllowed(ctx)
	}
	return s.dial(ctx, s.cfg.BaseURL)
}

// pingAllowed is the health check for a templated gateway, which has no one
// host to dial until a message names it. The allowlist is the nearest thing to
// a list of gateways this sink talks to, so the concrete entries are what gets
// dialled; one answering is enough to call the estate reachable.
//
// An allowlist of nothing but wildcards leaves nothing to dial, and says so
// rather than passing — a health check that cannot check anything reporting
// healthy is how an unreachable gateway stays unnoticed.
func (s *Sink) pingAllowed(ctx context.Context) error {
	scheme := "https://"
	if parsed, err := url.Parse(s.cfg.BaseURL); err == nil && parsed.Scheme == "http" {
		scheme = "http://"
	}

	var errs []error
	for _, rule := range s.allowed {
		if rule.wildcard {
			continue
		}
		err := s.dial(ctx, scheme+rule.host)
		if err == nil {
			return nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return errors.New("panmail sink: the gateway url is templated and every allowed host is a " +
			"wildcard, so there is no host to check; add one concrete host to allowed_hosts")
	}
	return errors.Join(errs...)
}

// dial reports whether something is listening where a gateway url points.
func (s *Sink) dial(ctx context.Context, baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("panmail sink: base url is not a url: %w", err)
	}

	port := parsed.Port()
	if port == "" {
		port = "443"
		if parsed.Scheme == "http" {
			port = "80"
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(parsed.Hostname(), port))
	if err != nil {
		return fmt.Errorf("panmail sink: cannot reach the gateway at %s: %w", parsed.Host, err)
	}
	return conn.Close()
}

// Close releases nothing: the SDK client holds only an http.Client, whose idle
// connections the runtime reclaims.
func (s *Sink) Close() error { return nil }

// compose renders the configured templates against the message.
func (s *Sink) compose(msg hermod.Message) (sdk.Message, error) {
	data := renderData(msg)

	var err error
	field := func(name, tmpl string) string {
		if err != nil {
			return ""
		}
		var rendered string
		rendered, err = s.render(name, tmpl, data)
		return rendered
	}
	recipients := func(name string, tmpls []string) []string {
		if err != nil {
			return nil
		}
		var rendered []string
		rendered, err = s.renderRecipients(name, tmpls, data)
		return rendered
	}

	from := field("from", s.cfg.From)
	subject := field("subject", s.cfg.Subject)
	html := field("html body", s.cfg.HTML)
	text := field("text body", s.cfg.Text)
	to := recipients("to", s.cfg.To)
	cc := recipients("cc", s.cfg.Cc)
	bcc := recipients("bcc", s.cfg.Bcc)
	if err != nil {
		return sdk.Message{}, err
	}

	// The two routing fields are rendered through renderRequired rather than
	// field: an unroutable message is refused, not sent. A provider that
	// rendered empty would send from whatever the gateway picked, and a template
	// id that rendered empty would fall through to the body below — both are
	// mail a recipient receives and nobody meant to send.
	providerID, err := s.renderRequired("provider id", s.cfg.ProviderID, data)
	if err != nil {
		return sdk.Message{}, err
	}
	templateID := ""
	if s.cfg.TemplateID != "" {
		if templateID, err = s.renderRequired("template id", s.cfg.TemplateID, data); err != nil {
			return sdk.Message{}, err
		}
	}

	if text, err = s.bodyFallback(msg, html, text, templateID); err != nil {
		return sdk.Message{}, err
	}

	mail := sdk.Message{
		ProviderID: providerID,
		From:       from,
		To:         to,
		Cc:         cc,
		Bcc:        bcc,
		Subject:    subject,
		HTML:       html,
		Text:       text,
		TemplateID: templateID,
	}
	if templateID != "" {
		mail.TemplateData = data
	}
	return mail, nil
}

// bodyFallback fills the text part from the formatter when nothing else says
// what the mail contains. New refuses the combination where neither a body, a
// template id nor a formatter exists, so this cannot leave the mail empty.
// The template id it takes is the rendered one, not the configured template:
// "the message has a stored template" is a fact about this message.
func (s *Sink) bodyFallback(msg hermod.Message, html, text, templateID string) (string, error) {
	if html != "" || text != "" || templateID != "" || s.formatter == nil {
		return text, nil
	}
	formatted, err := s.formatter.Format(msg)
	if err != nil {
		return "", fmt.Errorf("panmail sink: format message: %w", err)
	}
	return string(formatted), nil
}

// renderData is what the templates see: the message's own fields, with its data
// map copied over the top so a column named `table` wins for the workflow that
// configured it.
//
// The envelope fields go in first for exactly that reason — writing them last
// would overwrite a row's own `table` or `id` column with the envelope's.
func renderData(msg hermod.Message) map[string]any {
	data := map[string]any{
		"id":        msg.ID(),
		"operation": string(msg.Operation()),
		"table":     msg.Table(),
		"schema":    msg.Schema(),
		"metadata":  msg.Metadata(),
	}
	maps.Copy(data, msg.Data())
	return data
}

func (s *Sink) render(field, tmpl string, data map[string]any) (string, error) {
	if tmpl == "" || !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}
	parsed, err := template.New("panmail").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("panmail sink: %s template is not valid: %w", field, err)
	}
	var buf bytes.Buffer
	if err := parsed.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("panmail sink: rendering the %s template failed: %w", field, err)
	}
	return buf.String(), nil
}

// renderRecipients renders each entry and splits the result on commas, so one
// template variable holding several addresses expands into several recipients.
// Blank results are dropped rather than sent as an empty address.
func (s *Sink) renderRecipients(field string, tmpls []string, data map[string]any) ([]string, error) {
	if len(tmpls) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(tmpls))
	for _, tmpl := range tmpls {
		rendered, err := s.render(field, tmpl, data)
		if err != nil {
			return nil, err
		}
		for part := range strings.SplitSeq(rendered, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out, nil
}

// idempotencyKey is the identity of the mail, not of the message: two messages
// that render to the same recipients, subject and body are the same mail, and
// sending both is the duplicate this guards against.
func (s *Sink) idempotencyKey(msg hermod.Message, mail sdk.Message) (string, error) {
	if s.idemKeyTemplate != "" {
		rendered, err := s.render("idempotency key", s.idemKeyTemplate, renderData(msg))
		if err != nil {
			return "", err
		}
		// "<no value>" is what text/template writes for a field the message did
		// not have. Treating it as a key would collapse every such message into
		// one, and suppress all but the first.
		if key := strings.TrimSpace(rendered); key != "" && key != noValue {
			return key, nil
		}
	}

	h := fnv.New128a()
	write := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	// Only when the gateway is templated: the same mail sent to two gateways is
	// two sends, and hashing them alike would suppress the second. A static
	// gateway is deliberately left out — folding it in would change every key
	// already derived, orphaning the claims in the store, and a message whose
	// claim went missing is a message that gets mailed a second time.
	//
	// The api key is not hashed even when templated. It is a credential, and
	// this digest is written to a database.
	if s.routed {
		gateway, err := s.render("gateway url", s.cfg.BaseURL, renderData(msg))
		if err != nil {
			return "", err
		}
		write(strings.TrimSpace(gateway))
	}
	write(msg.ID())
	write(strings.ToLower(mail.From))
	for _, r := range mail.To {
		write(strings.ToLower(r))
	}
	for _, r := range mail.Cc {
		write(strings.ToLower(r))
	}
	for _, r := range mail.Bcc {
		write(strings.ToLower(r))
	}
	write(mail.Subject)
	write(mail.HTML)
	write(mail.Text)
	write(mail.TemplateID)
	return fmt.Sprintf("panmail:%x", h.Sum(nil)), nil
}

// stated reports whether the gateway told us it did not take the message.
//
// These four are the SDK's whole refusal vocabulary, and every one of them is a
// refusal: the message was not accepted, so repeating it cannot deliver
// anything twice. Anything else — a timeout, a reset connection, a proxy that
// hung up — leaves the outcome unknown.
func stated(err error) bool {
	var (
		limited *sdk.RateLimitedError
		backlog *sdk.BacklogFullError
		auth    *sdk.AuthError
		api     *sdk.APIError
	)
	return errors.As(err, &limited) ||
		errors.As(err, &backlog) ||
		errors.As(err, &auth) ||
		errors.As(err, &api)
}

// unguardedError is what a failed send reports when there is no claim to hold.
// A stated refusal is reported as-is; an unknown outcome has to say that the
// retry which follows may deliver a second copy, because nothing here can stop
// it.
func (s *Sink) unguardedError(err error) error {
	if stated(err) {
		return fmt.Errorf("panmail sink: %w", err)
	}
	return fmt.Errorf("panmail sink: the send did not complete and its outcome is not "+
		"known; the retry that follows may deliver the same mail a second time. Turn on "+
		"idempotency to suppress that: %w", err)
}

func (s *Sink) recordAccepted(result sdk.Result) {
	s.mu.Lock()
	s.lastMessageID = result.MessageID
	s.mu.Unlock()
}
