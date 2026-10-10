package retrieve

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/gsoultan/hermod/pkg/infra/pgxutil"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed pgvector_query.sql
var pgvectorQuerySQL string

// pgvectorTarget is the table a pgvector sink writes.
type pgvectorTarget struct {
	table, vectorColumn, idColumn, contentColumn, metadataColumn, metric string
}

// distanceOps maps a metric to pgvector's operator and to a score that is
// higher for closer rows: cosine similarity, negative L2 distance, and the
// inner product (<#> returns its negation).
var distanceOps = map[string]struct{ op, score string }{
	"cosine":        {"<=>", "1 - (%s <=> $1::vector)"},
	"l2":            {"<->", "-(%s <-> $1::vector)"},
	"inner_product": {"<#>", "-(%s <#> $1::vector)"},
}

// pgvectorQuery builds the nearest-neighbour statement for t. Every
// identifier is quoted; the vector and the limit are bound.
func pgvectorQuery(t pgvectorTarget) (string, error) {
	if t.table == "" {
		return "", errors.New("pgvector needs a table")
	}
	ops, ok := distanceOps[t.metric]
	if !ok {
		return "", fmt.Errorf("metric %q is not one of cosine, l2, inner_product", t.metric)
	}
	quote := func(name string) (string, error) {
		if name == "" {
			return "NULL", nil
		}
		q, err := sqlutil.QuoteIdent("pgx", name)
		if err != nil {
			return "", fmt.Errorf("invalid identifier %q: %w", name, err)
		}
		return q, nil
	}
	var (
		table, vec, id, content, meta string
		err                           error
	)
	for _, f := range []struct {
		dst  *string
		name string
	}{{&table, t.table}, {&vec, t.vectorColumn}, {&id, t.idColumn}, {&content, t.contentColumn}, {&meta, t.metadataColumn}} {
		if *f.dst, err = quote(f.name); err != nil {
			return "", err
		}
	}
	if vec == "NULL" || id == "NULL" {
		return "", errors.New("pgvector needs the vector and id columns")
	}
	return fmt.Sprintf(pgvectorQuerySQL, id, fmt.Sprintf(ops.score, vec), content, meta, table, vec, ops.op), nil
}

// vectorLiteral is v in pgvector's text form, [1,2,3].
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// pgvector queries the table a pgvector sink writes.
type pgvector struct {
	conn string
	stmt string
}

func newPgvector(conn string, t pgvectorTarget) (*pgvector, error) {
	q, err := pgvectorQuery(t)
	if err != nil {
		return nil, err
	}
	if _, _, err := pgxutil.ParsePoolConfig(conn); err != nil {
		return nil, errors.New("pgvector connection string cannot be parsed")
	}
	return &pgvector{conn: conn, stmt: q}, nil
}

func (p *pgvector) query(ctx context.Context, vector []float32, k int) ([]Doc, error) {
	pool, release, err := pools.get(ctx, p.conn)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := pool.Query(ctx, p.stmt, vectorLiteral(vector), k)
	if err != nil {
		return nil, fmt.Errorf("pgvector query: %w", err)
	}
	defer rows.Close()
	var docs []Doc
	for rows.Next() {
		var d Doc
		if err := rows.Scan(&d.ID, &d.Score, &d.Content, &d.Metadata); err != nil {
			return nil, fmt.Errorf("pgvector query: %w", err)
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgvector query: %w", err)
	}
	return docs, nil
}

// maxPools bounds the cached pools. A connection beyond it gets a pool for
// the one query, closed afterwards, so a pool another query is using is
// never closed under it.
const maxPools = 16

type poolCache struct {
	mu    sync.Mutex
	pools map[string]*pgxpool.Pool
}

var pools = &poolCache{pools: map[string]*pgxpool.Pool{}}

func (c *poolCache) get(ctx context.Context, conn string) (*pgxpool.Pool, func(), error) {
	sum := sha256.Sum256([]byte(conn))
	key := hex.EncodeToString(sum[:])
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.pools[key]; ok {
		return p, func() {}, nil
	}
	cfg, _, err := pgxutil.ParsePoolConfig(conn)
	if err != nil {
		return nil, nil, errors.New("pgvector connection string cannot be parsed")
	}
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("pgvector connect: %w", err)
	}
	if len(c.pools) >= maxPools {
		return p, p.Close, nil
	}
	c.pools[key] = p
	return p, func() {}, nil
}
