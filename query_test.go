package pgxotel

import "testing"

func TestQueryCollectionName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		sql            string
		wantCollection string
		wantOK         bool
	}{
		{
			name:           "select from one collection",
			sql:            "SELECT * FROM authors WHERE id = $1",
			wantCollection: "authors",
			wantOK:         true,
		},
		{
			name:           "select from schema qualified collection",
			sql:            "SELECT * FROM public.users WHERE id = $1",
			wantCollection: "public.users",
			wantOK:         true,
		},
		{
			name:           "select from quoted schema qualified collection",
			sql:            `SELECT * FROM "public"."users" WHERE id = $1`,
			wantCollection: "public.users",
			wantOK:         true,
		},
		{
			name:           "select from quoted collection with space",
			sql:            `SELECT * FROM public."user accounts" WHERE id = $1`,
			wantCollection: `public."user accounts"`,
			wantOK:         true,
		},
		{
			name:           "insert into one collection",
			sql:            "INSERT INTO authors (name) VALUES ($1)",
			wantCollection: "authors",
			wantOK:         true,
		},
		{
			name:           "update one collection",
			sql:            "UPDATE authors SET name = $1 WHERE id = $2",
			wantCollection: "authors",
			wantOK:         true,
		},
		{
			name:           "delete from one collection",
			sql:            "DELETE FROM authors WHERE id = $1",
			wantCollection: "authors",
			wantOK:         true,
		},
		{
			name:   "join omits collection",
			sql:    "SELECT * FROM authors JOIN books ON books.author_id = authors.id",
			wantOK: false,
		},
		{
			name: "cte with one real collection",
			sql: `WITH recent_authors AS (
	SELECT * FROM authors WHERE active = true
)
SELECT * FROM recent_authors`,
			wantCollection: "authors",
			wantOK:         true,
		},
		{
			name: "cte with join omits collection",
			sql: `WITH recent_authors AS (
	SELECT * FROM authors WHERE active = true
)
SELECT * FROM recent_authors JOIN books ON books.author_id = recent_authors.id`,
			wantOK: false,
		},
		{
			name:           "leading comments are ignored",
			sql:            "-- name: GetAuthor :one\n/* generated */\nSELECT * FROM authors WHERE id = $1",
			wantCollection: "authors",
			wantOK:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotCollection, gotOK := queryCollectionName(tt.sql)
			if gotOK != tt.wantOK {
				t.Fatalf("ok: got %v want %v", gotOK, tt.wantOK)
			}
			if gotCollection != tt.wantCollection {
				t.Fatalf("collection: got %q want %q", gotCollection, tt.wantCollection)
			}
		})
	}
}

func TestQueryOperationName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		sql           string
		wantOperation string
		wantOK        bool
	}{
		{
			name:          "select",
			sql:           "SELECT * FROM authors",
			wantOperation: "SELECT",
			wantOK:        true,
		},
		{
			name: "cte select",
			sql: `WITH recent_authors AS (
	SELECT * FROM authors
)
SELECT * FROM recent_authors`,
			wantOperation: "SELECT",
			wantOK:        true,
		},
		{
			name:   "unknown",
			sql:    "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotOperation, gotOK := queryOperationName(tt.sql)
			if gotOK != tt.wantOK {
				t.Fatalf("ok: got %v want %v", gotOK, tt.wantOK)
			}
			if gotOperation != tt.wantOperation {
				t.Fatalf("operation: got %q want %q", gotOperation, tt.wantOperation)
			}
		})
	}
}

func TestQuerySpanName(t *testing.T) {
	t.Parallel()

	tracer := &Tracer{spanNameMode: SpanNameSemantic}

	tests := []struct {
		name     string
		sql      string
		fallback string
		want     string
	}{
		{
			name:     "single collection",
			sql:      "SELECT * FROM public.users WHERE id = $1",
			fallback: spanQuery,
			want:     "SELECT public.users",
		},
		{
			name:     "without collection",
			sql:      "SELECT $1::int",
			fallback: spanQuery,
			want:     "SELECT",
		},
		{
			name:     "join without single collection",
			sql:      "SELECT * FROM authors JOIN books ON books.author_id = authors.id",
			fallback: spanQuery,
			want:     "SELECT",
		},
		{
			name:     "static mode",
			sql:      "SELECT * FROM users",
			fallback: spanQuery,
			want:     spanQuery,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			testTracer := tracer
			if tt.name == "static mode" {
				testTracer = &Tracer{}
			}

			if got := testTracer.querySpanName(tt.sql, tt.fallback); got != tt.want {
				t.Fatalf("span name: got %q want %q", got, tt.want)
			}
		})
	}
}
