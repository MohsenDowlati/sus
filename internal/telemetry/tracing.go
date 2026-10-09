// Package telemetry configures distributed tracing for the API and worker.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/MohsenDowlati/shorts/internal/logging"
	chimw "github.com/go-chi/chi/v5/middleware"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Init installs a batching TracerProvider before instrumented clients are created.
// Exporters read standard OTEL_EXPORTER_OTLP_* environment variables. Shut down
// the provider after requests, queued analytics and storage connections close.
func Init(ctx context.Context, defaultServiceName string) (*sdktrace.TracerProvider, error) {
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Error("telemetry error", "error", err)
	}))
	protocol := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"))
	if protocol == "" {
		protocol = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
	}
	if protocol == "" {
		protocol = "grpc"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", defaultServiceName)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize trace resource: %w", err)
	}

	// Supply a local Jaeger endpoint only when no endpoint was configured.
	// Explicit endpoints retain the exporter's normal TLS and environment options.
	localEndpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) == "" &&
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) == ""
	var exporter sdktrace.SpanExporter
	switch protocol {
	case "grpc":
		var options []otlptracegrpc.Option
		if localEndpoint {
			options = append(options, otlptracegrpc.WithEndpointURL("http://localhost:4317"))
		}
		exporter, err = otlptracegrpc.New(ctx, options...)
	case "http/protobuf":
		var options []otlptracehttp.Option
		if localEndpoint {
			options = append(options, otlptracehttp.WithEndpointURL("http://localhost:4318/v1/traces"))
		}
		exporter, err = otlptracehttp.New(ctx, options...)
	default:
		return nil, fmt.Errorf("unsupported OTLP traces protocol %q: use grpc or http/protobuf", protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("initialize OTLP trace exporter: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return provider, nil
}

// HTTP starts a server span with a stable method and route name. A valid remote
// W3C parent is continued; requests without one start a new trace.
func HTTP(operation string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		_, route, _ := strings.Cut(operation, " ")
		observed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logging.ObserveRequest(r.Context())
			wrapped := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				status := wrapped.Status()
				if status >= 400 {
					span := trace.SpanFromContext(r.Context())
					code := strconv.Itoa(status)
					span.SetAttributes(attribute.Int("http.response.status_code", status), attribute.String("error.code", code), attribute.String("error.type", "http_error"))
					span.RecordError(errors.New(http.StatusText(status)))
					span.SetStatus(codes.Error, code)
				}
			}()
			next.ServeHTTP(wrapped, r)
		})
		return otelhttp.NewHandler(otelhttp.WithRouteTag(route, observed), operation,
			otelhttp.WithSpanNameFormatter(func(_ string, _ *http.Request) string { return operation }),
			otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator(
				propagation.TraceContext{}, propagation.Baggage{},
			)),
		)
	}
}
