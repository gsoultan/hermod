package lookup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	sourcemongodb "github.com/gsoultan/hermod/pkg/comm/source/mongodb"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/batcher"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func init() {
	transformer.Register("db_lookup", &DBLookupTransformer{
		batchers: make(map[string]*batcherEntry),
	})
}

type DBLookupTransformer struct {
	batchers   map[string]*batcherEntry
	batchersMu sync.RWMutex
}

type RegistryProvider interface {
	GetSourceConfig(ctx context.Context, id string) (storage.Source, error)
	GetOrOpenDB(src storage.Source) (*sql.DB, error)
	GetLookupCache() (map[string]any, *sync.RWMutex) // This might need a better way
}

// requireNonCDCSource rejects a source that is configured for change data
// capture. A lookup is a per-message query against a table, and running it
// against a database that is also serving logical replication puts that load
// exactly where it hurts most.
//
// The rule itself lives in hermod.SourceAllowsDirectQueries, shared with the
// batch_sql source, so the two cannot drift. What matters here is that the
// flag is opt-out: a source carrying no use_cdc key at all is a CDC source, and
// treating a missing key as "not CDC" is how this check used to pass sources
// the engine runs as replication clients.
func requireNonCDCSource(src storage.Source) error {
	if hermod.SourceAllowsDirectQueries(src.Type, src.Config) {
		return nil
	}
	return fmt.Errorf("db_lookup requires a non-CDC source; disable CDC on source '%s' or use a non-CDC source (allowed exception: SQL Server)", src.Name)
}

// NOTE: Since we need Registry for storage and DB pool, we'll assume it's passed in context
// or we need a cleaner way to provide these services.
// For now, let's look at how we can access Registry from context as previously hinted in registry.go:893

