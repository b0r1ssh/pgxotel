package pgxotel_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/b0r1sh/pgxotel"
)

const databaseURL = "postgres://pgxotel:pgxotel@localhost:5432/pgxotel"

func setupFixture(t *testing.T, opts ...pgxotel.Option) (*trace.TracerProvider, *tracetest.InMemoryExporter, *pgx.ConnConfig) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Fatalf("failed to shutdown tracer provider: %v", err)
		}
	})

	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	config.Tracer = pgxotel.NewTracer(append([]pgxotel.Option{pgxotel.WithTracerProvider(tp)}, opts...)...)

	return tp, exporter, config
}

func TestTraceConnect(t *testing.T) {
	t.Parallel()

	t.Run("without recording span", func(t *testing.T) {
		t.Parallel()

		tracer := pgxotel.NewTracer()
		tracer.TraceConnectStart(context.Background(), pgx.TraceConnectStartData{})
	})

	t.Run("without error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t, pgxotel.WithNetworkAttributes(true))

		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		conn, err := pgx.ConnectConfig(rootCtx, config)
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })

		remoteAddr, ok := conn.PgConn().Conn().RemoteAddr().(*net.TCPAddr)
		if !ok {
			t.Fatalf("expected TCP remote address, got %T", conn.PgConn().Conn().RemoteAddr())
		}

		localAddr, ok := conn.PgConn().Conn().LocalAddr().(*net.TCPAddr)
		if !ok {
			t.Fatalf("expected TCP local address, got %T", conn.PgConn().Conn().LocalAddr())
		}

		networkType := "ipv6"
		if remoteAddr.IP.To4() != nil {
			networkType = "ipv4"
		}

		span, err := getSpanByName(exporter.GetSpans(), "db.connect", map[string]any{
			"db.system.name":        "postgresql",
			"server.address":        "localhost",
			"server.port":           5432,
			"user.name":             "pgxotel",
			"db.namespace":          "pgxotel",
			"network.transport":     "tcp",
			"network.peer.address":  remoteAddr.IP.String(),
			"network.peer.port":     remoteAddr.Port,
			"network.type":          networkType,
			"network.local.address": localAddr.IP.String(),
			"network.local.port":    localAddr.Port,
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Ok {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Ok)
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t)

		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		config.DialFunc = func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial timeout")
		}
		_, err := pgx.ConnectConfig(rootCtx, config)
		if err == nil {
			t.Fatal("expected connection error")
		}
		connectionErr := err

		span, err := getSpanByName(exporter.GetSpans(), "db.connect", map[string]any{
			"db.system.name": "postgresql",
			"server.address": "localhost",
			"server.port":    5432,
			"user.name":      "pgxotel",
			"db.namespace":   "pgxotel",
			"error.type":     connectionErr.Error(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Error {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Error)
		}
	})
}

