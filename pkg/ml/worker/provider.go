package worker

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"
)

// Source says where the worker in force was configured.
type Source string

const (
	// SourceNone means no worker is configured.
	SourceNone Source = "none"
	// SourceEnvironment is HERMOD_ML_WORKER_URL. It wins over a saved worker,
	// so a deployment that pins its worker in its manifest keeps it.
	SourceEnvironment Source = "environment"
	// SourceSaved is a worker connected from the console and kept in
	// Hermod's storage.
	SourceSaved Source = "saved"
	// SourceBuiltin is Hermod's built-in engine (pkg/ml/builtin) alone, in
	// force when no worker is configured.
	SourceBuiltin Source = "builtin"
)

// Provider answers the ML worker in force: the environment's, read once,
// else the one saved in Hermod's storage, which can change while Hermod runs.
// It is safe for concurrent use.
type Provider struct {
	env func() *Client
	now func() time.Time

	mu      sync.Mutex
	saved   *Client
	builtin *Client
	// route combines the built-in engine with a worker (SetEngine); routed
	// is its client for the worker routedFor, kept while that is in force.
	route     func(remote *Client) *Client
	routed    *Client
	routedFor *Client
	checked time.Time
	loading bool
	// gen counts SetSaved calls, so a read that started before one does not
	// put back the worker it replaced.
	gen uint64
}

// NewProvider returns a provider whose environment's worker is env, called
// at most once.
func NewProvider(env func() *Client) *Provider {
	return &Provider{env: sync.OnceValue(env), now: time.Now}
}

// Default is the process's provider. Every caller that needs the worker asks
// it, so a worker saved from the console reaches training, datasets and the
// Collect Dataset sink at once.
var Default = NewProvider(FromEnv)

// Env returns the environment's worker, or nil.
func (p *Provider) Env() *Client { return p.env() }

// Current returns the engines in force and where the worker among them was
// configured: the environment's worker, else the saved one, else none. With
// the built-in engine installed by SetEngine, the client is that engine
// routing to the worker, so both serve at once (SourceBuiltin when there is
// no worker); without it, the client is the worker, else the built-in
// engine of SetBuiltin, else nil and SourceNone.
func (p *Provider) Current() (*Client, Source) {
	remote, src := p.env(), SourceEnvironment
	p.mu.Lock()
	defer p.mu.Unlock()
	if remote == nil {
		remote, src = p.saved, SourceSaved
	}
	if remote == nil {
		src = SourceBuiltin
	}
	switch {
	case p.route != nil && remote == nil:
		return p.builtin, src
	case p.route != nil:
		if p.routed == nil || p.routedFor != remote {
			p.routed, p.routedFor = p.route(remote), remote
		}
		return p.routed, src
	case remote != nil:
		return remote, src
	case p.builtin != nil:
		return p.builtin, SourceBuiltin
	}
	return nil, SourceNone
}

// SetEngine installs Hermod's built-in engine: route returns its client
// routing to the worker remote, or alone for a nil remote. It is always in
// force, beside any worker; nil removes it.
func (p *Provider) SetEngine(route func(remote *Client) *Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.route, p.routed, p.routedFor = route, nil, nil
	p.builtin = nil
	if route != nil {
		p.builtin = route(nil)
	}
}

// SetBuiltin installs an in-process engine's client (NewLocal), in force
// only while no worker is configured; nil means Hermod has none. SetEngine
// installs the built-in engine beside the worker instead.
func (p *Provider) SetBuiltin(c *Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.builtin = c
}

// Builtin returns the built-in engine's client, or nil.
func (p *Provider) Builtin() *Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.builtin
}

// SetSaved installs the saved worker; nil means none is saved. It counts as
// a fresh read for Refresh.
func (p *Provider) SetSaved(c *Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saved, p.checked = c, p.now()
	p.gen++
}

// Refresh re-reads the saved worker with load when the last read is at least
// every old and no other read is running, so another Hermod process sharing
// the database picks up a worker saved there. A failed read keeps the worker
// it has and is returned. Nothing is read while the environment names a
// worker, since that one wins.
func (p *Provider) Refresh(ctx context.Context, every time.Duration, load func(context.Context) (*Client, error)) error {
	if p.env() != nil {
		return nil
	}
	p.mu.Lock()
	if p.loading || (!p.checked.IsZero() && p.now().Sub(p.checked) < every) {
		p.mu.Unlock()
		return nil
	}
	p.loading = true
	gen := p.gen
	p.mu.Unlock()

	c, err := load(ctx)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.loading = false
	if err != nil {
		p.checked = p.now()
		return err
	}
	if p.gen == gen {
		p.saved, p.checked = c, p.now()
	}
	return nil
}

// Configure returns a client for the worker at baseURL, with
// HERMOD_ML_TRAIN_TIMEOUT applied when set, on Hermod's data client.
func Configure(baseURL, token string) *Client {
	c := New(strings.TrimSpace(baseURL), token, nil)
	if d, err := time.ParseDuration(os.Getenv("HERMOD_ML_TRAIN_TIMEOUT")); err == nil && d > 0 {
		c.TrainTimeout = d
	}
	return c
}
