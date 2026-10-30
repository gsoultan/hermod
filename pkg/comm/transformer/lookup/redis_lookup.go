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
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/sharedcall"
	"github.com/redis/go-redis/v9"
)

func init() {
	transformer.Register("redis_lookup", newRedisLookupTransformer())
}

// RedisLookupTransformer reads a key from Redis -- a string with GET, one hash
// field with HGET, or a whole hash with HGETALL -- and merges the value into
// the message, the way db_lookup merges a row and api_lookup a response.
type RedisLookupTransformer struct {
	calls   *sharedcall.Group
	clients *redisClients
}

func newRedisLookupTransformer() *RedisLookupTransformer {
	return &RedisLookupTransformer{
		calls:   &sharedcall.Group{},
		clients: &redisClients{open: openRedisReader},
	}
}

// defaultRedisLookupTTL bounds how long a value is reused when the node does
// not say. Redis is usually where a value is put *because* it changes, and a
// read is cheap, so this sits with api_lookup's five minutes rather than
// db_lookup's hour; the local cache is there to spare the round trip on a
// burst of messages with one key, not to outlive the data.
const defaultRedisLookupTTL = time.Minute

// defaultRedisLookupTimeout bounds one read, including the dial on first use.
const defaultRedisLookupTimeout = 5 * time.Second

// redisCommands are the reads this node can issue. Nothing else is accepted:
// the node is a lookup, and a command name that is not one of these is a
// mistake in the config, not something to forward to the server.
var redisCommands = map[string]bool{"get": true, "hget": true, "hgetall": true}

// redisConn is where a lookup reads from. It is the client pool's key, so two
// nodes pointing at the same server and database share one pool.
type redisConn struct {
	addr     string
	username string
	password string
	db       int
	tls      bool
}

// redisRequest is one read, already resolved against its message.
type redisRequest struct {
	command string
	key     string
	field   string
}

// redisReader is the one thing a lookup needs from a client: a read that
// answers (nil, nil) when the key or field does not exist.
type redisReader interface {
	read(ctx context.Context, req redisRequest) (any, error)
	Close() error
}

// redisClients holds one client per connection for the life of the process.
//
// A client is a pool, so opening one per message would open a pool per
// message. Clients are not evicted: their number is bounded by the distinct
// connections configured on redis_lookup nodes, which is small, and closing
// one another node still holds would fail that node's next read.
type redisClients struct {
	mu   sync.Mutex
	m    map[redisConn]redisReader
	open func(redisConn) redisReader
}

func (c *redisClients) get(conn redisConn) redisReader {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.m[conn]; ok {
		return r
	}
	if c.m == nil {
		c.m = map[redisConn]redisReader{}
	}
	r := c.open(conn)
	c.m[conn] = r
	return r
}

func (c *redisClients) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, r := range c.m {
		_ = r.Close()
		delete(c.m, k)
	}
}

// goRedisReader is a redisReader over a go-redis client.
type goRedisReader struct{ client *redis.Client }

func openRedisReader(conn redisConn) redisReader {
	opts := &redis.Options{
		Addr:     conn.addr,
		Username: conn.username,
		Password: conn.password,
		DB:       conn.db,
		// Without this go-redis applies its own read and write timeouts and
		// ignores the context's deadline, so the node's Timeout would not bound
		// a read against a server that has stopped answering.
		ContextTimeoutEnabled: true,
	}
	if conn.tls {
		host, _, err := net.SplitHostPort(conn.addr)
		if err != nil {
			host = conn.addr
		}
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	}
	return goRedisReader{client: redis.NewClient(opts)}
}

func (g goRedisReader) read(ctx context.Context, req redisRequest) (any, error) {
	switch req.command {
	case "hget":
		v, err := g.client.HGet(ctx, req.key, req.field).Result()
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return decodeRedisValue(v), nil
	case "hgetall":
		h, err := g.client.HGetAll(ctx, req.key).Result()
		if err != nil {
			return nil, err
		}
		// Redis answers HGETALL on a missing key with an empty hash; there is
		// no such thing as an existing empty one, so this is the miss.
		if len(h) == 0 {
			return nil, nil
		}
		out := make(map[string]any, len(h))
		for k, v := range h {
			out[k] = decodeRedisValue(v)
		}
		return out, nil
	default:
		v, err := g.client.Get(ctx, req.key).Result()
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return decodeRedisValue(v), nil
	}
}

func (g goRedisReader) Close() error { return g.client.Close() }

// decodeRedisValue merges a JSON object or array as the structure it holds,
// which is how a record is usually cached in Redis (SET key <json>).
//
// Scalars stay text even when they would parse as JSON. "4.50" would become
// 4.5, "0812" is not JSON at all and "0813" would be, and a bigint id would be
// rounded through float64 -- the same loss db_lookup's raw key binding exists
// to avoid. Redis stores strings; a scalar it hands back is one.
func decodeRedisValue(v string) any {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return v
	}
	var out any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return v
	}
	return out
}

