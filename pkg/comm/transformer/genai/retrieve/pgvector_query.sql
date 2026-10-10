-- Nearest documents to $1 (a pgvector literal), at most $2 rows, closest first.
-- Identifiers cannot be bound as parameters: each %s below is filled with an
-- identifier quoted by sqlutil.QuoteIdent, a fixed operator, or NULL.
SELECT %[1]s::text AS id,
       %[2]s AS score,
       %[3]s AS content,
       %[4]s AS metadata
FROM %[5]s
ORDER BY %[6]s %[7]s $1::vector
LIMIT $2