func (t *DBLookupTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	registry, ok := ctx.Value(hermod.RegistryKey).(interface {
		GetSourceConfig(ctx context.Context, id string) (storage.Source, error)
		GetOrOpenDB(src storage.Source) (*sql.DB, error)
		GetLookupCache(key string) (any, bool)
		SetLookupCache(key string, value any, ttl time.Duration)
	})

	if !ok {
		return msg, errors.New("registry not found in context")
	}

	sourceID := core.GetConfigString(config, "sourceId")
	table := core.GetConfigString(config, "table")
	keyColumn := core.GetConfigString(config, "keyColumn")
	valueColumn := core.GetConfigString(config, "valueColumn")
	keyField := core.GetConfigString(config, "keyField")
	targetField := core.GetConfigString(config, "targetField")
	ttlStr := core.GetConfigString(config, "ttl")
	whereClause := core.GetConfigString(config, "whereClause")
	defaultValue := core.GetConfigString(config, "defaultValue")
	queryTemplate := core.GetConfigString(config, "queryTemplate")
	flattenInto := core.GetConfigString(config, "flattenInto")
	mode := core.GetConfigString(config, "mode")

	// Every path below that cannot enrich used to return success, so a message
	// that missed its lookup reached the sink indistinguishable from one that
	// hit. onMiss makes that outcome an explicit, auditable choice.
	onMiss := resolveMissPolicy(config, defaultValue != "")

	if sourceID == "" || targetField == "" {
		return msg, applyMissPolicy(msg, onMiss, targetField, defaultValue,
			fmt.Errorf("db_lookup: incomplete config (sourceId=%q, targetField=%q)", sourceID, targetField))
	}

	keyVal := evaluator.GetMsgValByPath(msg, keyField)

	if keyVal == nil && queryTemplate == "" && whereClause == "" {
		return msg, applyMissPolicy(msg, onMiss, targetField, defaultValue,
			missError(table, keyField, nil))
	}

	// Parsed before the query, not after it: a ttl that cannot be parsed is a
	// cache that never expires, and finding that out only once the row is in
	// hand means the bad entry is already stored.
	ttl, err := resolveLookupTTL(ttlStr, defaultDBLookupTTL)
	if err != nil {
		return msg, fmt.Errorf("db_lookup: %w", err)
	}

	// Snapshot the message once. Every query path below resolves its templates
	// against this same map, and so does the cache key, so the key cannot
	// describe a different query from the one that runs.
	data := msg.Data()

	cacheKey := lookupCacheKey(sourceID, table, keyColumn, valueColumn, keyVal, whereClause, queryTemplate, mode, data)
	if cached, found := registry.GetLookupCache(cacheKey); found {
		applyLookupResult(msg, targetField, flattenInto, cached)
		return msg, nil
	}

	src, err := registry.GetSourceConfig(ctx, sourceID)
	if err != nil {
		return msg, fmt.Errorf("failed to get source for lookup (sourceId: '%s'): %w", sourceID, err)
	}

	// Ahead of the branch below, not inside one arm of it: a lookup that batches
	// is still a query against the source, so batching used to hand out an
	// exemption from the rule and then cache the result it was not allowed to
	// fetch. This is also deliberately outside applyMissPolicy -- the source is
	// misconfigured, which is not the same event as a lookup that found no row,
	// and a passthrough policy must not turn it into silence.
	if err := requireNonCDCSource(src); err != nil {
		return msg, err
	}

	var resultVal any
	// Use batching if enabled and applicable (only for SQL-based table mode)
	useBatching, _ := config["use_batching"].(bool)
	if !useBatching {
		if s, ok := config["use_batching"].(string); ok {
			useBatching = s == "true"
		}
	}
	nodeID, _ := ctx.Value(hermod.NodeIDKey).(string)

	// A templated whereClause is deliberately excluded. Batching coalesces many
	// messages into one query, and a WHERE that varies per message cannot be
	// coalesced: the batcher resolved it once, against whichever message created
	// it, and then filtered every later batch by that first message's values.
	// There is no version of this that both batches and filters correctly, so
	// such a node takes the per-message path below, which already resolves its
	// templates for the message in hand.
	if useBatching && nodeID != "" && mode != "query" && queryTemplate == "" &&
		src.Type != "mongodb" && !strings.Contains(whereClause, "{{") {
		batchSize := configInt(config, "batchSize")
		if batchSize <= 0 {
			batchSize = 100
		}
		batchWaitStr, _ := config["batchWait"].(string)
		batchWait := 10 * time.Millisecond
		if d, err := time.ParseDuration(batchWaitStr); err == nil {
			batchWait = d
		}

		b := t.getOrCreateBatcher(nodeID, registry, src, table, keyColumn, valueColumn, whereClause, defaultValue, batchSize, batchWait)
		resultVal, err = b.Execute(ctx, keyVal)
		if err != nil {
			return msg, err
		}
	} else {
		if src.Type == "mongodb" {
			// queryTemplate not supported for Mongo; use whereClause
			resultVal, err = t.lookupMongoDB(ctx, src, table, keyColumn, keyVal, whereClause, valueColumn, defaultValue, data)
		} else {
			// If mode is explicit, follow it. Otherwise fallback to queryTemplate presence.
			useTemplate := false
			if mode == "query" {
				useTemplate = true
			} else if mode == "table" {
				useTemplate = false
			} else if queryTemplate != "" {
				useTemplate = true
			}

			if useTemplate {
				resultVal, err = t.lookupSQLWithTemplate(ctx, registry, src, queryTemplate, valueColumn, data)
			} else {
				resultVal, err = t.lookupSQL(ctx, registry, src, table, keyColumn, keyVal, whereClause, valueColumn, defaultValue, data)
			}
		}
	}

	if err != nil {
		return msg, err
	}

	if resultVal != nil {
		if ttl.cache {
			registry.SetLookupCache(cacheKey, resultVal, ttl.duration)
		}
		applyLookupResult(msg, targetField, flattenInto, resultVal)
	} else {
		// The query ran and produced nothing. Same decision as the paths above.
		return msg, applyMissPolicy(msg, onMiss, targetField, defaultValue,
			missError(table, keyField, keyVal))
	}

	return msg, nil
}

// lookupCacheKey identifies the row a lookup is about to fetch.
//
// Everything that selects that row has to be in the key, and for a templated
// lookup most of it is not in the configuration: queryTemplate and whereClause
// carry {{ ... }} tokens whose values come from the message. Keying on the raw
// template text made the key byte-identical for every message in a workflow --
// and since an unset ttl caches forever (SetLookupCache treats ttl <= 0 as no
// expiry), the first message's row was then served to every message after it.
// The shape that hits it hardest is the one the editor produces by default:
// mode "query", a queryTemplate, and no keyField, so keyVal is nil as well.
//
// Only the resolved values are appended, not the resolved statement: they are
// what varies per message, and they go in as a digest because their size is
// bounded by nothing -- a templated blob field would otherwise become a
// megabyte-long map key.
//
// %T as well as %v throughout: without it the string "1" and the number 1
// produce the same key, so two lookups keyed on the same id in different types
// serve each other's rows.
func lookupCacheKey(sourceID, table, keyColumn, valueColumn string, keyVal any,
	whereClause, queryTemplate, mode string, data map[string]any,
) string {
	key := hermod.LookupCacheKeyPrefix(sourceID) + fmt.Sprintf("%s:%s:%s:%T:%v:%s:%s:%s",
		table, keyColumn, valueColumn, keyVal, keyVal, whereClause, queryTemplate, mode)

	binding := bindingDigest(whereClause, queryTemplate, data)
	if binding == "" {
		return key
	}
	return key + ":" + binding
}

