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
	captureCollection   bool
	spanNameMode        SpanNameMode
}

func NewTracer(opts ...Option) *Tracer {
	o := newOptions(opts...)

	tracer := o.tracerProvider.Tracer(ScopeName, trace.WithInstrumentationVersion(Version))

	return &Tracer{
		tracer:              tracer,
		attributes:          o.attributes,
		captureQueryParams:  o.captureQueryParams,
		captureNetworkAttrs: o.captureNetworkAttrs,
		captureCollection:   o.captureCollection,
		spanNameMode:        o.spanNameMode,
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
	spanName := spanCopy
	if t.spanNameMode == SpanNameSemantic {
		spanName = spanNameFromOperationAndTarget("COPY", strings.Join(data.TableName, "."))
	}

	spanCtx, _ := t.tracer.Start(ctx, spanName,
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
	spanName := t.querySpanName(data.SQL, spanPrepare)

	spanCtx, _ := t.tracer.Start(ctx, spanName,
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
	spanName := spanBatch
	if t.spanNameMode == SpanNameSemantic {
		spanName = "BATCH"
	}

	spanCtx, _ := t.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
	if t.spanNameMode == SpanNameSemantic {
		spanCtx = context.WithValue(spanCtx, batchSpanNameStateKey{}, &batchSpanNameState{})
	}

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
	t.updateBatchSpanName(ctx, data.SQL)

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
	spanName := t.querySpanName(data.SQL, spanQuery)

	if t.captureQueryParams {
		attrs = append(attrs, queryParameterAttributesFromArgs(data.Args)...)
	}

	spanCtx, _ := t.tracer.Start(ctx, spanName,
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

type batchSpanNameStateKey struct{}

type batchSpanNameState struct {
	collections []string
	ambiguous   bool
}

func (t *Tracer) querySpanName(sql string, fallback string) string {
	if t.spanNameMode != SpanNameSemantic {
		return fallback
	}

	operation, ok := queryOperationName(sql)
	if !ok {
		if collection, ok := queryCollectionName(sql); ok {
			return collection
		}
		return "postgresql"
	}

	collection, _ := queryCollectionName(sql)
	return spanNameFromOperationAndTarget(operation, collection)
}

func (t *Tracer) updateBatchSpanName(ctx context.Context, sql string) {
	if t.spanNameMode != SpanNameSemantic {
		return
	}

	state, _ := ctx.Value(batchSpanNameStateKey{}).(*batchSpanNameState)
	if state == nil {
		return
	}

	collections := uniqueQueryCollections(sql)
	if len(collections) != 1 {
		state.ambiguous = true
	} else {
		state.collections = appendQueryCollections(state.collections, collections[0])
	}

	span := trace.SpanFromContext(ctx)
	if state.ambiguous || len(state.collections) != 1 {
		span.SetName("BATCH")
		return
	}
	span.SetName(spanNameFromOperationAndTarget("BATCH", state.collections[0]))
}

func spanNameFromOperationAndTarget(operation string, target string) string {
	if operation == "" {
		if target != "" {
			return target
		}
		return "postgresql"
	}
	if target == "" {
		return operation
	}
	return operation + " " + target
}

func (t *Tracer) queryAttributesFromSQL(sql string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 2)
	if t.captureCollection {
		if collection, ok := queryCollectionName(sql); ok {
			attrs = append(attrs, semconv.DBCollectionName(collection))
		}
	}
	attrs = append(attrs, semconv.DBQueryText(sql))
	return attrs
}

func queryOperationName(sql string) (string, bool) {
	fields := strings.Fields(stripLeadingSQLComments(sql))
	for _, field := range fields {
		keyword := cleanQueryKeyword(field)
		switch keyword {
		case "WITH", "RECURSIVE", "AS":
			continue
		case "SELECT", "INSERT", "UPDATE", "DELETE", "CALL", "CREATE", "ALTER", "DROP", "TRUNCATE", "VACUUM":
			return keyword, true
		}
	}
	return "", false
}

func queryCollectionName(sql string) (string, bool) {
	collections := uniqueQueryCollections(sql)
	if len(collections) != 1 {
		return "", false
	}
	return collections[0], true
}

func uniqueQueryCollections(sql string) []string {
	fields := strings.Fields(stripLeadingSQLComments(sql))
	if len(fields) == 0 {
		return nil
	}

	cteNames := collectCTENames(fields)
	collections := make([]string, 0, 2)
	for index := 0; index < len(fields); index++ {
		switch cleanQueryKeyword(fields[index]) {
		case "FROM", "JOIN", "INTO", "UPDATE":
			collections = appendQueryCollections(collections, collectionTargetsAfter(fields[index+1:], cteNames)...)
		}
	}

	return collections
}

func collectionTargetsAfter(fields []string, ignoredNames []string) []string {
	targets := make([]string, 0, 2)
	sql := strings.Join(fields, " ")
	for _, field := range fields {
		keyword := cleanQueryKeyword(field)
		if keyword == "" || keyword == "SELECT" {
			return targets
		}
		if stopsCollectionTargetScan(keyword) {
			break
		}

		target := cleanQueryIdentifier(field)
		if parsedTarget, ok := nextCollectionIdentifier(sql); ok {
			target = parsedTarget
		}
		if target != "" && target != "*" && !containsQueryName(ignoredNames, target) {
			targets = append(targets, target)
		}
		if !strings.HasSuffix(field, ",") {
			break
		}
	}
	return targets
}

func appendQueryCollections(collections []string, candidates ...string) []string {
	for _, candidate := range candidates {
		if candidate != "" && !containsQueryName(collections, candidate) {
			collections = append(collections, candidate)
		}
	}
	return collections
}

func collectCTENames(fields []string) []string {
	if len(fields) == 0 || cleanQueryKeyword(fields[0]) != "WITH" {
		return nil
	}

	names := make([]string, 0, 1)
	for index := 1; index < len(fields); index++ {
		keyword := cleanQueryKeyword(fields[index])
		if keyword == "RECURSIVE" {
			continue
		}
		if keyword == "SELECT" || keyword == "INSERT" || keyword == "UPDATE" || keyword == "DELETE" {
			return names
		}
		if index+1 < len(fields) && cleanQueryKeyword(fields[index+1]) == "AS" {
			name := cleanQueryIdentifier(fields[index])
			if name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func stripLeadingSQLComments(sql string) string {
	for {
		sql = strings.TrimLeft(sql, " \t\r\n")
		if strings.HasPrefix(sql, "--") {
			if index := strings.IndexAny(sql, "\r\n"); index >= 0 {
				sql = sql[index+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(sql, "/*") {
			if index := strings.Index(sql[2:], "*/"); index >= 0 {
				sql = sql[index+4:]
				continue
			}
			return ""
		}
		return sql
	}
}

func stopsCollectionTargetScan(keyword string) bool {
	switch keyword {
	case "WHERE", "JOIN", "ON", "GROUP", "ORDER", "HAVING", "LIMIT", "OFFSET", "UNION", "EXCEPT", "INTERSECT", "RETURNING", "VALUES", "SET", "WITH", "INSERT", "UPDATE", "DELETE", "CALL", "USING":
		return true
	}
	return false
}

func cleanQueryKeyword(keyword string) string {
	return strings.ToUpper(cleanQueryIdentifier(keyword))
}

func cleanQueryIdentifier(identifier string) string {
	return strings.Trim(identifier, "\"'`;(),")
}

func nextCollectionIdentifier(sql string) (string, bool) {
	sql = strings.TrimLeft(sql, " \t\r\n")
	parts := make([]string, 0, 2)
	for {
		part, remaining, ok := nextIdentifierPart(sql)
		if !ok {
			return "", false
		}
		parts = append(parts, part)

		remaining = strings.TrimLeft(remaining, " \t\r\n")
		if !strings.HasPrefix(remaining, ".") {
			break
		}
		sql = remaining[1:]
	}

	return strings.Join(parts, "."), true
}

func nextIdentifierPart(sql string) (string, string, bool) {
	if sql == "" {
		return "", "", false
	}
	if sql[0] == '"' {
		return nextQuotedIdentifierPart(sql)
	}

	end := 0
	for end < len(sql) {
		char := sql[end]
		if char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return "", "", false
	}
	return cleanQueryIdentifier(sql[:end]), sql[end:], true
}

func nextQuotedIdentifierPart(sql string) (string, string, bool) {
	var builder strings.Builder
	for index := 1; index < len(sql); index++ {
		if sql[index] == '"' {
			if index+1 < len(sql) && sql[index+1] == '"' {
				builder.WriteByte(sql[index+1])
				index++
				continue
			}
			part := builder.String()
			if strings.ContainsAny(part, " \t\r\n") {
				part = `"` + part + `"`
			}
			return part, sql[index+1:], true
		}
		builder.WriteByte(sql[index])
	}
	return "", "", false
}

func containsQueryName(names []string, name string) bool {
	for _, candidate := range names {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
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
