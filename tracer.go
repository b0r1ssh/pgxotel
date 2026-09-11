package pgxotel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/b0r1ssh/pgcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	ScopeName = "github.com/b0r1sh/pgxotel"
	Version   = "0.1.0"

	spanConnect = "db.connect"
	spanAcquire = "db.pool.acquire"
	spanCopy    = "db.copy"
	spanPrepare = "db.prepare"
	spanBatch   = "db.batch"
	spanQuery   = "db.query"
)

var (
	_ pgx.QueryTracer       = (*Tracer)(nil)
	_ pgx.ConnectTracer     = (*Tracer)(nil)
	_ pgx.PrepareTracer     = (*Tracer)(nil)
	_ pgx.CopyFromTracer    = (*Tracer)(nil)
	_ pgxpool.AcquireTracer = (*Tracer)(nil)
	_ pgx.BatchTracer       = (*Tracer)(nil)
)

type Tracer struct {
	tracer trace.Tracer

	attributes          []attribute.KeyValue
	captureQueryParams  bool
	captureNetworkAttrs bool
	omitQueryComments   bool
	captureQuerySummary bool
	captureSQLCName     bool
}

func NewTracer(opts ...Option) *Tracer {
	o := newOptions(opts...)

	tracer := o.tracerProvider.Tracer(ScopeName, trace.WithInstrumentationVersion(Version))

	return &Tracer{
		tracer:              tracer,
		attributes:          o.attributes,
		captureQueryParams:  o.captureQueryParams,
		captureNetworkAttrs: o.captureNetworkAttrs,
		omitQueryComments:   o.omitQueryComments,
		captureQuerySummary: o.captureQuerySummary,
		captureSQLCName:     o.captureSQLCName,
	}
}

// TraceConnectStart implements [pgx.ConnectTracer].
func (t *Tracer) TraceConnectStart(ctx context.Context, data pgx.TraceConnectStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	attrs := append(t.attributes, connectionAttributesFromPgxConfig(data.ConnConfig)...)

	spanCtx, _ := t.tracer.Start(ctx, spanConnect,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)

	return spanCtx
}

// TraceConnectEnd implements [pgx.ConnectTracer].
func (t *Tracer) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	defer span.End()

	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
		span.SetAttributes(semconv.ErrorTypeKey.String(pgErrType(data.Err)))
	} else {
		if t.captureNetworkAttrs {
			span.SetAttributes(networkPeerAttributesFromConn(data.Conn)...)
		}
		span.SetStatus(codes.Ok, "")
	}
}

// TraceAcquireStart implements [pgxpool.AcquireTracer].
func (t *Tracer) TraceAcquireStart(ctx context.Context, pool *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	attrs := append(t.attributes, connectionAttributesFromPgxConfig(pool.Config().ConnConfig)...)

	spanCtx, _ := t.tracer.Start(ctx, spanAcquire,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)

	return spanCtx
}

// TraceAcquireEnd implements [pgxpool.AcquireTracer].
func (t *Tracer) TraceAcquireEnd(ctx context.Context, pool *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	defer span.End()

	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
		span.SetAttributes(semconv.ErrorTypeKey.String(pgErrType(data.Err)))
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// TraceCopyFromStart implements [pgx.CopyFromTracer].
func (t *Tracer) TraceCopyFromStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	attrs := append(t.attributes, connectionAttributesFromPgxConfig(conn.Config())...)
	attrs = append(attrs, collectAttributeFrom(data.TableName))

	spanCtx, _ := t.tracer.Start(ctx, spanCopy,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)

	return spanCtx
}

// TraceCopyFromEnd implements [pgx.CopyFromTracer].
func (t *Tracer) TraceCopyFromEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceCopyFromEndData) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	defer span.End()

	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
		span.SetAttributes(semconv.ErrorTypeKey.String(pgErrType(data.Err)))
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// TracePrepareStart implements [pgx.PrepareTracer].
func (t *Tracer) TracePrepareStart(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	attrs := append(t.attributes, connectionAttributesFromPgxConfig(conn.Config())...)
	attrs = append(attrs, t.queryAttributesFromSQL(data.SQL)...)

	spanCtx, _ := t.tracer.Start(ctx, spanPrepare,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)

	return spanCtx
}

