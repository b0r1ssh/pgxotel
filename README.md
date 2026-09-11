## Query collection names

`CopyFrom` spans always record `db.collection.name` because pgx provides the
target table directly.

For query, prepare, and batch query spans, pgx only provides SQL text. Enable
best-effort single-collection extraction with `WithQueryCollectionName(true)`:

```go
config.Tracer = pgxotel.NewTracer(
	pgxotel.WithQueryCollectionName(true),
)
```

For example:

```sql
SELECT * FROM authors WHERE id = $1;
```

records:

```text
db.collection.name = authors
```

The attribute is omitted when the query appears to reference multiple
collections, such as joins or `INSERT ... SELECT`, because OpenTelemetry
recommends using `db.collection.name` only for operations on a single collection.

## Span names

By default, pgxotel uses stable static span names such as `db.query`,
`db.prepare`, `db.batch`, and `db.copy`.

Enable semantic span names with `WithSpanNameMode(SpanNameSemantic)`:

```go
config.Tracer = pgxotel.NewTracer(
	pgxotel.WithSpanNameMode(pgxotel.SpanNameSemantic),
)
```

When semantic span names are enabled, pgxotel follows the OpenTelemetry fallback
shape: operation plus target when a single target is known, otherwise operation
only.

Examples:

```text
SELECT * FROM public.users WHERE id = $1  -> SELECT public.users
SELECT $1::int                            -> SELECT
SELECT * FROM authors JOIN books ON ...   -> SELECT
CopyFrom public.users                     -> COPY public.users
Batch with one collection                 -> BATCH public.users
Batch with mixed or unknown collections   -> BATCH
```

Semantic query span names are best-effort because pgx provides SQL text, not a
parsed collection name, for query, prepare, and batch query tracing.
