package observability

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
)

type responseStatus struct {
	http.ResponseWriter
	status int
}

func (w *responseStatus) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *responseStatus) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *responseStatus) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func HTTP(next http.Handler) (http.Handler, error) {
	meter := otel.Meter("wbd/http")
	requests, err := meter.Int64Counter("wbd.http.requests")
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram("wbd.http.request.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60))
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := otel.Tracer("wbd/http").Start(ctx, "http.request")
		defer span.End()
		request := r.WithContext(ctx)
		started := time.Now()
		response := &responseStatus{ResponseWriter: w}
		next.ServeHTTP(response, request)
		if response.status == 0 {
			response.status = http.StatusOK
		}
		route := request.Pattern
		if route == "" {
			route = "unmatched"
		}
		status := strconv.Itoa(response.status)
		method := request.Method
		switch method {
		case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		default:
			method = "OTHER"
		}
		attributes := metric.WithAttributes(attribute.String("method", method), attribute.String("route", route), attribute.String("status", status))
		requests.Add(ctx, 1, attributes)
		duration.Record(ctx, time.Since(started).Seconds(), attributes)
		span.SetAttributes(attribute.String("http.route", route), attribute.Int("http.response.status_code", response.status))
		if response.status >= 500 {
			span.SetStatus(codes.Error, "request failed")
		}
		slog.InfoContext(ctx, "HTTP request completed", "event", "http.request", "method", method, "route", route, "status", response.status, "duration_ms", time.Since(started).Milliseconds())
	}), nil
}