// bindingDigest hashes the per-message values a lookup's templates resolve to,
// returning "" when neither clause is templated -- which keeps the key, and the
// cost, exactly what it was for a plain table lookup.
func bindingDigest(whereClause, queryTemplate string, data map[string]any) string {
	hasQuery := strings.Contains(queryTemplate, "{{")
	hasWhere := strings.Contains(whereClause, "{{")
	if !hasQuery && !hasWhere {
		return ""
	}

	h := sha256.New()
	if hasQuery {
		// The same walk that binds the arguments, so the digest and the
		// statement can never disagree about what the template depends on.
		for i, arg := range sqlutil.TemplateArgs(queryTemplate, data) {
			_, _ = fmt.Fprintf(h, "q%d\x00%T\x00%v\x00", i, arg, arg)
		}
	}
	if hasWhere {
		// lookupSQL parses whereClause itself, with its own AND-splitting and
		// its own per-fragment template handling, so there is no argument list
		// to reuse. Rendering the whole clause is the faithful stand-in: it is
		// a pure function of (clause, data), which is all a key needs.
		_, _ = fmt.Fprintf(h, "w\x00%s\x00", evaluator.ResolveTemplate(whereClause, data))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// applyLookupResult writes a found value into the message: at targetField, and
// -- when the value is a row rather than a single column and flattenInto is set
// -- as individual fields as well.
//
// Both the fresh-lookup path and the cache-hit path go through here. They used
// to differ: the cached path wrote targetField and returned, so flattenInto
// applied to the first message with a given key and to no message after it. In
// the editor, where the live preview re-runs on a debounce and nothing sets a
// TTL by default, that meant the flattened fields appeared once and then
// disappeared for good.
func applyLookupResult(msg hermod.Message, targetField, flattenInto string, value any) {
	msg.SetData(targetField, value)
	if flattenInto == "" {
		return
	}
	row, ok := value.(map[string]any)
	if !ok {
		return
	}
	prefix := strings.TrimSuffix(flattenInto, ".")
	for k, v := range row {
		if flattenInto == "." || prefix == "" {
			msg.SetData(k, v)
		} else {
			msg.SetData(prefix+"."+k, v)
		}
	}
}

func (t *DBLookupTransformer) lookupMongoDB(ctx context.Context, src storage.Source, table, keyColumn string, keyVal any, whereClause, valueColumn, defaultValue string, data map[string]any) (any, error) {
	uri := src.Config["uri"]
	if uri == "" {
		host := src.Config["host"]
		port := src.Config["port"]
		user := src.Config["user"]
		password := src.Config["password"]
		if user != "" && password != "" {
			uri = fmt.Sprintf("mongodb://%s:%s@%s:%s", url.QueryEscape(user), url.QueryEscape(password), host, port)
		} else {
			uri = fmt.Sprintf("mongodb://%s:%s", host, port)
		}
	}

	client, err := sourcemongodb.GetClient(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongodb for lookup: %w", err)
	}

	dbName := src.Config["database"]
	collName := table
	if collName == "" {
		collName = src.Config["collection"]
	}

	coll := client.Database(dbName).Collection(collName)
	filter := bson.M{keyColumn: keyVal}
	if whereClause != "" {
		err = json.Unmarshal([]byte(evaluator.ResolveTemplate(whereClause, data)), &filter)
		if err != nil {
			return nil, fmt.Errorf("failed to parse mongo whereClause: %w", err)
		}
	}

	var result map[string]any
	err = coll.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, fmt.Errorf("mongo lookup failed: %w", err)
	}

	var finalResult any = result
	if valueColumn != "" && valueColumn != "*" {
		finalResult = result[valueColumn]
	}
	return finalResult, nil
}

// lookupDialect is the SQL dialect the statement must be built in.
//
// It is not always src.Type. A batch_sql source is a wrapper -- it holds
// queries and a cron and delegates its connection to another source -- so the
// dialect belongs to the delegate, not to the wrapper. GetOrOpenDB already
// resolves that delegate when it opens the pool, but the statement builder read
// src.Type directly and fell through to "?" placeholders against PostgreSQL:
//
//	failed to execute lookup query: ERROR: syntax error at or near "LIMIT" (SQLSTATE 42601)
//
// which reads as the operator's SQL being wrong rather than as Hermod building
// it in the wrong dialect. The node's own picker excludes batch_sql, so this is
// reachable only through the API or an imported workflow -- the engine accepted
// a configuration the editor refuses, and then blamed the operator for it.
//
// yugabyte was missing from the same hand-written switch, for the same reason:
// it is PostgreSQL-compatible and needs $1. CanonicalDriver is the one table
// that already knows every mapping, including the ones nobody remembered here.
//
// The registry is taken as any and type-asserted rather than widened into the
// parameter: a caller that cannot resolve a delegate keeps exactly the
// behaviour it had.
func lookupDialect(ctx context.Context, registry any, src storage.Source) string {
	sourceType := src.Type
	if sourceType == "batch_sql" {
		if r, ok := registry.(interface {
			GetSourceConfig(ctx context.Context, id string) (storage.Source, error)
		}); ok {
			if delegate, err := r.GetSourceConfig(ctx, src.Config["source_id"]); err == nil && delegate.Type != "" {
				sourceType = delegate.Type
			}
		}
	}
	if driver, ok := sqlutil.CanonicalDriver(sourceType); ok {
		return driver
	}
	return sourceType
}

func (t *DBLookupTransformer) lookupSQL(ctx context.Context, registry interface {
	GetOrOpenDB(src storage.Source) (*sql.DB, error)
}, src storage.Source, table, keyColumn string, keyVal any, whereClause, valueColumn, defaultValue string, data map[string]any) (any, error) {
	db, err := registry.GetOrOpenDB(src)
	if err != nil {
		return nil, fmt.Errorf("failed to get database for lookup: %w", err)
	}
	driver := lookupDialect(ctx, registry, src)

	// Quote table and columns safely
	quotedTable, err := sqlutil.QuoteIdent(driver, table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}

	selectList := "*"
	if valueColumn != "" && valueColumn != "*" {
		cols := strings.Split(valueColumn, ",")
		qcols := make([]string, 0, len(cols))
		for _, c := range cols {
			c = strings.TrimSpace(c)
			qc, qerr := sqlutil.QuoteIdent(driver, c)
			if qerr != nil {
				return nil, fmt.Errorf("invalid column in valueColumn: %w", qerr)
			}
			qcols = append(qcols, qc)
		}
		selectList = strings.Join(qcols, ", ")
	}

	var whereParts []string
	var args []any
	nextIdx := 1
	batchMode := false

	if whereClause != "" {
		// Safe subset parser: support AND-separated expressions
		parts := core.SplitSQLConditions(whereClause)
		for _, part := range parts {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			kv := strings.SplitN(p, "=", 2)
			if len(kv) != 2 {
				return nil, fmt.Errorf("unsupported whereClause fragment: %q", p)
			}
			col := strings.TrimSpace(kv[0])
			rhs := strings.TrimSpace(kv[1])
			qcol, qerr := sqlutil.QuoteIdent(driver, col)
			if qerr != nil {
				return nil, fmt.Errorf("invalid column in whereClause: %w", qerr)
			}
			var val any
			if strings.HasPrefix(rhs, "{{") && strings.HasSuffix(rhs, "}}") && strings.Count(rhs, "{{") == 1 {
				// Single token template: preserve original type and handle nil correctly for SQL
				token := strings.TrimSpace(rhs[2 : len(rhs)-2])
				token = strings.TrimPrefix(token, ".")
				val = evaluator.GetValByPath(data, token)
			} else if strings.Contains(rhs, "{{") {
				// Evaluate template into a value string and trim any surrounding quotes
				s := evaluator.ResolveTemplate(rhs, data)
				if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
					s = strings.Trim(s, "'")
				} else if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
					s = strings.Trim(s, "\"")
				}
				val = s
			} else if strings.HasPrefix(rhs, "'") && strings.HasSuffix(rhs, "'") {
				val = strings.Trim(rhs, "'")
			} else if strings.HasPrefix(rhs, "\"") && strings.HasSuffix(rhs, "\"") {
				val = strings.Trim(rhs, "\"")
			} else {
				// Treat as raw token (number/bool/null)
				if strings.EqualFold(rhs, "NULL") {
					val = nil
				} else if i, err := strconv.ParseInt(rhs, 10, 64); err == nil {
					val = i
				} else if f, err := strconv.ParseFloat(rhs, 64); err == nil {
					val = f
				} else if b, err := strconv.ParseBool(rhs); err == nil {
					val = b
				} else {
					val = rhs
				}
			}
			// A list value means membership, not equality. Binding it to a
			// single "=" placeholder was both the wrong operator and an
			// argument no driver can encode.
			if arr, ok := asSlice(val); ok {
				if len(arr) == 0 {
					return nil, nil
				}
				phs := make([]string, 0, len(arr))
				for range arr {
					phs = append(phs, sqlutil.Placeholder(driver, nextIdx))
					nextIdx++
				}
				whereParts = append(whereParts, fmt.Sprintf("%s IN (%s)", qcol, strings.Join(phs, ", ")))
				args = append(args, arr...)
				batchMode = true
				continue
			}
			ph := sqlutil.Placeholder(driver, nextIdx)
			nextIdx++
			whereParts = append(whereParts, fmt.Sprintf("%s = %s", qcol, ph))
			args = append(args, val)
		}
		if len(whereParts) == 0 {
			return nil, errors.New("invalid whereClause: no conditions parsed")
		}
	} else if keyColumn != "" {
		qkey, qerr := sqlutil.QuoteIdent(driver, keyColumn)
		if qerr != nil {
			return nil, fmt.Errorf("invalid keyColumn: %w", qerr)
		}
		// Support batch lookup for slice/array keyVal -> IN (...)
		if arr, ok := asSlice(keyVal); ok {
			if len(arr) == 0 {
				return nil, nil
			}
			var phs []string
			for range arr {
				phs = append(phs, sqlutil.Placeholder(driver, nextIdx))
				nextIdx++
			}
			whereParts = append(whereParts, fmt.Sprintf("%s IN (%s)", qkey, strings.Join(phs, ", ")))
			args = append(args, arr...)
			batchMode = true
		} else {
			ph := sqlutil.Placeholder(driver, nextIdx)
			whereParts = append(whereParts, fmt.Sprintf("%s = %s", qkey, ph))
			args = append(args, keyVal)
		}
	} else {
		return nil, errors.New("either whereClause or keyColumn must be provided for db_lookup")
	}

	query := buildLookupQuery(driver, selectList, quotedTable, whereParts, batchMode)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute lookup query: %w", err)
	}
	defer rows.Close()

	rowsOut, err := sqlutil.ScanRows(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to scan lookup results: %w", err)
	}

	if len(rowsOut) == 0 {
		return nil, nil
	}

	isBatch := strings.Contains(strings.ToUpper(query), " IN (")

	// Single column requested.
	if valueColumn != "" && valueColumn != "*" && !strings.Contains(valueColumn, ",") {
		findValue := func(row map[string]any) any {
			if val, ok := row[valueColumn]; ok {
				return val
			}
			for k, v := range row {
				if strings.EqualFold(k, valueColumn) {
					return v
				}
			}
			return nil
		}

		if !isBatch && len(rowsOut) == 1 {
			return findValue(rowsOut[0]), nil
		}
		var results []any
		for _, row := range rowsOut {
			if val := findValue(row); val != nil {
				results = append(results, val)
			}
		}
		if len(results) == 0 {
			return nil, nil
		}
		return results, nil
	}

	if !isBatch && len(rowsOut) == 1 {
		return rowsOut[0], nil
	}
	return rowsOut, nil
}