// TracePrepareEnd implements [pgx.PrepareTracer].
func (t *Tracer) TracePrepareEnd(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareEndData) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	defer span.End()

	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
		span.SetAttributes(semconv.ErrorTypeKey.String(pgErrType(data.Err)))
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// TraceBatchStart implements [pgx.BatchTracer].
func (t *Tracer) TraceBatchStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	attrs := append(t.attributes, connectionAttributesFromPgxConfig(conn.Config())...)

	size := 0
	if b := data.Batch; b != nil {
		size = b.Len()
	}
	attrs = append(attrs, semconv.DBOperationBatchSize(size))

	spanCtx, _ := t.tracer.Start(ctx, spanBatch,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)

	return spanCtx
}

// TraceBatchEnd implements [pgx.BatchTracer].
func (t *Tracer) TraceBatchEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchEndData) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	defer span.End()

	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
		span.SetAttributes(semconv.ErrorTypeKey.String(pgErrType(data.Err)))
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

func (t *Tracer) TraceBatchQuery(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchQueryData) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	attrs := append(t.attributes, connectionAttributesFromPgxConfig(conn.Config())...)
	attrs = append(attrs, operationAttributeFromCommandTag(data.CommandTag))
	attrs = append(attrs, retunredRowsAttributeFromCommandTag(data.CommandTag))
	attrs = append(attrs, t.queryAttributesFromSQL(data.SQL)...)

	if t.captureQueryParams {
		attrs = append(attrs, queryParameterAttributesFromArgs(data.Args)...)
	}

	span.SetAttributes(attrs...)
}

// TraceQueryStart implements [pgx.QueryTracer].
func (t *Tracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	attrs := append(t.attributes, connectionAttributesFromPgxConfig(conn.Config())...)
	attrs = append(attrs, t.queryAttributesFromSQL(data.SQL)...)

	if t.captureQueryParams {
		attrs = append(attrs, queryParameterAttributesFromArgs(data.Args)...)
	}

	spanCtx, _ := t.tracer.Start(ctx, spanQuery,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)

	return spanCtx
}

// TraceQueryEnd implements [pgx.QueryTracer].
func (t *Tracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	defer span.End()

	span.SetAttributes(operationAttributeFromCommandTag(data.CommandTag))
	span.SetAttributes(retunredRowsAttributeFromCommandTag(data.CommandTag))

	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
		span.SetAttributes(semconv.ErrorTypeKey.String(pgErrType(data.Err)))
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

func connectionAttributesFromPgxConfig(config *pgx.ConnConfig) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0)

	if config != nil {
		attrs = append(attrs, semconv.DBSystemNamePostgreSQL)

		attrs = append(attrs, semconv.ServerAddress(config.Host))
		attrs = append(attrs, semconv.ServerPort(int(config.Port)))

		if config.User != "" {
			attrs = append(attrs, semconv.UserName(config.User))
		}

		if config.Database != "" {
			attrs = append(attrs, semconv.DBNamespace(config.Database))
		}
	}

	return attrs
}

func networkPeerAttributesFromConn(conn *pgx.Conn) []attribute.KeyValue {
	if conn == nil || conn.PgConn() == nil || conn.PgConn().Conn() == nil {
		return nil
	}

	netConn := conn.PgConn().Conn()
	remoteAddr := netConn.RemoteAddr()
	localAddr := netConn.LocalAddr()
	attrs := make([]attribute.KeyValue, 0, 2)

	switch remoteAddr := remoteAddr.(type) {
	case *net.TCPAddr:
		attrs = append(attrs,
			semconv.NetworkTransportTCP,
			semconv.NetworkPeerAddress(remoteAddr.IP.String()),
			semconv.NetworkPeerPort(remoteAddr.Port),
		)
		if remoteAddr.IP.To4() != nil {
			attrs = append(attrs, semconv.NetworkTypeIPv4)
		} else {
			attrs = append(attrs, semconv.NetworkTypeIPv6)
		}
	case *net.UnixAddr:
		attrs = append(attrs,
			semconv.NetworkTransportUnix,
			semconv.NetworkPeerAddress(remoteAddr.Name),
		)
	}

	switch localAddr := localAddr.(type) {
	case *net.TCPAddr:
		attrs = append(attrs,
			semconv.NetworkLocalAddress(localAddr.IP.String()),
			semconv.NetworkLocalPort(localAddr.Port),
		)
	case *net.UnixAddr:
		attrs = append(attrs, semconv.NetworkLocalAddress(localAddr.Name))
	}

	return attrs
}

