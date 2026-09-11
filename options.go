package pgxotel

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type SpanNameMode int

const (
	SpanNameStatic SpanNameMode = iota
	SpanNameSemantic
)

type options struct {
	tracerProvider      trace.TracerProvider
	attributes          []attribute.KeyValue
	captureQueryParams  bool
	captureNetworkAttrs bool
	captureCollection   bool
	spanNameMode        SpanNameMode
}

type Option interface {
	apply(*options)
}

type tracerProviderOption struct {
	trace.TracerProvider
}

func (t tracerProviderOption) apply(opts *options) {
	opts.tracerProvider = t.TracerProvider
}

// WithTracerProvider sets the tracer provider to use for creating spans. If not set, the global tracer provider will be used.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return tracerProviderOption{TracerProvider: tp}
}

type attrOption []attribute.KeyValue

func (a attrOption) apply(opts *options) {
	opts.attributes = append(opts.attributes, a...)
}

// WithAttributes adds attributes to all spans created by the pgxotel instrumentation.
func WithAttributes(attrs ...attribute.KeyValue) Option {
	return attrOption(attrs)
}

type captureQueryParamsOption bool

func (c captureQueryParamsOption) apply(opts *options) {
	opts.captureQueryParams = bool(c)
}

// WithQueryParameters enables capturing query parameters as db.query.parameter.<key>
// span attributes. Opt-in only: parameter values may contain sensitive data.
func WithQueryParameters(enabled bool) Option {
	return captureQueryParamsOption(enabled)
}

type captureNetworkAttributesOption bool

func (c captureNetworkAttributesOption) apply(opts *options) {
	opts.captureNetworkAttrs = bool(c)
}

// WithNetworkAttributes enables network.peer.* attributes on connection spans.
func WithNetworkAttributes(enabled bool) Option {
	return captureNetworkAttributesOption(enabled)
}

type captureCollectionOption bool

func (c captureCollectionOption) apply(opts *options) {
	opts.captureCollection = bool(c)
}

// WithQueryCollectionName enables best-effort db.collection.name extraction from query text.
// The attribute is only set when the query appears to reference a single collection.
func WithQueryCollectionName(enabled bool) Option {
	return captureCollectionOption(enabled)
}

type spanNameModeOption SpanNameMode

func (s spanNameModeOption) apply(opts *options) {
	opts.spanNameMode = SpanNameMode(s)
}

// WithSpanNameMode controls how spans are named. The default is SpanNameStatic.
func WithSpanNameMode(mode SpanNameMode) Option {
	return spanNameModeOption(mode)
}

func newOptions(opts ...Option) *options {
	o := &options{
		tracerProvider:      otel.GetTracerProvider(),
		attributes:          make([]attribute.KeyValue, 0),
		captureQueryParams:  false,
		captureNetworkAttrs: false,
		captureCollection:   false,
		spanNameMode:        SpanNameStatic,
	}

	for _, opt := range opts {
		opt.apply(o)
	}

	return o
}