func TestTraceAcquire(t *testing.T) {
	t.Parallel()

	t.Run("without recording span", func(t *testing.T) {
		t.Parallel()

		tracer := pgxotel.NewTracer()
		tracer.TraceAcquireStart(context.Background(), nil, pgxpool.TraceAcquireStartData{})
	})

	t.Run("without error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t)
		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		poolConfig, err := pgxpool.ParseConfig(databaseURL)
		if err != nil {
			t.Fatalf("parse pool config: %v", err)
		}
		poolConfig.ConnConfig = config

		pool, err := pgxpool.NewWithConfig(rootCtx, poolConfig)
		if err != nil {
			t.Fatalf("create pool: %v", err)
		}
		t.Cleanup(func() { pool.Close() })

		conn, err := pool.Acquire(rootCtx)
		if err != nil {
			t.Fatalf("acquire connection: %v", err)
		}
		conn.Release()

		span, err := getSpanByName(exporter.GetSpans(), "db.pool.acquire", map[string]any{
			"db.system.name": "postgresql",
			"server.address": "localhost",
			"server.port":    5432,
			"user.name":      "pgxotel",
			"db.namespace":   "pgxotel",
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Ok {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Ok)
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t)
		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		config.DialFunc = func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial timeout")
		}
		poolConfig, err := pgxpool.ParseConfig(databaseURL)
		if err != nil {
			t.Fatalf("parse pool config: %v", err)
		}
		poolConfig.ConnConfig = config

		pool, err := pgxpool.NewWithConfig(rootCtx, poolConfig)
		if err != nil {
			t.Fatalf("create pool: %v", err)
		}
		t.Cleanup(func() { pool.Close() })

		_, err = pool.Acquire(rootCtx)
		if err == nil {
			t.Fatal("expected acquire error")
		}
		acquireErr := err

		span, err := getSpanByName(exporter.GetSpans(), "db.pool.acquire", map[string]any{
			"db.system.name": "postgresql",
			"server.address": "localhost",
			"server.port":    5432,
			"user.name":      "pgxotel",
			"db.namespace":   "pgxotel",
			"error.type":     acquireErr.Error(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Error {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Error)
		}
	})
}

func TestTraceBatch(t *testing.T) {
	t.Parallel()

	t.Run("without recording span", func(t *testing.T) {
		t.Parallel()

		tracer := pgxotel.NewTracer()
		tracer.TraceBatchQuery(context.Background(), nil, pgx.TraceBatchQueryData{
			SQL: "SELECT $1::int",
		})
	})

	t.Run("batch start without recording span", func(t *testing.T) {
		t.Parallel()

		tracer := pgxotel.NewTracer()
		tracer.TraceBatchStart(context.Background(), nil, pgx.TraceBatchStartData{})
	})

	t.Run("without error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t,
			pgxotel.WithQueryParameters(true),
			pgxotel.WithAttributes(attribute.String("test.attribute", "batch")),
		)
		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		conn, err := pgx.ConnectConfig(rootCtx, config)
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })

		var batch pgx.Batch
		batch.Queue("SELECT $1::int", 42)
		results := conn.SendBatch(rootCtx, &batch)
		if err := results.Close(); err != nil {
			t.Fatalf("execute batch: %v", err)
		}

		span, err := getSpanByName(exporter.GetSpans(), "db.batch", map[string]any{
			"db.system.name":            "postgresql",
			"server.address":            "localhost",
			"server.port":               5432,
			"user.name":                 "pgxotel",
			"db.namespace":              "pgxotel",
			"db.operation.batch.size":   1,
			"db.operation.name":         "SELECT",
			"db.response.returned_rows": 1,
			"db.query.text":             "SELECT $1::int",
			"db.query.parameter.0":      "42",
			"test.attribute":            "batch",
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Ok {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Ok)
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t)
		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		conn, err := pgx.ConnectConfig(rootCtx, config)
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })

		var batch pgx.Batch
		batch.Queue("SELECT * FROM missing_table")
		results := conn.SendBatch(rootCtx, &batch)
		if err := results.Close(); err == nil {
			t.Fatal("expected batch error")
		}

		span, err := getSpanByName(exporter.GetSpans(), "db.batch", map[string]any{
			"db.system.name":          "postgresql",
			"server.address":          "localhost",
			"server.port":             5432,
			"user.name":               "pgxotel",
			"db.namespace":            "pgxotel",
			"db.operation.batch.size": 1,
			"db.response.status_code": "42P01",
			"error.type":              "UndefinedTable",
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Error {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Error)
		}
	})
}

func TestTracePrepare(t *testing.T) {
	t.Parallel()

	t.Run("without recording span", func(t *testing.T) {
		t.Parallel()

		tracer := pgxotel.NewTracer()
		tracer.TracePrepareStart(context.Background(), nil, pgx.TracePrepareStartData{})
	})

	t.Run("without error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t)
		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		conn, err := pgx.ConnectConfig(rootCtx, config)
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })

		if _, err := conn.Prepare(rootCtx, "trace_prepare", "SELECT $1::int"); err != nil {
			t.Fatalf("prepare query: %v", err)
		}

		span, err := getSpanByName(exporter.GetSpans(), "db.prepare", map[string]any{
			"db.system.name": "postgresql",
			"server.address": "localhost",
			"server.port":    5432,
			"user.name":      "pgxotel",
			"db.namespace":   "pgxotel",
			"db.query.text":  "SELECT $1::int",
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Ok {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Ok)
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t)
		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		conn, err := pgx.ConnectConfig(rootCtx, config)
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })

		_, err = conn.Prepare(rootCtx, "trace_prepare_error", "SELECT * FROM missing_table")
		if err == nil {
			t.Fatal("expected prepare error")
		}

		span, err := getSpanByName(exporter.GetSpans(), "db.prepare", map[string]any{
			"db.system.name":          "postgresql",
			"server.address":          "localhost",
			"server.port":             5432,
			"user.name":               "pgxotel",
			"db.namespace":            "pgxotel",
			"db.query.text":           "SELECT * FROM missing_table",
			"db.response.status_code": "42P01",
			"error.type":              "UndefinedTable",
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Error {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Error)
		}
	})
}

