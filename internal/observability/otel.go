package observability

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/user/hermod/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InitOTLP initializes the OpenTelemetry SDK with OTLP exporters.
func InitOTLP(ctx context.Context, cfg config.OTLPConfig) (func(context.Context) error, error) {
	if cfg.Endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	protocol := strings.ToLower(cfg.Protocol)
	if protocol == "" {
		protocol = "http" // default
	}

	var traceExporter trace.SpanExporter
	var metricExporter metric.Exporter

	if protocol == "grpc" {
		// gRPC Trace Exporter
		traceOpts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			traceOpts = append(traceOpts, otlptracegrpc.WithHeaders(cfg.Headers))
		}
		traceExporter, err = otlptracegrpc.New(ctx, traceOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create gRPC trace exporter: %w", err)
		}

		// gRPC Metric Exporter
		metricOpts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			metricOpts = append(metricOpts, otlpmetricgrpc.WithHeaders(cfg.Headers))
		}
		metricExporter, err = otlpmetricgrpc.New(ctx, metricOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create gRPC metric exporter: %w", err)
		}
	} else {
		// HTTP Trace Exporter
		traceOpts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			traceOpts = append(traceOpts, otlptracehttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			traceOpts = append(traceOpts, otlptracehttp.WithHeaders(cfg.Headers))
		}
		traceExporter, err = otlptracehttp.New(ctx, traceOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP trace exporter: %w", err)
		}

		// HTTP Metric Exporter
		metricOpts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			metricOpts = append(metricOpts, otlpmetrichttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			metricOpts = append(metricOpts, otlpmetrichttp.WithHeaders(cfg.Headers))
		}
		metricExporter, err = otlpmetrichttp.New(ctx, metricOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP metric exporter: %w", err)
		}
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(traceExporter),
		trace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExporter, metric.WithInterval(15*time.Second))),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	shutdown := func(ctx context.Context) error {
		var errs []error
		if err := tp.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := mp.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
		if len(errs) > 0 {
			return fmt.Errorf("shutdown errors: %v", errs)
		}
		return nil
	}

	return shutdown, nil
}