// pickRedisValue applies valuePath to a value that was read. A path into
// something that is not an object finds nothing, so it is a miss rather than
// the whole value: the node asked for one part of it.
func pickRedisValue(v any, valuePath string) any {
	if valuePath == "" || valuePath == "." || v == nil {
		return v
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return evaluator.GetValByPath(m, valuePath)
}

// redisLookupCacheKey identifies the value this lookup will store.
//
// Everything that selects the value is in it: the server, the database, the
// resolved key and field, the path picked out of it, and who is asking -- an
// ACL user may see different keys from another. The credentials are only
// mixed into the digest, never written in the clear, for the reason
// apiLookupCacheKey gives; hashing the resolved key with them also bounds the
// size of a key built from message data. The ttl is in it for the reason
// lookupCacheKey gives.
func redisLookupCacheKey(conn redisConn, req redisRequest, valuePath string, ttl time.Duration) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "user\x00%s\x00pass\x00%s\x00tls\x00%t\x00cmd\x00%s\x00key\x00%s\x00field\x00%s\x00path\x00%s\x00ttl\x00%s\x00",
		conn.username, conn.password, conn.tls, req.command, req.key, req.field, valuePath, ttl)
	return fmt.Sprintf("redis:%s/%d:%s", conn.addr, conn.db, hex.EncodeToString(h.Sum(nil)))
}

// redisMissError names the read that found nothing, so an operator can tell
// which of several lookups in a workflow missed.
func redisMissError(req redisRequest, valuePath string) error {
	what := fmt.Sprintf("%s %s", strings.ToUpper(req.command), req.key)
	if req.command == "hget" {
		what += " " + req.field
	}
	if valuePath != "" && valuePath != "." {
		return fmt.Errorf("redis_lookup: %s found nothing at path %q", what, valuePath)
	}
	return fmt.Errorf("redis_lookup: %s found nothing", what)
}

func configBool(config map[string]any, key string) bool {
	switch v := config[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// redisReadSettings are the parts of a node's config that are wrong or right
// regardless of the message: the command, the cache ttl and the read timeout.
type redisReadSettings struct {
	command string
	ttl     lookupTTL
	timeout time.Duration
}

// readRedisSettings parses them. Each failure is a misconfiguration, and like
// an unparseable ttl it is deliberately not routed through onMiss: a
// passthrough policy must not turn it into silence.
func readRedisSettings(config map[string]any) (redisReadSettings, error) {
	command := strings.ToLower(strings.TrimSpace(core.GetConfigString(config, "command")))
	if command == "" {
		command = "get"
	}
	if !redisCommands[command] {
		return redisReadSettings{}, fmt.Errorf("redis_lookup: command %q is not one of get, hget, hgetall", command)
	}

	ttl, err := resolveLookupTTL(core.GetConfigString(config, "ttl"), defaultRedisLookupTTL)
	if err != nil {
		return redisReadSettings{}, fmt.Errorf("redis_lookup: %w", err)
	}

	timeout := defaultRedisLookupTimeout
	if s := strings.TrimSpace(core.GetConfigString(config, "timeout")); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return redisReadSettings{}, fmt.Errorf("redis_lookup: timeout %q is not a positive duration such as %q", s, "2s")
		}
		timeout = d
	}
	return redisReadSettings{command: command, ttl: ttl, timeout: timeout}, nil
}

// resolveRedisConn is the connection a node names.
//
// It is the node's, not the record's. Scoped resolution reads secrets and env
// for the message's vhost and no row data, so a payload cannot choose the
// server a lookup goes to or the password it sends there.
func resolveRedisConn(config map[string]any, msg hermod.Message) redisConn {
	return redisConn{
		addr:     strings.TrimSpace(evaluator.ResolveTemplateScoped(core.GetConfigString(config, "addr"), msg)),
		username: evaluator.ResolveTemplateScoped(core.GetConfigString(config, "username"), msg),
		password: evaluator.ResolveTemplateScoped(core.GetConfigString(config, "password"), msg),
		db:       configInt(config, "db"),
		tls:      configBool(config, "tls"),
	}
}

// incompleteRedisConfig names what a node is missing, or is nil when it has
// everything its command needs.
func incompleteRedisConfig(conn redisConn, command, keyTemp, fieldTemp, targetField string) error {
	if conn.addr != "" && keyTemp != "" && targetField != "" && (command != "hget" || fieldTemp != "") {
		return nil
	}
	return fmt.Errorf("redis_lookup: incomplete config (addr=%q, key=%q, field=%q, targetField=%q)",
		conn.addr, keyTemp, fieldTemp, targetField)
}

