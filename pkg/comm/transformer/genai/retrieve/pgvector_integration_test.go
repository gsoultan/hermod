//go:build integration

package retrieve

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The pgvector store against a real server with the extension loaded, on a
// table shaped like the one the pgvector sink creates plus a content column.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 PGVECTOR_DSN='postgres://postgres:postgres@127.0.0.1:5432/hermod_it?sslmode=disable' \
//	go test -tags=integration ./pkg/comm/transformer/genai/retrieve/
func TestPgvectorStore_ReturnsTheNearestRowsClosestFirst(t *testing.T) {
	dsn := os.Getenv("PGVECTOR_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and PGVECTOR_DSN to run")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(t.Context(), "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Fatalf("the vector extension is not available on this server: %v", err)
	}
	const table = "ai_retrieve_it"
	drop := func() { _, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) }
	drop()
	t.Cleanup(drop)
	for _, stmt := range []string{
		"CREATE TABLE " + table + " (id TEXT PRIMARY KEY, embedding vector(3), body TEXT, metadata JSONB)",
		"INSERT INTO " + table + ` VALUES
			('near', '[1,0,0]', 'refunds take 5 days', '{"lang":"en"}'),
			('mid',  '[1,1,0]', 'shipping is free',    '{"lang":"en"}'),
			('far',  '[0,0,1]', 'opening hours',       NULL)`,
	} {
		if _, err := pool.Exec(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}

	s, err := newPgvector(dsn, pgvectorTarget{
		table: table, vectorColumn: "embedding", idColumn: "id", contentColumn: "body", metadataColumn: "metadata", metric: "cosine",
	})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := s.query(t.Context(), []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].ID != "near" || docs[1].ID != "mid" {
		t.Fatalf("docs = %+v", docs)
	}
	if docs[0].Score < 0.99 || docs[0].Score <= docs[1].Score {
		t.Fatalf("scores = %v, %v", docs[0].Score, docs[1].Score)
	}
	if docs[0].Content != "refunds take 5 days" {
		t.Fatalf("content = %#v", docs[0].Content)
	}
	if meta, _ := docs[0].Metadata.(map[string]any); meta["lang"] != "en" {
		t.Fatalf("metadata = %#v", docs[0].Metadata)
	}
}