func operationAttributeFromCommandTag(tag pgconn.CommandTag) attribute.KeyValue {
	switch {
	case tag.Select():
		return semconv.DBOperationName("SELECT")
	case tag.Insert():
		return semconv.DBOperationName("INSERT")
	case tag.Update():
		return semconv.DBOperationName("UPDATE")
	case tag.Delete():
		return semconv.DBOperationName("DELETE")
	}

	return semconv.DBOperationName(strings.ToUpper(tag.String()))
}

func retunredRowsAttributeFromCommandTag(tag pgconn.CommandTag) attribute.KeyValue {
	return semconv.DBResponseReturnedRows(int(tag.RowsAffected()))
}

func collectAttributeFrom(tableName pgx.Identifier) attribute.KeyValue {
	return semconv.DBCollectionName(strings.Join(tableName, "."))
}

func (t *Tracer) queryAttributesFromSQL(sql string) []attribute.KeyValue {
	queryText := sql
	summary := ""
	attrs := make([]attribute.KeyValue, 0, 2)

	if name, trimmedSQL, ok := sqlcQueryName(sql); ok {
		if t.captureSQLCName {
			summary = name
		}
		_ = trimmedSQL
	}
	if t.omitQueryComments {
		queryText = stripSQLComments(queryText)
	}
	if summary == "" && t.captureQuerySummary {
		summary, _ = querySummary(queryText)
	}
	if summary != "" {
		attrs = append(attrs, attribute.String("db.query.summary", summary))
	}

	attrs = append(attrs, semconv.DBQueryText(queryText))
	return attrs
}

func stripSQLComments(sql string) string {
	var builder strings.Builder
	builder.Grow(len(sql))

	for index := 0; index < len(sql); {
		switch {
		case strings.HasPrefix(sql[index:], "--"):
			index += 2
			for index < len(sql) && sql[index] != '\n' && sql[index] != '\r' {
				index++
			}
		case strings.HasPrefix(sql[index:], "/*"):
			index = skipBlockComment(sql, index)
		case sql[index] == '\'':
			index = writeSingleQuotedSQL(&builder, sql, index)
		case sql[index] == '"':
			index = writeDoubleQuotedSQL(&builder, sql, index)
		case sql[index] == '$':
			if end := dollarQuoteEnd(sql, index); end > index {
				builder.WriteString(sql[index:end])
				index = end
			} else {
				builder.WriteByte(sql[index])
				index++
			}
		default:
			builder.WriteByte(sql[index])
			index++
		}
	}

	return strings.TrimSpace(builder.String())
}

func skipBlockComment(sql string, index int) int {
	depth := 0
	for index < len(sql) {
		switch {
		case strings.HasPrefix(sql[index:], "/*"):
			depth++
			index += 2
		case strings.HasPrefix(sql[index:], "*/"):
			depth--
			index += 2
			if depth == 0 {
				return index
			}
		default:
			index++
		}
	}
	return index
}

func writeSingleQuotedSQL(builder *strings.Builder, sql string, index int) int {
	builder.WriteByte(sql[index])
	index++
	for index < len(sql) {
		builder.WriteByte(sql[index])
		if sql[index] == '\'' {
			if index+1 < len(sql) && sql[index+1] == '\'' {
				builder.WriteByte(sql[index+1])
				index += 2
				continue
			}
			return index + 1
		}
		index++
	}
	return index
}

