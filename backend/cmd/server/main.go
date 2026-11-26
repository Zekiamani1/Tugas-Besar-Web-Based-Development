package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"wbd/internal/middleware"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/dbmanagement"
	"wbd/internal/service/migration"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(consoleSpans{}))
	otel.SetTracerProvider(provider)
	defer provider.Shutdown(context.Background())
	sessionConfig, err := loadSessionConfig()
	if err != nil {
		log.Fatalf("invalid session configuration: %v", err)
	}
	cookieConfig, err := loadCookieConfig()
	if err != nil {
		log.Fatalf("invalid cookie configuration: %v", err)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=postgres dbname=wbd port=5432 sslmode=disable"
	}

	internalDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect internal database: %v", err)
	}

	sqlDB, err := internalDB.DB()
	defer sqlDB.Close()

	acSvc := accesscontrol.NewService(internalDB, sessionConfig)
	dbmSvc := dbmanagement.NewService(internalDB)
	defer dbmSvc.CloseAllTargetConnections()
	migSvc := migration.NewService(internalDB, dbmSvc)
	if err := migSvc.Initialize(context.Background()); err != nil {
		log.Fatalf("failed to initialize migration history: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	rabbitURL := os.Getenv("RABBITMQ_URL")
	if rabbitURL == "" {
		log.Fatal("RABBITMQ_URL must be set")
	}
	go runRabbit(ctx, rabbitURL, func(conn *amqp.Connection) error { return migSvc.RunPublisher(ctx, conn, "migration_jobs") })

	cookieConfig.MigrationApproval = migSvc
	app := NewAppWithConfig(acSvc, dbmSvc, migSvc, cookieConfig)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	corsOriginsRaw := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if corsOriginsRaw == "" {
		corsOriginsRaw = "http://localhost:3000"
	}
	handler := middleware.CORS(strings.Split(corsOriginsRaw, ","), app)

	log.Printf("server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Printf("server stopped: %v", err)
	}
}

type consoleSpans struct{}

func (consoleSpans) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		slog.Info("trace span", "event", "trace.span", "name", span.Name(), "trace_id", span.SpanContext().TraceID().String(), "span_id", span.SpanContext().SpanID().String(), "duration_ms", span.EndTime().Sub(span.StartTime()).Milliseconds(), "status", span.Status().Code.String())
	}
	return nil
}

func (consoleSpans) Shutdown(context.Context) error { return nil }

func runRabbit(ctx context.Context, address string, run func(*amqp.Connection) error) {
	for ctx.Err() == nil {
		conn, err := amqp.Dial(address)
		if err == nil {
			err = run(conn)
			_ = conn.Close()
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("RabbitMQ connection will retry", "event", "queue.reconnect")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func loadSessionConfig() (accesscontrol.SessionConfig, error) {
	absoluteTTL, err := parsePositiveDurationEnv("SESSION_ABSOLUTE_TTL")
	if err != nil {
		return accesscontrol.SessionConfig{}, err
	}

	idleTimeout, err := parsePositiveDurationEnv("SESSION_IDLE_TIMEOUT")
	if err != nil {
		return accesscontrol.SessionConfig{}, err
	}
	rotationInterval, err := parsePositiveDurationEnv("SESSION_ROTATION_INTERVAL")
	if err != nil {
		return accesscontrol.SessionConfig{}, err
	}
	gracePeriod, err := parsePositiveDurationEnv("SESSION_ROTATION_GRACE_PERIOD")
	if err != nil {
		return accesscontrol.SessionConfig{}, err
	}
	if gracePeriod >= rotationInterval {
		return accesscontrol.SessionConfig{}, fmt.Errorf("SESSION_ROTATION_GRACE_PERIOD must be shorter than SESSION_ROTATION_INTERVAL")
	}

	return accesscontrol.SessionConfig{
		AbsoluteTTL:      absoluteTTL,
		IdleTimeout:      idleTimeout,
		RotationInterval: rotationInterval,
		GracePeriod:      gracePeriod,
	}, nil
}

func loadCookieConfig() (CookieConfig, error) {
	value := strings.TrimSpace(os.Getenv("SESSION_COOKIE_SECURE"))
	if value == "" {
		return CookieConfig{}, fmt.Errorf("SESSION_COOKIE_SECURE must be set to true or false")
	}
	secure, err := strconv.ParseBool(value)
	if err != nil {
		return CookieConfig{}, fmt.Errorf("SESSION_COOKIE_SECURE must be true or false: %w", err)
	}
	return CookieConfig{Secure: secure}, nil
}

func parsePositiveDurationEnv(name string) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, fmt.Errorf("%s must be set", name)
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration (for example 30m or 12h): %w", name, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}

	return duration, nil
}
