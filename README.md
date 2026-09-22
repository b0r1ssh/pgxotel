# pgxotel

[![CI](https://github.com/b0r1ssh/pgxotel/actions/workflows/go-test.yml/badge.svg)](https://github.com/b0r1ssh/pgxotel/actions/workflows/go-test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/b0r1ssh/pgxotel.svg)](https://pkg.go.dev/github.com/b0r1ssh/pgxotel)
[![codecov](https://codecov.io/gh/b0r1ssh/pgxotel/graph/badge.svg?token=7ZA7WM97TV)](https://codecov.io/gh/b0r1ssh/pgxotel)

A small Go package providing OpenTelemetry tracing instrumentation for pgx v5.
It creates client spans for PostgreSQL operations and records attributes from
the OpenTelemetry PostgreSQL semantic conventions.

The instrumentation scope is `github.com/b0r1sh/pgxotel`. Spans are created
with the instrumentation version reported by this module.

## Features

- Tracing for queries, batches, prepared statements, copies, connections, and pool acquisitions
- OpenTelemetry semantic conventions for PostgreSQL span attributes
- PostgreSQL SQLSTATE error names and span status recording
- Optional query parameter, network attribute, and SQL comment controls
- Optional span names that follow the database semantic conventions
- Custom tracer providers and attributes

## OpenTelemetry conventions

The implementation follows the [OpenTelemetry PostgreSQL semantic
conventions](https://opentelemetry.io/docs/specs/semconv/db/postgresql/) and
the [database client span conventions](https://opentelemetry.io/docs/specs/semconv/db/database-spans/).
The emitted attributes currently include:

| Attribute | When it is emitted |
| --- | --- |
| `db.system.name = postgresql` | All database and connection spans |
| `server.address`, `server.port` | From the pgx connection configuration |
| `db.namespace` | `{database}\|{schema}`, when available |
| `user.name` | The configured PostgreSQL user, when available |
| `db.query.text` | Query, batch item, and prepare spans |
| `db.operation.name` | The PostgreSQL command tag, when available |
| `db.response.returned_rows` | Query and batch spans |
| `db.response.status_code` | Failed PostgreSQL operations with a SQLSTATE |
| `error.type` | Failed operations; PostgreSQL errors use the SQLSTATE name or code, other errors use a low-cardinality Go error type |
| `db.collection.name` | `COPY` operations when the target table is known |
| `db.operation.batch.size` | Batch spans |

`db.namespace` is set to `{database}|{schema}` using the first schema in
`search_path` when it was provided at connection time (for example via
`options=-c search_path=...` in the connection string), falling back to the
database name alone otherwise. It is not updated if the search path changes
later in the connection's lifetime, since tracking that would require an extra
query per operation.

The span kind is `CLIENT`. Successful operations are marked `OK`; failures are
recorded with an exception event, `ERROR` status, and SQLSTATE attributes when
PostgreSQL provides them. For non-PostgreSQL errors (connection failures,
context cancellation, and similar), `error.type` is the Go type of the
innermost wrapped error (for example `*net.OpError`) rather than the error
message, keeping the attribute low-cardinality as the conventions require.

Span names default to fixed values (`db.query`, `db.prepare`, `db.copy`,
`db.batch`, `db.connect`, `db.pool.acquire`). Enabling `WithSemanticSpanNames`
names query, prepare, and copy spans `{db.operation.name}` (for example
`SELECT`), or `COPY {db.collection.name}` for copies, falling back to the
fixed name when the operation can't be determined from the SQL text. Batch
spans are named `BATCH`, since a batch can contain multiple operations.

## Installation

```sh
go get github.com/b0r1sh/pgxotel
```

## Set up tracing with pgx

Create an OpenTelemetry tracer provider, attach a `pgxotel.Tracer` to the pgx
configuration, and pass the active context to each database operation. This
example uses the stdout exporter so it can be run without a collector:

```sh
go get go.opentelemetry.io/otel/exporters/stdout/stdouttrace
```

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/b0r1sh/pgxotel"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

func main() {
	ctx := context.Background()

	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatal(err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName("pgx-example")),
	)
	if err != nil {
		log.Fatal(err)
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	defer func() {
		if err := tracerProvider.Shutdown(ctx); err != nil {
			log.Printf("shut down tracer provider: %v", err)
		}
	}()
	otel.SetTracerProvider(tracerProvider)

	config, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}

	config.ConnConfig.Tracer = pgxotel.NewTracer(
		pgxotel.WithTracerProvider(tracerProvider),
		pgxotel.WithAttributes(attribute.String("app.component", "repository")),
		pgxotel.WithNetworkAttributes(true),
		pgxotel.WithTrimQueryComments(true),
	)

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	ctx, span := tracerProvider.Tracer("example").Start(ctx, "read-user")
	defer span.End()

	var name string
	if err := pool.QueryRow(ctx, "SELECT name FROM users WHERE id = $1", 42).Scan(&name); err != nil {
		log.Fatal(err)
	}
}
```

Replace the stdout exporter with the exporter used by your observability
backend in production. The same tracer works with a direct `pgx.Conn`; assign
it to `pgx.ConnConfig.Tracer` before connecting:

```go
config, err := pgx.ParseConfig(databaseURL)
if err != nil {
	return err
}
config.Tracer = pgxotel.NewTracer()

conn, err := pgx.ConnectConfig(ctx, config)
```

The instrumentation creates spans only when the context passed to pgx contains
an active, recording span. Always propagate the request or job context instead
of replacing it with `context.Background()` at the database call site.

## Options

Pass options to `pgxotel.NewTracer`. All options are optional.

| Option | Default | Behavior |
| --- | --- | --- |
| `WithTracerProvider(provider)` | Global OpenTelemetry tracer provider | Uses the supplied provider to create pgx spans. This is useful when the provider is not registered globally. |
| `WithAttributes(attrs...)` | No additional attributes | Adds the attributes to every span created by the tracer. Multiple calls accumulate attributes. |
| `WithQueryParameters(enabled)` | `false` | Records query arguments as `db.query.parameter.<index>` attributes. Enable only after reviewing the risk of exposing credentials, personal data, or other sensitive values. |
| `WithNetworkAttributes(enabled)` | `false` | Adds network peer and local address attributes to connection and database operation spans. |
| `WithTrimQueryComments(enabled)` | `false` | Removes line and block comments from `db.query.text` when enabled. The remaining SQL whitespace is normalized. |
| `WithSemanticSpanNames(enabled)` | `false` | Names query, prepare, copy, and batch spans per the OpenTelemetry database semantic conventions instead of the fixed `db.query`/`db.prepare`/`db.copy`/`db.batch` names. |

Query text is captured by default. Parameterized SQL text is generally safe to
capture because values remain separate, but this package does not sanitize
literal values in non-parameterized SQL. The PostgreSQL conventions recommend
sanitizing such text before collection, so review your queries and telemetry
access policy before enabling this instrumentation for sensitive workloads.

`WithQueryParameters(true)` records values as
`db.query.parameter.0`, `db.query.parameter.1`, and so on for regular query
spans. Parameter capture is disabled by default and is never added to batch
spans, as required by the PostgreSQL semantic conventions. Values may contain
credentials, personal data, or other sensitive information.

`WithNetworkAttributes(true)` adds the observed peer and local socket
attributes (`network.*`). The connection configuration attributes above are
always recorded when available; network attributes require an established
connection and are therefore added only when the socket can be inspected.

For example, query parameter capture must be explicitly enabled:

```go
tracer := pgxotel.NewTracer(
	pgxotel.WithQueryParameters(true),
)
```

## Local development

The integration tests expect PostgreSQL at the URL used in
`tracer_test.go`. Start the included database and run the tests with:

```sh
docker compose up -d --wait postgres
go test -race ./...
docker compose down -v
```

For unit-style usage without PostgreSQL, use a non-recording context or an
in-memory OpenTelemetry exporter in your own tests. The tracer intentionally
does not create child spans unless the context passed to pgx contains an active
recording span.

## Compatibility and scope

- Requires Go 1.25 or newer and pgx v5.
- This package provides tracing instrumentation only; it does not provide
	OpenTelemetry metrics for PostgreSQL client operations.
- `WithSemanticSpanNames` derives the span name from the leading SQL keyword
	only; it does not parse SQL to derive a full `db.query.summary` or extract
	table names from query text.
- `db.namespace` reflects the schema active in `search_path` at connection
	time only; it is not re-evaluated if the application changes the search
	path later on the same connection.
- Query parameter values are converted to strings using Go formatting. They are
	not normalized or redacted.
- A pgx batch callback is represented as one batch span even when the batch
  contains one operation; the semantic conventions recommend modeling a
  single-operation batch as a regular database operation. `db.operation.name`
  and `db.response.returned_rows` on a batch span currently reflect only the
  last item in the batch.

See the [OpenTelemetry PostgreSQL conventions](https://opentelemetry.io/docs/specs/semconv/db/postgresql/)
for the requirements that apply to downstream exporters and collectors.

## Recommended next improvements

For closer alignment with the current conventions, the next implementation
steps would be:

1. Add opt-in SQL literal sanitization, or make sanitized query text the
	default for non-parameterized statements.
2. Add low-cardinality `db.query.summary` generation and use it as the span
	name target instead of the leading-keyword heuristic used today.
3. Aggregate `db.operation.name` and `db.response.returned_rows` across all
	items in a batch (for example `BATCH SELECT` when homogeneous, `BATCH`
	otherwise, and a summed row count) instead of reporting only the last item.
4. Re-evaluate `db.namespace` when the application changes `search_path` on an
	existing connection, or expose an explicit namespace override option.
5. Record the `db.client.operation.exception` event described by the
	[database exceptions conventions](https://opentelemetry.io/docs/specs/semconv/db/database-exceptions/)
	in addition to the standard OpenTelemetry `exception` event.
6. Add PostgreSQL client metrics such as operation duration and connection pool
	usage.

## Release workflow for contributors

Merging a pull request into `main` automatically creates a Git tag and a
GitHub release. Before merging, use at most one of these labels to select the
semantic version increment:

- `patch`: bug fixes and other backward-compatible changes (`v1.2.3` to
	`v1.2.4`).
- `minor`: new backward-compatible functionality (`v1.2.3` to `v1.3.0`).
- `major`: breaking API changes (`v1.2.3` to `v2.0.0`).

When no release label is present, the workflow uses `patch`. Do not combine
`patch`, `minor`, and `major` labels on one pull request; the release workflow
will fail if more than one is present.

Use the `skip-release` label for documentation, CI, or other changes that do
not need a new module version. When a pull request with this label is merged,
the publish workflow skips the release entirely.
