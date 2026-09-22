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
	Version   = "1.0.2"

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
	trimQueryComments   bool
}

func NewTracer(opts ...Option) *Tracer {
	o := newOptions(opts...)

	tracer := o.tracerProvider.Tracer(ScopeName, trace.WithInstrumentationVersion(Version))

	return &Tracer{
		tracer:              tracer,
		attributes:          append([]attribute.KeyValue(nil), o.attributes...),
		captureQueryParams:  o.captureQueryParams,
		captureNetworkAttrs: o.captureNetworkAttrs,
		trimQueryComments:   o.trimQueryComments,
	}
}

// TraceConnectStart implements [pgx.ConnectTracer].
func (t *Tracer) TraceConnectStart(ctx context.Context, data pgx.TraceConnectStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	attrs := t.attributesWith(connectionAttributesFromConfig(data.ConnConfig))

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
		span.SetAttributes(errorAttributes(data.Err)...)
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

	attrs := t.attributesWith(connectionAttributesFromPool(pool))

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
		span.SetAttributes(errorAttributes(data.Err)...)
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// TraceCopyFromStart implements [pgx.CopyFromTracer].
func (t *Tracer) TraceCopyFromStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	attrs := t.attributesWith(connectionAttributesFromConn(conn))
	if t.captureNetworkAttrs {
		attrs = append(attrs, networkPeerAttributesFromConn(conn)...)
	}
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
		span.SetAttributes(errorAttributes(data.Err)...)
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// TracePrepareStart implements [pgx.PrepareTracer].
func (t *Tracer) TracePrepareStart(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	attrs := t.attributesWith(connectionAttributesFromConn(conn))
	if t.captureNetworkAttrs {
		attrs = append(attrs, networkPeerAttributesFromConn(conn)...)
	}
	attrs = append(attrs, queryAttributeFromQuery(data.SQL, t.trimQueryComments))

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
		span.SetAttributes(errorAttributes(data.Err)...)
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// TraceBatchStart implements [pgx.BatchTracer].
func (t *Tracer) TraceBatchStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	attrs := t.attributesWith(connectionAttributesFromConn(conn))
	if t.captureNetworkAttrs {
		attrs = append(attrs, networkPeerAttributesFromConn(conn)...)
	}

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
		span.SetAttributes(errorAttributes(data.Err)...)
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

func (t *Tracer) TraceBatchQuery(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchQueryData) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	attrs := t.attributesWith(connectionAttributesFromConn(conn))
	attrs = append(attrs, operationAttributeFromCommandTag(data.CommandTag))
	attrs = append(attrs, retunredRowsAttributeFromCommandTag(data.CommandTag))
	attrs = append(attrs, queryAttributeFromQuery(data.SQL, t.trimQueryComments))

	span.SetAttributes(attrs...)
}

// TraceQueryStart implements [pgx.QueryTracer].
func (t *Tracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	attrs := t.attributesWith(connectionAttributesFromConn(conn))
	if t.captureNetworkAttrs {
		attrs = append(attrs, networkPeerAttributesFromConn(conn)...)
	}
	attrs = append(attrs, queryAttributeFromQuery(data.SQL, t.trimQueryComments))

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
		span.SetAttributes(errorAttributes(data.Err)...)
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

func connectionAttributesFromConfig(config *pgx.ConnConfig) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0)

	if config != nil {
		attrs = append(attrs, semconv.DBSystemNamePostgreSQL)

		attrs = append(attrs, semconv.ServerAddress(config.Host))
		attrs = append(attrs, semconv.ServerPort(int(config.Port)))

		if config.User != "" {
			attrs = append(attrs, semconv.UserName(config.User))
		}

		if namespace := namespaceFromConfig(config); namespace != "" {
			attrs = append(attrs, semconv.DBNamespace(namespace))
		}
	}

	return attrs
}

// namespaceFromConfig builds db.namespace as "{database}|{schema}" per the PostgreSQL
// semantic conventions, using the search_path set at connection time to avoid an
// extra round trip. Only the first schema in search_path is used.
func namespaceFromConfig(config *pgx.ConnConfig) string {
	schema := firstSearchPathSchema(config.RuntimeParams["search_path"])

	switch {
	case config.Database != "" && schema != "":
		return config.Database + "|" + schema
	case schema != "":
		return schema
	default:
		return config.Database
	}
}

func firstSearchPathSchema(searchPath string) string {
	schema, _, _ := strings.Cut(searchPath, ",")
	return strings.Trim(strings.TrimSpace(schema), `"`)
}

func connectionAttributesFromConn(conn *pgx.Conn) []attribute.KeyValue {
	if conn == nil {
		return nil
	}

	return connectionAttributesFromConfig(conn.Config())
}

func connectionAttributesFromPool(pool *pgxpool.Pool) []attribute.KeyValue {
	if pool == nil || pool.Config() == nil {
		return nil
	}

	return connectionAttributesFromConfig(pool.Config().ConnConfig)
}

func (t *Tracer) attributesWith(groups ...[]attribute.KeyValue) []attribute.KeyValue {
	length := len(t.attributes)
	for _, group := range groups {
		length += len(group)
	}

	attrs := make([]attribute.KeyValue, 0, length)
	attrs = append(attrs, t.attributes...)
	for _, group := range groups {
		attrs = append(attrs, group...)
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

func queryAttributeFromQuery(query string, trim bool) attribute.KeyValue {
	queryText := strings.TrimSpace(query)

	if trim {
		queryText = stripSQLComments(query)
	}

	return semconv.DBQueryText(queryText)
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
		default:
			builder.WriteByte(sql[index])
			index++
		}
	}

	return strings.Join(strings.Fields(strings.TrimSpace(builder.String())), " ")
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

func queryParameterAttributesFromArgs(args []any) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(args))

	for i, arg := range args {
		key := strconv.Itoa(i)
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

func errorAttributes(err error) []attribute.KeyValue {
	attrs := []attribute.KeyValue{semconv.ErrorTypeKey.String(pgErrType(err))}
	if pgErr := pgErrDetails(err); pgErr != nil {
		attrs = append(attrs, semconv.DBResponseStatusCode(pgErr.Code))
	}
	return attrs
}

func pgErrType(err error) string {
	if pgErr := pgErrDetails(err); pgErr != nil {
		name := pgcode.Name(pgErr.Code)
		if name != "" {
			return name
		}

		return pgErr.Code
	}

	if errors.Is(err, context.Canceled) {
		return "context.Canceled"
	}

	// err.Error() is unbounded and may embed dynamic details (hosts, ports,
	// timings). pgx wraps connection failures in generic container types, so
	// unwrap to the innermost error for a stable, low-cardinality classification.
	return fmt.Sprintf("%T", rootCause(err))
}

func rootCause(err error) error {
	for {
		unwrapped := errors.Unwrap(err)
		if unwrapped == nil {
			return err
		}
		err = unwrapped
	}
}
