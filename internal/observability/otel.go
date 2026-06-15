package observability

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

// OtelTracer implements Tracer using OpenTelemetry spans.
type OtelTracer struct {
	tracer trace.Tracer
	cfg    *OtelConfig
}

// OtelConfig configures the OtelTracer.
type OtelConfig struct {
	ServiceName    string
	ServiceVersion string
	Endpoint       string // OTLP HTTP endpoint, default "localhost:4318"
	Insecure       bool
}

// DefaultOtelConfig returns sensible defaults for OtelConfig.
func DefaultOtelConfig() *OtelConfig {
	return &OtelConfig{
		ServiceName:    "goagentx",
		ServiceVersion: "0.1.0",
		Endpoint:       "localhost:4318",
		Insecure:       true,
	}
}

var tracerProvider *sdktrace.TracerProvider
var tracerInitCounter uint64

// NewOtelTracer creates a new OtelTracer with an OTLP HTTP exporter.
// If cfg is nil, defaults are used.
func NewOtelTracer(cfg *OtelConfig) (Tracer, func(), error) {
	if cfg == nil {
		cfg = DefaultOtelConfig()
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.ServiceVersionKey.String(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("otel: create resource: %w", err)
	}

	opts := []otlptracehttp.Option{}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	opts = append(opts, otlptracehttp.WithEndpoint(cfg.Endpoint))

	exp, err := otlptracehttp.New(context.Background(), opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("otel: create exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)

	id := atomic.AddUint64(&tracerInitCounter, 1)
	tracerName := fmt.Sprintf("%s/tracer/%d", cfg.ServiceName, id)

	t := &OtelTracer{
		tracer: tp.Tracer(tracerName),
		cfg:    cfg,
	}

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(ctx)
	}

	return t, shutdown, nil
}

// NewNoopOtelTracer creates a no-op OtelTracer for testing when no OTLP endpoint is available.
func NewNoopOtelTracer() Tracer {
	return &OtelTracer{
		tracer: trace.NewNoopTracerProvider().Tracer("noop"),
		cfg:    DefaultOtelConfig(),
	}
}

// RecordLLMCall records an LLM call as an OTel span.
func (t *OtelTracer) RecordLLMCall(ctx context.Context, call *LLMCall) {
	if call == nil {
		return
	}
	_, span := t.tracer.Start(ctx, "llm.call",
		trace.WithAttributes(
			attribute.String("llm.model", call.Model),
			attribute.String("llm.prompt", truncateAttr(call.Prompt, 2000)),
			attribute.String("llm.response", truncateAttr(call.Response, 2000)),
			attribute.Int("llm.tokens_used", call.TokensUsed),
			attribute.String("trace_id", call.TraceID),
		),
	)
	defer span.End()

	if call.Error != nil {
		span.RecordError(call.Error)
		span.SetAttributes(attribute.Bool("error", true))
	}
}

// RecordToolCall records a tool execution as an OTel span.
func (t *OtelTracer) RecordToolCall(ctx context.Context, call *ToolCall) {
	if call == nil {
		return
	}
	_, span := t.tracer.Start(ctx, "tool.call",
		trace.WithAttributes(
			attribute.String("tool.name", call.ToolName),
			attribute.String("trace_id", call.TraceID),
		),
	)
	defer span.End()

	if call.Error != nil {
		span.RecordError(call.Error)
		span.SetAttributes(attribute.Bool("error", true))
	}
}

// RecordAgentStep records an agent step as an OTel span.
func (t *OtelTracer) RecordAgentStep(ctx context.Context, step *AgentStep) {
	if step == nil {
		return
	}
	_, span := t.tracer.Start(ctx, "agent.step",
		trace.WithAttributes(
			attribute.String("agent.id", step.AgentID),
			attribute.String("agent.step", step.StepName),
			attribute.String("trace_id", step.TraceID),
		),
	)
	defer span.End()

	if step.Error != nil {
		span.RecordError(step.Error)
		span.SetAttributes(attribute.Bool("error", true))
	}
}

// RecordError records an error as an OTel span event.
func (t *OtelTracer) RecordError(ctx context.Context, err *AgentError) {
	if err == nil {
		return
	}
	_, span := t.tracer.Start(ctx, "agent.error",
		trace.WithAttributes(
			attribute.String("agent.id", err.AgentID),
			attribute.String("error.type", err.ErrorType),
			attribute.String("error.message", err.Message),
			attribute.String("trace_id", err.TraceID),
			attribute.Bool("error", true),
		),
	)
	defer span.End()
}

// GetTraceID returns the current trace ID from context.
func (t *OtelTracer) GetTraceID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span == nil {
		return ""
	}
	return span.SpanContext().TraceID().String()
}

// WithTrace returns a new context with a span context.
func (t *OtelTracer) WithTrace(ctx context.Context) context.Context {
	_, span := t.tracer.Start(ctx, "root")
	defer span.End()
	return ctx
}

// truncateAttr truncates a string attribute value to maxLen runes.
func truncateAttr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}

// Ensure OtelTracer implements Tracer at compile time.
var _ Tracer = (*OtelTracer)(nil)
