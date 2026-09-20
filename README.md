# pgxotel

[![CI](https://github.com/b0r1ssh/pgxotel/actions/workflows/go-test.yml/badge.svg)](https://github.com/b0r1ssh/pgxotel/actions/workflows/go-test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/b0r1ssh/pgxotel.svg)](https://pkg.go.dev/github.com/b0r1ssh/pgxotel)
[![codecov](https://codecov.io/gh/b0r1ssh/pgxotel/graph/badge.svg?token=7ZA7WM97TV)](https://codecov.io/gh/b0r1ssh/pgxotel)

A small Go package providing OpenTelemetry tracing instrumentation for pgx v5.

It creates spans for PostgreSQL operations and enriches them with database,
query, connection, and error details.

## Features

- Tracing for queries, batches, prepared statements, copies, connections, and pool acquisitions
- OpenTelemetry semantic conventions for PostgreSQL span attributes
- PostgreSQL SQLSTATE error names and span status recording
- Optional query parameter, network attribute, and SQL comment controls
- Custom tracer providers and attributes

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

For example, query parameter capture must be explicitly enabled:

```go
tracer := pgxotel.NewTracer(
	pgxotel.WithQueryParameters(true),
)
```

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
