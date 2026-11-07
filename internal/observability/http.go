package observability

import (
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type transport struct{ next http.RoundTripper }

// HTTPTransport deliberately omits URLs, headers and payloads from spans.
func HTTPTransport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return transport{next}
}
func (t transport) RoundTrip(r *http.Request) (response *http.Response, err error) {
	method := "OTHER"
	if r.Method == "GET" || r.Method == "POST" {
		method = r.Method
	}
	ctx, span := Start(r.Context(), "upstream.http", attribute.String("http.request.method", method))
	defer func() {
		if response != nil {
			span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode))
			if response.StatusCode >= 500 {
				span.SetStatus(codes.Error, "upstream_error")
			}
		}
		End(span, err)
	}()
	return t.next.RoundTrip(r.Clone(ctx))
}