func writeDoubleQuotedSQL(builder *strings.Builder, sql string, index int) int {
	builder.WriteByte(sql[index])
	index++
	for index < len(sql) {
		builder.WriteByte(sql[index])
		if sql[index] == '"' {
			if index+1 < len(sql) && sql[index+1] == '"' {
				builder.WriteByte(sql[index+1])
				index += 2
				continue
			}
			return index + 1
		}
		index++
	}
	return index
}

func dollarQuoteEnd(sql string, index int) int {
	endTag := index + 1
	for endTag < len(sql) && (sql[endTag] == '_' || sql[endTag] >= 'a' && sql[endTag] <= 'z' || sql[endTag] >= 'A' && sql[endTag] <= 'Z' || sql[endTag] >= '0' && sql[endTag] <= '9') {
		endTag++
	}
	if endTag >= len(sql) || sql[endTag] != '$' {
		return -1
	}

	tag := sql[index : endTag+1]
	if closeIndex := strings.Index(sql[endTag+1:], tag); closeIndex >= 0 {
		return endTag + 1 + closeIndex + len(tag)
	}
	return -1
}

func querySummary(sql string) (string, bool) {
	sql = stripLeadingSQLComments(sql)
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return "", false
	}
	cteNames := collectCTENames(sql)

	parts := make([]string, 0, 4)
	for index := 0; index < len(fields); index++ {
		field := cleanSummaryKeyword(fields[index])
		switch field {
		case "SELECT", "DELETE":
			parts = append(parts, field)
		case "INSERT":
			parts = append(parts, field)
			if collection, ok := fieldAfter(fields[index+1:], "INTO"); ok {
				parts = append(parts, collection)
			}
		case "UPDATE", "CALL":
			parts = append(parts, field)
			if index+1 < len(fields) {
				parts = append(parts, cleanSummaryIdentifier(fields[index+1]))
			}
		case "FROM":
			parts = append(parts, summaryTargets(fields[index+1:], cteNames)...)
		case "JOIN":
			if index+1 < len(fields) {
				if target := cleanSummaryIdentifier(fields[index+1]); target != "" && !containsSummaryName(cteNames, target) {
					parts = append(parts, target)
				}
			}
		}
	}

	if len(parts) == 0 {
		return cleanSummaryKeyword(fields[0]), true
	}

	return strings.Join(parts, " "), true
}

func fieldAfter(fields []string, marker string) (string, bool) {
	for index := 0; index+1 < len(fields); index++ {
		if strings.EqualFold(fields[index], marker) {
			return cleanSummaryIdentifier(fields[index+1]), true
		}
	}
	return "", false
}

func summaryTargets(fields []string, ignoredNames map[string]struct{}) []string {
	targets := make([]string, 0, 2)
	for _, field := range fields {
		keyword := cleanSummaryKeyword(field)
		if keyword == "" {
			continue
		}
		if keyword == "SELECT" {
			return targets
		}
		if stopsSummaryTargetScan(keyword) {
			break
		}

		target := cleanSummaryIdentifier(field)
		if target != "" && target != "*" && !containsSummaryName(ignoredNames, target) {
			targets = append(targets, target)
		}
		if !strings.HasSuffix(field, ",") {
			break
		}
	}
	return targets
}

func stopsSummaryTargetScan(keyword string) bool {
	switch keyword {
	case "WHERE", "JOIN", "ON", "GROUP", "ORDER", "HAVING", "LIMIT", "OFFSET", "UNION", "EXCEPT", "INTERSECT", "RETURNING", "VALUES", "SET", "WITH", "INSERT", "UPDATE", "DELETE", "CALL":
		return true
	}
	return false
}

