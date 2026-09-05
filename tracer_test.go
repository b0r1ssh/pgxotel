package pgxotel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/b0r1sh/pgxotel"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTraceConnect(t *testing.T) {
	t.Parallel()

	config, err := pgx.ParseConfig("postgres://user:secret@localhost:5433/database")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	tests := []struct {
		name string
		err  error
		code codes.Code
		attr map[string]any
	}{
		{
			name: "without error",
			code: codes.Ok,
			attr: map[string]any{
				"db.system.name": "postgresql",
				"server.address": "localhost",
				"server.port":    5433,
				"user.name":      "user",
				"db.namespace":   "database",
			},
		},
		{
			name: "with error",
			err:  errors.New("dial timeout"),
			code: codes.Error,
			attr: map[string]any{
				"db.system.name": "postgresql",
				"server.address": "localhost",
				"server.port":    5433,
				"user.name":      "user",
				"db.namespace":   "database",
				"error.type":     "dial timeout",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exporter := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			t.Cleanup(func() {
				err := tp.Shutdown(context.Background())
				if err != nil {
					t.Fatalf("failed to shutdown tracer provider: %v", err)
				}
			})

			tr := pgxotel.NewTracer(pgxotel.WithTracerProvider(tp))

			rootCtx, rootSpan := tp.Tracer(t.Name()).Start(t.Context(), "root")
			t.Cleanup(func() { rootSpan.End() })

			ctx := tr.TraceConnectStart(rootCtx, pgx.TraceConnectStartData{ConnConfig: config})
			tr.TraceConnectEnd(ctx, pgx.TraceConnectEndData{Err: tt.err})

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("expected exactly 1 ended span, got %d", len(spans))
			}

			span := spans[0]

			if got, want := span.Status.Code, tt.code; got != want {
				t.Fatalf("unexpected status code: got %v want %v", got, want)
			}

			if got, want := span.Attributes, tt.attr; !attributesEqual(got, want) {
				t.Fatalf("unexpected attributes: got %v want %v", got, want)
			}
		})
	}

	t.Run("without parent span", func(t *testing.T) {
		t.Parallel()

		exporter := tracetest.NewInMemoryExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
		t.Cleanup(func() {
			err := tp.Shutdown(context.Background())
			if err != nil {
				t.Fatalf("failed to shutdown tracer provider: %v", err)
			}
		})

		tr := pgxotel.NewTracer(pgxotel.WithTracerProvider(tp))

		ctx := tr.TraceConnectStart(context.Background(), pgx.TraceConnectStartData{ConnConfig: config})
		tr.TraceConnectEnd(ctx, pgx.TraceConnectEndData{})

		if got := len(exporter.GetSpans()); got != 0 {
			t.Fatalf("expected no spans when parent context is not recording, got %d", got)
		}
	})
}

func attributesEqual(got []attribute.KeyValue, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}

	for _, kv := range got {
		w, ok := want[string(kv.Key)]
		if !ok {
			return false
		}

		if !attributeValueEqual(kv.Value, w) {
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
	default:
		return false
	}
}
