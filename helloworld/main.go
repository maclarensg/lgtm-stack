package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

var (
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "helloworld_requests_total",
			Help: "Total number of requests",
		},
		[]string{"method", "path", "status"},
	)

	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "helloworld_request_duration_seconds",
			Help:    "Request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	inFlightRequests = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "helloworld_in_flight_requests",
			Help: "Number of in-flight requests",
		},
	)
)

func init() {
	prometheus.MustRegister(requestsTotal, requestDuration, inFlightRequests)
}

func initTracer(ctx context.Context) (*sdktrace.TracerProvider, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "alloy.observability.svc:4317"
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("creating OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("helloworld"),
			semconv.ServiceVersionKey.String("1.0.0"),
			attribute.String("environment", "dev"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp, nil
}

func helloHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	inFlightRequests.Inc()
	defer inFlightRequests.Dec()

	ctx := r.Context()
	tracer := otel.Tracer("helloworld")
	ctx, span := tracer.Start(ctx, "hello-handler")
	defer span.End()

	// Simulate some work with variable latency
	workDuration := time.Duration(50+rand.Intn(200)) * time.Millisecond
	_, childSpan := tracer.Start(ctx, "process-request")
	time.Sleep(workDuration)
	childSpan.SetAttributes(attribute.Int64("work.duration_ms", workDuration.Milliseconds()))
	childSpan.End()

	name := r.URL.Query().Get("name")
	if name == "" {
		name = "World"
	}

	span.SetAttributes(
		attribute.String("request.name", name),
		attribute.String("http.method", r.Method),
		attribute.String("http.url", r.URL.String()),
	)

	message := fmt.Sprintf("Hello, %s!", name)

	slog.InfoContext(ctx, "request handled",
		"method", r.Method,
		"path", r.URL.Path,
		"name", name,
		"duration_ms", time.Since(start).Milliseconds(),
		"trace_id", span.SpanContext().TraceID().String(),
	)

	status := http.StatusOK

	// Simulate occasional errors (5% chance)
	if rand.Float64() < 0.05 {
		status = http.StatusInternalServerError
		message = "Something went wrong!"
		span.SetAttributes(attribute.Bool("error", true))
		slog.ErrorContext(ctx, "simulated error",
			"trace_id", span.SpanContext().TraceID().String(),
		)
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	fmt.Fprintln(w, message)

	duration := time.Since(start).Seconds()
	requestsTotal.WithLabelValues(r.Method, r.URL.Path, fmt.Sprintf("%d", status)).Inc()
	requestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp, err := initTracer(ctx)
	if err != nil {
		slog.Error("failed to initialize tracer", "error", err)
	} else {
		defer func() {
			if err := tp.Shutdown(ctx); err != nil {
				slog.Error("failed to shutdown tracer", "error", err)
			}
		}()
		slog.Info("tracer initialized")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", helloHandler)
	mux.HandleFunc("/healthz", healthHandler)
	mux.Handle("/metrics", promhttp.Handler())

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("starting server", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	slog.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)
}