// sharedRead reads req, picking valuePath out of the value. Identical
// concurrent reads share one call, which outlives any one message's
// cancellation -- see package sharedcall.
func (t *RedisLookupTransformer) sharedRead(ctx context.Context, key string, conn redisConn, req redisRequest, valuePath string, timeout time.Duration) (any, error) {
	reader := t.clients.get(conn)
	return t.calls.Do(ctx, key, func(ctx context.Context) (any, error) {
		readCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		v, err := reader.read(readCtx, req)
		if err != nil {
			return nil, err
		}
		return pickRedisValue(v, valuePath), nil
	})
}

// lookupCache is the registry's lookup cache, shared by every lookup.
type lookupCache interface {
	GetLookupCache(key string) (any, bool)
	SetLookupCache(key string, value any, ttl time.Duration)
}

// redisOutput is where and how a read's result lands on the message.
type redisOutput struct {
	valuePath    string
	targetField  string
	flattenInto  string
	defaultValue string
	onMiss       missPolicy
	// onError is for a read that failed, which apiErrorPolicy decides: it
	// fails unless a default value or policy says otherwise.
	onError missPolicy
}

func (o redisOutput) miss(msg hermod.Message, cause error) error {
	return applyMissPolicy(msg, o.onMiss, o.targetField, o.defaultValue, cause)
}

func (t *RedisLookupTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	cache, ok := ctx.Value(hermod.RegistryKey).(lookupCache)
	if !ok {
		return msg, errors.New("registry not found in context")
	}

	settings, err := readRedisSettings(config)
	if err != nil {
		return msg, err
	}

	keyTemp := core.GetConfigString(config, "key")
	fieldTemp := core.GetConfigString(config, "field")
	defaultValue := core.GetConfigString(config, "defaultValue")
	out := redisOutput{
		valuePath:    core.GetConfigString(config, "valuePath"),
		targetField:  core.GetConfigString(config, "targetField"),
		flattenInto:  core.GetConfigString(config, "flattenInto"),
		defaultValue: defaultValue,
		onMiss:       resolveMissPolicy(config, defaultValue != ""),
		onError:      apiErrorPolicy(config, defaultValue != ""),
	}

	conn := resolveRedisConn(config, msg)
	if err := incompleteRedisConfig(conn, settings.command, keyTemp, fieldTemp, out.targetField); err != nil {
		return msg, out.miss(msg, err)
	}

	// A key whose token found no value is not a key anyone wrote. "user:" is
	// still a valid Redis key, and if it exists its value would be merged
	// into every message that lacks a user, so this is a miss and Redis is
	// not asked. Resolving the tokens a second time costs little next to a
	// round trip, and the key and field are short.
	if empty := evaluator.EmptyTokens(msg, keyTemp, fieldTemp); len(empty) > 0 {
		return msg, out.miss(msg,
			fmt.Errorf("redis_lookup: these tokens had no value, so no key was read: %s", strings.Join(empty, ", ")))
	}

	req := redisRequest{command: settings.command, key: evaluator.ResolveTemplateMsg(keyTemp, msg)}
	if settings.command == "hget" {
		req.field = evaluator.ResolveTemplateMsg(fieldTemp, msg)
	}
	return msg, t.lookup(ctx, msg, cache, settings, conn, req, out)
}

// lookup serves req from the cache or from Redis and merges the result into
// msg, returning the error to propagate, or nil to continue.
func (t *RedisLookupTransformer) lookup(ctx context.Context, msg hermod.Message, cache lookupCache,
	settings redisReadSettings, conn redisConn, req redisRequest, out redisOutput,
) error {
	ttl := settings.ttl
	// Built even with the cache off: it is the shared-call key below as well.
	cacheKey := redisLookupCacheKey(conn, req, out.valuePath, ttl.duration)

	if ttl.cache {
		if cached, found := cache.GetLookupCache(cacheKey); found {
			applyLookupResult(msg, out.targetField, out.flattenInto, cached)
			return nil
		}
	}

	res, err := t.sharedRead(ctx, cacheKey, conn, req, out.valuePath, settings.timeout)
	if err != nil {
		// This message was cancelled: that is not a failed lookup, and writing
		// a default value into a message nobody will deliver helps no one.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return applyMissPolicy(msg, out.onError, out.targetField, out.defaultValue,
			fmt.Errorf("redis_lookup: %s %s failed: %w", strings.ToUpper(req.command), req.key, err))
	}

	if res == nil {
		// Not cached: a miss is a fact about this message, not about Redis.
		return out.miss(msg, redisMissError(req, out.valuePath))
	}

	if ttl.cache {
		cache.SetLookupCache(cacheKey, res, ttl.duration)
	}
	applyLookupResult(msg, out.targetField, out.flattenInto, res)
	return nil
}