// batcherEntry pairs a batcher with a description of what its closure captured,
// so the next message can tell whether that is still the right one.
type batcherEntry struct {
	fingerprint string
	batcher     *batcher.Batcher[any, any]
}

// batcherFingerprint covers everything getOrCreateBatcher's closure holds on to.
//
// Digested rather than concatenated because a source's config is arbitrary
// length, and this string is a value in a map that lives for the life of the
// process.
func batcherFingerprint(src storage.Source, table, keyColumn, valueColumn, whereClause, defaultValue string, batchSize int, batchWait time.Duration) string {
	h := sha256.New()
	// fmt sorts map keys, so a config map prints the same way every time.
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%v\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d",
		src.ID, src.Type, src.Config,
		table, keyColumn, valueColumn, whereClause, defaultValue,
		batchSize, batchWait)
	return hex.EncodeToString(h.Sum(nil))
}

// getOrCreateBatcher returns the batcher for a node, building a new one when
// the node has none or when what it captured no longer matches the request.
//
// It used to return the first batcher it ever built for a node id and never
// look again, which froze the source alongside it: repointing the node at
// another database, or rotating its credentials, left the batching path
// querying the old one until the process restarted. The registry drops that
// source's cache entries on an edit for exactly this reason
// (Registry.invalidateLookupCacheForSource), and this defeated it.
//
// The superseded batcher is dropped rather than closed. Close makes a
// concurrent Execute fail with context.Canceled, which would turn a
// reconfiguration into failed messages; an abandoned batcher instead flushes
// whatever it already had -- its timer is a one-shot AfterFunc -- and is then
// garbage.
func (t *DBLookupTransformer) getOrCreateBatcher(nodeID string, registry any, src storage.Source, table, keyColumn, valueColumn, whereClause, defaultValue string, batchSize int, batchWait time.Duration) *batcher.Batcher[any, any] {
	fingerprint := batcherFingerprint(src, table, keyColumn, valueColumn, whereClause, defaultValue, batchSize, batchWait)

	t.batchersMu.RLock()
	entry, ok := t.batchers[nodeID]
	t.batchersMu.RUnlock()
	if ok && entry.fingerprint == fingerprint {
		return entry.batcher
	}

	t.batchersMu.Lock()
	defer t.batchersMu.Unlock()
	// Re-check: another message may have rebuilt it while the lock was released.
	if entry, ok := t.batchers[nodeID]; ok && entry.fingerprint == fingerprint {
		return entry.batcher
	}

	b := batcher.NewBatcher(batchSize, batchWait, func(ctx context.Context, keys []any) (map[any]any, error) {
		// nil, not the creating message's data: the caller only reaches this
		// path when whereClause carries no {{ }} token, so there is nothing for
		// lookupSQL to resolve against. Passing the data through is how a single
		// message's values came to filter every batch after it.
		return t.lookupSQLBatch(ctx, registry.(interface {
			GetOrOpenDB(src storage.Source) (*sql.DB, error)
		}), src, table, keyColumn, keys, whereClause, valueColumn, defaultValue, nil)
	})

	if t.batchers == nil {
		// A transformer built without the constructor -- init() supplies the map,
		// but a zero value is otherwise perfectly usable, and assigning into a nil
		// map panics.
		t.batchers = make(map[string]*batcherEntry)
	}
	t.batchers[nodeID] = &batcherEntry{fingerprint: fingerprint, batcher: b}
	return b
}

