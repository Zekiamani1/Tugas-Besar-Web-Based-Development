package observability

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Runtime struct {
	trace   *sdktrace.TracerProvider
	logs    *sdklog.LoggerProvider
	metrics *sdkmetric.MeterProvider
}

func Setup(ctx context.Context, serviceName string) (*Runtime, error) {
	res := resource.NewSchemaless(attribute.String("service.name", serviceName))
	metricOptions := []sdkmetric.Option{sdkmetric.WithResource(res)}
	traceOptions := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	var logProvider *sdklog.LoggerProvider
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		traceExporter, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, err
		}
		metricExporter, err := otlpmetrichttp.New(ctx)
		if err != nil {
			_ = traceExporter.Shutdown(ctx)
			return nil, err
		}
		logExporter, err := otlploghttp.New(ctx)
		if err != nil {
			_ = traceExporter.Shutdown(ctx)
			_ = metricExporter.Shutdown(ctx)
			return nil, err
		}
		traceOptions = append(traceOptions, sdktrace.WithBatcher(traceExporter))
		metricOptions = append(metricOptions, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)))
		logProvider = sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)))
	} else {
		traceOptions = append(traceOptions, sdktrace.WithBatcher(consoleSpans{}))
	}
	traceProvider := sdktrace.NewTracerProvider(traceOptions...)
	meterProvider := sdkmetric.NewMeterProvider(metricOptions...)
	otel.SetTracerProvider(traceProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if err := initMetrics(); err != nil {
		_ = meterProvider.Shutdown(ctx)
		_ = traceProvider.Shutdown(ctx)
		if logProvider != nil {
			_ = logProvider.Shutdown(ctx)
		}
		return nil, err
	}
	stdout := slog.NewJSONHandler(os.Stdout, nil)
	if logProvider == nil {
		slog.SetDefault(slog.New(fanoutHandler{handlers: []slog.Handler{stdout}}))
	} else {
		slog.SetDefault(slog.New(fanoutHandler{handlers: []slog.Handler{stdout, otelslog.NewHandler(serviceName, otelslog.WithLoggerProvider(logProvider))}}))
	}
	return &Runtime{trace: traceProvider, logs: logProvider, metrics: meterProvider}, nil
}

type consoleSpans struct{}

func (consoleSpans) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		slog.Info("trace span", "event", "trace.span", "name", span.Name(), "trace_id", span.SpanContext().TraceID().String(), "span_id", span.SpanContext().SpanID().String(), "duration_ms", span.EndTime().Sub(span.StartTime()).Milliseconds(), "status", span.Status().Code.String())
	}
	return nil
}

func (consoleSpans) Shutdown(context.Context) error { return nil }

func (r *Runtime) Shutdown(ctx context.Context) error {
	var logErr error
	if r.logs != nil {
		logErr = r.logs.Shutdown(ctx)
	}
	return errors.Join(r.metrics.Shutdown(ctx), r.trace.Shutdown(ctx), logErr)
}

func Shutdown(r *Runtime) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Shutdown(ctx); err != nil {
		slog.Error("observability shutdown failed")
	}
}
