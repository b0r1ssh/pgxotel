package pgxotel

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

func TestSQLCQueryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sql         string
		wantName    string
		wantSQL     string
		wantMatched bool
	}{
		{
			name:        "sqlc query directive",
			sql:         "-- name: GetAuthor :one\nSELECT * FROM authors\nWHERE id = $1 LIMIT 1;",
			wantName:    "GetAuthor",
			wantSQL:     "SELECT * FROM authors\nWHERE id = $1 LIMIT 1;",
			wantMatched: true,
		},
		{
			name:        "leading blank line",
			sql:         "\n-- name: GetAuthor :one\nSELECT * FROM authors",
			wantName:    "GetAuthor",
			wantSQL:     "\nSELECT * FROM authors",
			wantMatched: true,
		},
		{
			name:        "ordinary leading comment",
			sql:         "-- Author: TechOnTheNet.com\nSELECT * FROM authors",
			wantSQL:     "-- Author: TechOnTheNet.com\nSELECT * FROM authors",
			wantMatched: false,
		},
		{
			name:        "trailing block comment",
			sql:         "SELECT * FROM authors\nWHERE id = $1 LIMIT 1;\n/* Author: TechOnTheNet.com */",
			wantSQL:     "SELECT * FROM authors\nWHERE id = $1 LIMIT 1;\n/* Author: TechOnTheNet.com */",
			wantMatched: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotName, gotSQL, gotMatched := sqlcQueryName(tt.sql)
			if gotMatched != tt.wantMatched {
				t.Fatalf("matched: got %v want %v", gotMatched, tt.wantMatched)
			}
			if gotName != tt.wantName {
				t.Fatalf("name: got %q want %q", gotName, tt.wantName)
			}
			if gotSQL != tt.wantSQL {
				t.Fatalf("sql: got %q want %q", gotSQL, tt.wantSQL)
			}
		})
	}
}

func TestQuerySummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sql         string
		wantSummary string
	}{
		{
			name:        "select collection",
			sql:         "SELECT * FROM authors WHERE id = $1 LIMIT 1;",
			wantSummary: "SELECT authors",
		},
		{
			name:        "insert collection",
			sql:         "INSERT INTO authors (name) VALUES ($1)",
			wantSummary: "INSERT authors",
		},
		{
			name:        "leading comments",
			sql:         "-- name: GetAuthor :one\n/* source: generated */\nSELECT * FROM authors WHERE id = $1 LIMIT 1;",
			wantSummary: "SELECT authors",
		},
		{
			name: "cte with join",
			sql: `WITH recent_authors AS (
	SELECT * FROM authors WHERE active = true
)
SELECT recent_authors.id
FROM recent_authors
JOIN books ON books.author_id = recent_authors.id`,
			wantSummary: "SELECT authors SELECT books",
		},
		{
			name: "anonymous table",
			sql: `SELECT order_date
FROM (SELECT *
	FROM orders o
	JOIN customers c ON o.customer_id = c.customer_id)`,
			wantSummary: "SELECT SELECT orders customers",
		},
		{
			name:        "fallback operation",
			sql:         "VACUUM",
			wantSummary: "VACUUM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotSummary, ok := querySummary(tt.sql)
			if !ok {
				t.Fatal("expected summary")
			}
			if gotSummary != tt.wantSummary {
				t.Fatalf("summary: got %q want %q", gotSummary, tt.wantSummary)
			}
		})
	}
}

func TestQueryAttributesFromSQL(t *testing.T) {
	t.Parallel()

	sql := "-- name: GetAuthor :one\nSELECT * FROM authors WHERE id = $1 LIMIT 1;"

	t.Run("generated summary keeps query text", func(t *testing.T) {
		t.Parallel()

		tracer := &Tracer{captureQuerySummary: true}
		attrs := tracer.queryAttributesFromSQL(sql)

		assertAttribute(t, attrs, "db.query.summary", "SELECT authors")
		assertAttribute(t, attrs, "db.query.text", sql)
	})

	t.Run("sqlc name can override summary", func(t *testing.T) {
		t.Parallel()

		tracer := &Tracer{captureSQLCName: true}
		attrs := tracer.queryAttributesFromSQL(sql)

		assertAttribute(t, attrs, "db.query.summary", "GetAuthor")
		assertAttribute(t, attrs, "db.query.text", sql)
	})

	t.Run("query comments can be removed from query text", func(t *testing.T) {
		t.Parallel()

		tracer := &Tracer{omitQueryComments: true, captureSQLCName: true}
		attrs := tracer.queryAttributesFromSQL(sql)

		assertAttribute(t, attrs, "db.query.summary", "GetAuthor")
		assertAttribute(t, attrs, "db.query.text", "SELECT * FROM authors WHERE id = $1 LIMIT 1;")
	})
}

func TestStripSQLComments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sql  string
		want string
	}{
		{
			name: "line and block comments",
			sql:  "-- name: GetAuthor :one\nSELECT * FROM authors /* source */ WHERE id = $1; -- trailing",
			want: "SELECT * FROM authors  WHERE id = $1;",
		},
		{
			name: "comments inside quoted strings are kept",
			sql:  "SELECT '-- not a comment', \"/* also not a comment */\" FROM authors -- trailing",
			want: "SELECT '-- not a comment', \"/* also not a comment */\" FROM authors",
		},
		{
			name: "comments inside dollar quoted strings are kept",
			sql:  "SELECT $$-- not a comment$$, $tag$/* not a comment */$tag$ FROM authors /* trailing */",
			want: "SELECT $$-- not a comment$$, $tag$/* not a comment */$tag$ FROM authors",
		},
		{
			name: "nested block comments",
			sql:  "SELECT 1 /* outer /* inner */ outer */ FROM authors",
			want: "SELECT 1  FROM authors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := stripSQLComments(tt.sql); got != tt.want {
				t.Fatalf("sql: got %q want %q", got, tt.want)
			}
		})
	}
}

func assertAttribute(t *testing.T, attrs []attribute.KeyValue, key string, value string) {
	t.Helper()

	for _, attr := range attrs {
		if string(attr.Key) == key {
			if attr.Value.AsString() != value {
				t.Fatalf("%s: got %q want %q", key, attr.Value.AsString(), value)
			}
			return
		}
	}

	t.Fatalf("missing attribute %s", key)
}