func (t *DBLookupTransformer) lookupSQLBatch(ctx context.Context, registry interface {
	GetOrOpenDB(src storage.Source) (*sql.DB, error)
}, src storage.Source, table, keyColumn string, keys []any, whereClause, valueColumn, defaultValue string, data map[string]any) (map[any]any, error) {
	requestedCols := valueColumn
	if requestedCols == "" {
		requestedCols = "*"
	}

	// Force keyColumn into selection to allow correlation
	batchValueColumn := requestedCols
	if batchValueColumn != "*" && !strings.Contains(batchValueColumn, keyColumn) {
		batchValueColumn += "," + keyColumn
	}

	res, err := t.lookupSQL(ctx, registry, src, table, keyColumn, keys, whereClause, batchValueColumn, defaultValue, data)
	if err != nil {
		return nil, err
	}

	results := make(map[any]any)
	if res == nil {
		return results, nil
	}

	var rows []map[string]any
	if r, ok := res.(map[string]any); ok {
		rows = []map[string]any{r}
	} else if rs, ok := res.([]map[string]any); ok {
		rows = rs
	}

	for _, row := range rows {
		k := row[keyColumn]
		if k == nil {
			for rk, rv := range row {
				if strings.EqualFold(rk, keyColumn) {
					k = rv
					break
				}
			}
		}

		var finalVal any
		if requestedCols != "*" && !strings.Contains(requestedCols, ",") {
			finalVal = row[requestedCols]
			if finalVal == nil {
				for rk, rv := range row {
					if strings.EqualFold(rk, requestedCols) {
						finalVal = rv
						break
					}
				}
			}
		} else {
			finalVal = row
		}
		if k != nil {
			results[k] = finalVal
		}
	}

	return results, nil
}