func collectCTENames(sql string) map[string]struct{} {
	names := make(map[string]struct{})
	remaining := strings.TrimLeft(sql, " \t\r\n")
	if !strings.HasPrefix(cleanSummaryKeyword(firstSummaryField(remaining)), "WITH") {
		return names
	}

	remaining = strings.TrimSpace(remaining[len(firstSummaryField(remaining)):])
	if strings.HasPrefix(cleanSummaryKeyword(firstSummaryField(remaining)), "RECURSIVE") {
		remaining = strings.TrimSpace(remaining[len(firstSummaryField(remaining)):])
	}

	for remaining != "" {
		name := cleanSummaryIdentifier(firstSummaryField(remaining))
		if name == "" {
			return names
		}
		remaining = strings.TrimSpace(remaining[len(firstSummaryField(remaining)):])

		if strings.HasPrefix(remaining, "(") {
			end := strings.Index(remaining, ")")
			if end < 0 {
				return names
			}
			remaining = strings.TrimSpace(remaining[end+1:])
		}

		if !strings.HasPrefix(cleanSummaryKeyword(firstSummaryField(remaining)), "AS") {
			return names
		}
		names[name] = struct{}{}
		remaining = strings.TrimSpace(remaining[len(firstSummaryField(remaining)):])

		if !strings.HasPrefix(remaining, "(") {
			return names
		}
		remaining = skipParenthesizedSummarySQL(remaining)
		remaining = strings.TrimSpace(remaining)
		if !strings.HasPrefix(remaining, ",") {
			return names
		}
		remaining = strings.TrimSpace(remaining[1:])
	}

	return names
}

func firstSummaryField(sql string) string {
	sql = strings.TrimLeft(sql, " \t\r\n")
	if index := strings.IndexAny(sql, " \t\r\n"); index >= 0 {
		return sql[:index]
	}
	return sql
}

func skipParenthesizedSummarySQL(sql string) string {
	depth := 0
	for index, char := range sql {
		switch char {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return sql[index+1:]
			}
		}
	}
	return ""
}

func containsSummaryName(names map[string]struct{}, name string) bool {
	_, ok := names[name]
	return ok
}

func cleanSummaryKeyword(keyword string) string {
	return strings.ToUpper(strings.Trim(keyword, "\"'`;(),"))
}

func cleanSummaryIdentifier(identifier string) string {
	return strings.Trim(identifier, "\"'`;(),")
}

func stripLeadingSQLComments(sql string) string {
	for {
		sql = strings.TrimLeft(sql, " \t\r\n")
		if strings.HasPrefix(sql, "--") {
			_, rest, _ := splitSQLLine(sql)
			sql = rest
			continue
		}
		if strings.HasPrefix(sql, "/*") {
			end := strings.Index(sql[2:], "*/")
			if end < 0 {
				return ""
			}
			sql = sql[end+4:]
			continue
		}
		return sql
	}
}

func sqlcQueryName(sql string) (string, string, bool) {
	remaining := sql
	prefix := ""

	for {
		line, rest, separator := splitSQLLine(remaining)
		trimmedLine := strings.TrimSpace(line)

		if trimmedLine == "" {
			if separator == "" {
				return "", sql, false
			}
			prefix += line + separator
			remaining = rest
			continue
		}

		if !strings.HasPrefix(trimmedLine, "--") {
			return "", sql, false
		}

		comment := strings.TrimSpace(strings.TrimPrefix(trimmedLine, "--"))
		if !strings.HasPrefix(comment, "name:") {
			return "", sql, false
		}

		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(comment, "name:")))
		if len(fields) == 0 {
			return "", sql, false
		}

		return fields[0], prefix + rest, true
	}
}

func splitSQLLine(sql string) (string, string, string) {
	if index := strings.IndexAny(sql, "\r\n"); index >= 0 {
		if sql[index] == '\r' && index+1 < len(sql) && sql[index+1] == '\n' {
			return sql[:index], sql[index+2:], "\r\n"
		}
		return sql[:index], sql[index+1:], sql[index : index+1]
	}

	return sql, "", ""
}

func queryParameterAttributesFromArgs(args []any) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(args))

	for i, arg := range args {
		key := strconv.Itoa(i + 1)
		attrs = append(attrs, semconv.DBQueryParameter(key, fmt.Sprintf("%v", arg)))
	}

	return attrs
}

func pgErrDetails(err error) *pgconn.PgError {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr
	}
	return nil
}

func pgErrType(err error) string {
	if pgErr := pgErrDetails(err); pgErr != nil {
		name := pgcode.Name(pgErr.Code)
		if name != "" {
			return name
		}

		return pgErr.Code
	}

	return err.Error()
}
