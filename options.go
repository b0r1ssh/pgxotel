package pgxotel

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type options struct {
	tracerProvider      trace.TracerProvider
	attributes          []attribute.KeyValue
	captureQueryParams  bool
	captureNetworkAttrs bool
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

func newOptions(opts ...Option) *options {
	o := &options{
		tracerProvider:      otel.GetTracerProvider(),
		attributes:          make([]attribute.KeyValue, 0),
		captureQueryParams:  false,
		captureNetworkAttrs: false,
	}

	for _, opt := range opts {
		opt.apply(o)
	}

	return o
}
