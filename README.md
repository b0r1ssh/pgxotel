## Query text and summaries

By default, pgxotel records `db.query.text` as the SQL string passed to pgx. SQL
comments are kept because they are part of the query text.

Use `WithQueryComments(false)` to remove SQL comments from `db.query.text`:

```go
config.Tracer = pgxotel.NewTracer(
	pgxotel.WithQueryComments(false),
)
```

This removes line comments, block comments, sqlc directives, and SQL commenter
comments while preserving comment-like text inside quoted strings.

Enable low-cardinality query summaries with `WithQuerySummary(true)`:

```go
config.Tracer = pgxotel.NewTracer(
	pgxotel.WithQuerySummary(true),
)
```

For example, this query text:

```sql
SELECT * FROM authors WHERE id = $1 LIMIT 1;
```

records a `db.query.summary` similar to:

```text
SELECT authors
```

This follows the OpenTelemetry semantic convention examples such as
`SELECT wuser_table` and `INSERT shipping_details SELECT orders`. Summaries can
also be stable application labels, such as `get user by id`, when the
instrumentation has that label available.

Queries with CTEs preserve the SQL operations and targets that affect the query:

```sql
WITH recent_authors AS (
	SELECT * FROM authors WHERE active = true
)
SELECT recent_authors.id
FROM recent_authors
JOIN books ON books.author_id = recent_authors.id;
```

records a `db.query.summary` similar to:

```text
SELECT authors SELECT books
```

sqlc query name directives can be handled explicitly:

```sql
-- name: GetAuthor :one
SELECT * FROM authors WHERE id = $1 LIMIT 1;
```

Use `WithSQLCQueryName(true)` to use `GetAuthor` as `db.query.summary`. This fits
the application-label form of `db.query.summary` and is opt-in because sqlc names
are not database syntax. Use `WithQueryComments(false)` to remove the sqlc
directive from `db.query.text`.

Do not use `db.stored_procedure.name` for sqlc query names. That attribute is for
actual database stored procedures, such as `CALL get_author($1)`.
