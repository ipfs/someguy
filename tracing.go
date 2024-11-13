package main

import (
	"context"
	"strings"

	"github.com/ipfs/boxo/tracing"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	traceapi "go.opentelemetry.io/otel/trace"
)

// SetupTracing sets up the tracing based on the OTEL_* environment variables,
// It returns a trace.TracerProvider.
func SetupTracing(ctx context.Context, traceFraction float64) (*trace.TracerProvider, error) {
	tp, err := NewTracerProvider(ctx, traceFraction)
	if err != nil {
		return nil, err
	}

	// Sets the default trace provider for this process. If this is not done, tracing
	// will not be enabled. Please note that this will apply to the entire process
	// as it is set as the default tracer, as per OTel recommendations.
	otel.SetTracerProvider(tp)

	// Configures the default propagators used by the Open Telemetry library. By
	// using autoprop.NewTextMapPropagator, we ensure the value of the environmental
	// variable OTEL_PROPAGATORS is respected, if set. By default, Trace Context
	// and Baggage are used. More details on:
	// https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/sdk-environment-variables.md
	otel.SetTextMapPropagator(autoprop.NewTextMapPropagator())

	return tp, nil
}

// NewTracerProvider creates and configures a TracerProvider.
func NewTracerProvider(ctx context.Context, traceFraction float64) (*trace.TracerProvider, error) {
	exporters, err := tracing.NewSpanExporters(ctx)
	if err != nil {
		return nil, err
	}

	options := []trace.TracerProviderOption{}

	for _, exporter := range exporters {
		options = append(options, trace.WithBatcher(exporter))
	}

	r, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			semconv.ServiceNameKey.String(name),
			semconv.ServiceVersionKey.String(version),
		),
	)
	if err != nil {
		return nil, err
	}

	var baseSampler trace.Sampler
	if traceFraction == 0 {
		baseSampler = trace.NeverSample()
	} else {
		baseSampler = trace.TraceIDRatioBased(traceFraction)
	}

	// Sample all children whose parents are sampled
	// Probabilistically sample if the span is a root which is a Gateway request
	sampler := trace.ParentBased(
		CascadingSamplerFunc(func(parameters trace.SamplingParameters) bool {
			return !traceapi.SpanContextFromContext(parameters.ParentContext).IsValid()
		}, "root sampler",
			CascadingSamplerFunc(func(parameters trace.SamplingParameters) bool {
				return strings.HasPrefix(parameters.Name, "someguy")
			}, "someguy request sampler",
				baseSampler)))

	options = append(options, trace.WithResource(r), trace.WithSampler(sampler))
	return trace.NewTracerProvider(options...), nil
}

// CascadingSamplerFunc will sample with the next tracer if the condition is met, otherwise the sample will be dropped
func CascadingSamplerFunc(shouldSample func(parameters trace.SamplingParameters) bool, description string, next trace.Sampler) trace.Sampler {
	return funcSampler{
		next: next,
		fn: func(parameters trace.SamplingParameters) trace.SamplingResult {
			if shouldSample(parameters) {
				return next.ShouldSample(parameters)
			}
			return trace.SamplingResult{
				Decision:   trace.Drop,
				Tracestate: traceapi.SpanContextFromContext(parameters.ParentContext).TraceState(),
			}
		},
		description: description,
	}
}