func TestTraceCopyFrom(t *testing.T) {
	t.Parallel()

	t.Run("without recording span", func(t *testing.T) {
		t.Parallel()

		tracer := pgxotel.NewTracer()
		tracer.TraceCopyFromStart(context.Background(), nil, pgx.TraceCopyFromStartData{})
	})

	t.Run("without error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t)
		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		conn, err := pgx.ConnectConfig(rootCtx, config)
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })

		if _, err := conn.Exec(rootCtx, "CREATE TEMP TABLE trace_copy (value int)"); err != nil {
			t.Fatalf("create temp table: %v", err)
		}
		if _, err := conn.CopyFrom(rootCtx, pgx.Identifier{"trace_copy"}, []string{"value"}, pgx.CopyFromRows([][]any{{1}})); err != nil {
			t.Fatalf("copy rows: %v", err)
		}

		span, err := getSpanByName(exporter.GetSpans(), "db.copy", map[string]any{
			"db.system.name":     "postgresql",
			"server.address":     "localhost",
			"server.port":        5432,
			"user.name":          "pgxotel",
			"db.namespace":       "pgxotel",
			"db.collection.name": "trace_copy",
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Ok {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Ok)
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t)
		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		conn, err := pgx.ConnectConfig(rootCtx, config)
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })

		if _, err := conn.Exec(rootCtx, "CREATE TEMP TABLE trace_copy_error (value int)"); err != nil {
			t.Fatalf("create temp table: %v", err)
		}
		_, err = conn.CopyFrom(rootCtx, pgx.Identifier{"trace_copy_error"}, []string{"value"}, pgx.CopyFromRows([][]any{{"not-an-int"}}))
		if err == nil {
			t.Fatal("expected copy error")
		}

		span, err := getSpanByName(exporter.GetSpans(), "db.copy", map[string]any{
			"db.system.name":          "postgresql",
			"server.address":          "localhost",
			"server.port":             5432,
			"user.name":               "pgxotel",
			"db.namespace":            "pgxotel",
			"db.collection.name":      "trace_copy_error",
			"db.response.status_code": "57014",
			"error.type":              "QueryCanceled",
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Error {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Error)
		}
	})
}

