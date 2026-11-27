package observability

import (
	"context"
	"errors"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

type fanoutHandler struct {
	handlers []slog.Handler
}

func (h fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		var hasTraceID, hasSpanID bool
		record.Attrs(func(attr slog.Attr) bool {
			hasTraceID = hasTraceID || attr.Key == "trace_id"
			hasSpanID = hasSpanID || attr.Key == "span_id"
			return true
		})
		if !hasTraceID {
			record.AddAttrs(slog.String("trace_id", span.TraceID().String()))
		}
		if !hasSpanID {
			record.AddAttrs(slog.String("span_id", span.SpanID().String()))
		}
	}
	var result error
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, record.Level) {
			result = errors.Join(result, handler.Handle(ctx, record.Clone()))
		}
	}
	return result
}

func (h fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	result := fanoutHandler{handlers: make([]slog.Handler, 0, len(h.handlers))}
	for _, handler := range h.handlers {
		result.handlers = append(result.handlers, handler.WithAttrs(attrs))
	}
	return result
}

func (h fanoutHandler) WithGroup(name string) slog.Handler {
	result := fanoutHandler{handlers: make([]slog.Handler, 0, len(h.handlers))}
	for _, handler := range h.handlers {
		result.handlers = append(result.handlers, handler.WithGroup(name))
	}
	return result
}
