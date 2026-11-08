package observability

import (
	"context"
	"errors"
	"github.com/carvebloom/authova/internal/diagnostic"
	"math"
	"net/url"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// ConfigureTracing is optional. Exporting is asynchronous, bounded, and cannot
// become an authentication dependency. Production requires verified HTTPS.
func ConfigureTracing(ctx context.Context, endpoint, service string, ratio float64, development bool) (func(context.Context) error, error) {
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (!development && u.Scheme != "https") || (u.Scheme != "http" && u.Scheme != "https") || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
		return nil, errors.New("invalid trace exporter configuration")
	}
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint), otlptracehttp.WithTimeout(2*time.Second))
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithResource(sdkresource.NewSchemaless(attribute.String("service.name", service))), sdktrace.WithSampler(sdktrace.TraceIDRatioBased(ratio)), sdktrace.WithBatcher(exporter, sdktrace.WithMaxQueueSize(256), sdktrace.WithMaxExportBatchSize(64), sdktrace.WithBatchTimeout(time.Second), sdktrace.WithExportTimeout(2*time.Second)))
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("authova").Start(ctx, name, trace.WithAttributes(attrs...))
}
func End(span trace.Span, err error) {
	if err != nil {
		span.SetStatus(codes.Error, "operation_failed")
		d := diagnostic.Inspect(err)
		span.AddEvent("operation.failed", trace.WithAttributes(attribute.String("error.type", d.Kind), attribute.String("authova.first_operation", d.Operation), attribute.String("authova.first_phase", d.Phase)))
		span.SetAttributes(attribute.String("error.type", d.Kind), attribute.String("authova.cause_type", d.Type), attribute.String("authova.error_source", d.Source))
	}
	span.End()
}