func TestTraceQuery(t *testing.T) {
	t.Parallel()

	t.Run("without error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t,
			pgxotel.WithQueryParameters(true),
			pgxotel.WithNetworkAttributes(true),
			pgxotel.WithAttributes(attribute.String("test.attribute", "query")),
		)
		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		conn, err := pgx.ConnectConfig(rootCtx, config)
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })

		remoteAddr, ok := conn.PgConn().Conn().RemoteAddr().(*net.TCPAddr)
		if !ok {
			t.Fatalf("expected TCP remote address, got %T", conn.PgConn().Conn().RemoteAddr())
		}
		localAddr, ok := conn.PgConn().Conn().LocalAddr().(*net.TCPAddr)
		if !ok {
			t.Fatalf("expected TCP local address, got %T", conn.PgConn().Conn().LocalAddr())
		}
		networkType := "ipv6"
		if remoteAddr.IP.To4() != nil {
			networkType = "ipv4"
		}

		if _, err := conn.Exec(rootCtx, "SELECT $1::int", 42); err != nil {
			t.Fatalf("execute query: %v", err)
		}

		span, err := getSpanByName(exporter.GetSpans(), "db.query", map[string]any{
			"db.system.name":            "postgresql",
			"server.address":            "localhost",
			"server.port":               5432,
			"user.name":                 "pgxotel",
			"db.namespace":              "pgxotel",
			"db.query.text":             "SELECT $1::int",
			"db.query.parameter.0":      "42",
			"test.attribute":            "query",
			"db.operation.name":         "SELECT",
			"db.response.returned_rows": 1,
			"network.transport":         "tcp",
			"network.peer.address":      remoteAddr.IP.String(),
			"network.peer.port":         remoteAddr.Port,
			"network.type":              networkType,
			"network.local.address":     localAddr.IP.String(),
			"network.local.port":        localAddr.Port,
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Ok {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Ok)
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t)
		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		conn, err := pgx.ConnectConfig(rootCtx, config)
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })

		_, err = conn.Exec(rootCtx, "SELECT * FROM missing_table")
		if err == nil {
			t.Fatal("expected query error")
		}
		span, err := getSpanByName(exporter.GetSpans(), "db.query", map[string]any{
			"db.system.name":            "postgresql",
			"server.address":            "localhost",
			"server.port":               5432,
			"user.name":                 "pgxotel",
			"db.namespace":              "pgxotel",
			"db.query.text":             "SELECT * FROM missing_table",
			"db.operation.name":         "",
			"db.response.returned_rows": 0,
			"db.response.status_code":   "42P01",
			"error.type":                "UndefinedTable",
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Error {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Error)
		}
	})

	t.Run("with trimmed query comments", func(t *testing.T) {
		t.Parallel()

		tp, exporter, config := setupFixture(t,
			pgxotel.WithTrimQueryComments(true),
			pgxotel.WithQueryParameters(true),
		)
		rootCtx, rootSpan := tp.Tracer("tracer").Start(t.Context(), "root")
		t.Cleanup(func() { rootSpan.End() })

		conn, err := pgx.ConnectConfig(rootCtx, config)
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })

		_, err = conn.Exec(rootCtx, "SELECT /* comment */ $1::int", 42)
		if err != nil {
			t.Fatalf("execute query: %v", err)
		}
		span, err := getSpanByName(exporter.GetSpans(), "db.query", map[string]any{
			"db.system.name":            "postgresql",
			"server.address":            "localhost",
			"server.port":               5432,
			"user.name":                 "pgxotel",
			"db.namespace":              "pgxotel",
			"db.query.text":             "SELECT $1::int",
			"db.query.parameter.0":      "42",
			"db.operation.name":         "SELECT",
			"db.response.returned_rows": 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Ok {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Ok)
		}

		exporter.Reset()

		_, err = conn.Exec(rootCtx, "-- name: GetOne :one\nSELECT $1::int", 42)
		if err != nil {
			t.Fatalf("execute sqlc query: %v", err)
		}
		span, err = getSpanByName(exporter.GetSpans(), "db.query", map[string]any{
			"db.system.name":            "postgresql",
			"server.address":            "localhost",
			"server.port":               5432,
			"user.name":                 "pgxotel",
			"db.namespace":              "pgxotel",
			"db.query.text":             "SELECT $1::int",
			"db.query.parameter.0":      "42",
			"db.operation.name":         "SELECT",
			"db.response.returned_rows": 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Ok {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Ok)
		}

		exporter.Reset()

		_, err = conn.Exec(rootCtx, "SELECT $1::int -- trailing comment", 42)
		if err != nil {
			t.Fatalf("execute trailing-comment query: %v", err)
		}
		span, err = getSpanByName(exporter.GetSpans(), "db.query", map[string]any{
			"db.system.name":            "postgresql",
			"server.address":            "localhost",
			"server.port":               5432,
			"user.name":                 "pgxotel",
			"db.namespace":              "pgxotel",
			"db.query.text":             "SELECT $1::int",
			"db.query.parameter.0":      "42",
			"db.operation.name":         "SELECT",
			"db.response.returned_rows": 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Ok {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Ok)
		}

		exporter.Reset()

		_, err = conn.Exec(rootCtx, `SELECT $1::int
/*
 * Author: b0r1sh
 * Purpose: To show a comment that spans multiple lines in your SQL
 * statement in PostgreSQL.
 */`, 42)
		if err != nil {
			t.Fatalf("execute multiline-comment query: %v", err)
		}
		span, err = getSpanByName(exporter.GetSpans(), "db.query", map[string]any{
			"db.system.name":            "postgresql",
			"server.address":            "localhost",
			"server.port":               5432,
			"user.name":                 "pgxotel",
			"db.namespace":              "pgxotel",
			"db.query.text":             "SELECT $1::int",
			"db.query.parameter.0":      "42",
			"db.operation.name":         "SELECT",
			"db.response.returned_rows": 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if span.Status.Code != codes.Ok {
			t.Fatalf("unexpected status code: got %v want %v", span.Status.Code, codes.Ok)
		}
	})
}

func getSpanByName(spans tracetest.SpanStubs, name string, wantAttributes map[string]any) (tracetest.SpanStub, error) {
	for _, span := range spans {
		if span.Name != name {
			continue
		}

		if len(span.Attributes) != len(wantAttributes) || !attributesContain(span.Attributes, wantAttributes) {
			return tracetest.SpanStub{}, fmt.Errorf("span %q has unexpected attributes: got %v want %v", name, span.Attributes, wantAttributes)
		}

		return span, nil
	}

	return tracetest.SpanStub{}, fmt.Errorf("span %q not found", name)
}

func attributesContain(got []attribute.KeyValue, want map[string]any) bool {
	for key, wantValue := range want {
		found := false
		for _, kv := range got {
			if string(kv.Key) == key && attributeValueEqual(kv.Value, wantValue) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func attributeValueEqual(got attribute.Value, want any) bool {
	switch w := want.(type) {
	case string:
		return got.Type() == attribute.STRING && got.AsString() == w
	case bool:
		return got.Type() == attribute.BOOL && got.AsBool() == w
	case int64:
		return got.Type() == attribute.INT64 && got.AsInt64() == w
	case int:
		return got.Type() == attribute.INT64 && got.AsInt64() == int64(w)
	case float64:
		return got.Type() == attribute.FLOAT64 && got.AsFloat64() == w
	}
	return false
}