// lookupSQLWithTemplate executes a full custom SELECT template while safely parameterizing any {{ ... }} tokens.
func (t *DBLookupTransformer) lookupSQLWithTemplate(ctx context.Context, registry interface {
	GetOrOpenDB(src storage.Source) (*sql.DB, error)
}, src storage.Source, queryTemplate string, valueColumn string, data map[string]any) (any, error) {
	db, err := registry.GetOrOpenDB(src)
	if err != nil {
		return nil, fmt.Errorf("failed to get database for lookup: %w", err)
	}

	driver := lookupDialect(ctx, registry, src)

	b := core.ParameterizeTemplateEx(driver, queryTemplate, data)
	if b.Err != nil {
		return nil, b.Err
	}
	sqlText, args := b.SQL, b.Args
	if strings.TrimSpace(sqlText) == "" {
		return nil, errors.New("empty queryTemplate after processing")
	}

	// Execute query and fetch results.
	// We always use rows.Scan with dynamic columns because queryTemplate is user-provided.
	rows, err := db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute lookup query: %w", err)
	}
	defer rows.Close()

	rowsOut, err := sqlutil.ScanRows(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to scan lookup results: %w", err)
	}

	if len(rowsOut) == 0 {
		return nil, nil
	}

	// Determine result based on valueColumn setting
	if valueColumn == "*" || valueColumn == "" {
		if len(rowsOut) == 1 {
			return rowsOut[0], nil
		}
		return rowsOut, nil
	}

	// Multiple columns requested via comma-separated list
	if strings.Contains(valueColumn, ",") {
		requestedCols := strings.Split(valueColumn, ",")
		for i := range requestedCols {
			requestedCols[i] = strings.TrimSpace(requestedCols[i])
		}

		filterRow := func(row map[string]any) map[string]any {
			filtered := make(map[string]any)
			for _, rc := range requestedCols {
				if val, ok := row[rc]; ok {
					filtered[rc] = val
				} else {
					// try case-insensitive
					for k, v := range row {
						if strings.EqualFold(k, rc) {
							filtered[rc] = v
							break
						}
					}
				}
			}
			return filtered
		}

		if len(rowsOut) == 1 {
			return filterRow(rowsOut[0]), nil
		}
		var filteredRows []map[string]any
		for _, row := range rowsOut {
			filteredRows = append(filteredRows, filterRow(row))
		}
		return filteredRows, nil
	}

	// Single column requested.
	// We look for it in the scanned row(s). Case-insensitive match for convenience.
	findValue := func(row map[string]any) any {
		if val, ok := row[valueColumn]; ok {
			return val
		}
		for k, v := range row {
			if strings.EqualFold(k, valueColumn) {
				return v
			}
		}
		return nil
	}

	if len(rowsOut) == 1 {
		return findValue(rowsOut[0]), nil
	}

	var results []any
	for _, row := range rowsOut {
		results = append(results, findValue(row))
	}
	return results, nil
}

// asSlice tries to coerce v into a slice of any for batch IN processing.
//
// It delegates to core.AsSlice so the lookup paths and the query-template path
// agree on what counts as a list. The hand-written type switch this replaced
// covered []any, []string, []int, []int64 and []float64 only, so a list that
// arrived as any other slice kind was bound whole to one placeholder and the
// driver rejected it.
func asSlice(v any) ([]any, bool) {
	return core.AsSlice(v)
}

func buildLookupQuery(driver, selectList, quotedTable string, whereParts []string, batchMode bool) string {
	whereJoined := strings.Join(whereParts, " AND ")
	if !batchMode {
		switch driver {
		case "mssql", "sqlserver":
			return fmt.Sprintf("SELECT TOP 1 %s FROM %s WHERE %s", selectList, quotedTable, whereJoined)
		case "oracle":
			return fmt.Sprintf("SELECT %s FROM %s WHERE %s FETCH FIRST 1 ROWS ONLY", selectList, quotedTable, whereJoined)
		default:
			return fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT 1", selectList, quotedTable, whereJoined)
		}
	}
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s", selectList, quotedTable, whereJoined)
}
