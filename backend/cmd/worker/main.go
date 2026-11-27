package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"wbd/internal/models"
	"wbd/internal/observability"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/dbmanagement"
	"wbd/internal/service/migration"
)

func main() {
	telemetry, err := observability.Setup(context.Background(), "database-platform-worker")
	if err != nil {
		log.Fatalf("failed to initialize observability: %v", err)
	}
	defer observability.Shutdown(telemetry)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=postgres dbname=wbd port=5432 sslmode=disable"
	}
	internalDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: observability.NewGormLogger()})
	if err != nil {
		log.Fatalf("failed to connect internal database: %v", err)
	}
	if err := observability.RegisterGormQueryStart(internalDB); err != nil {
		log.Fatalf("failed to initialize database logging: %v", err)
	}
	sqlDB, err := internalDB.DB()
	if err != nil {
		log.Fatalf("failed to get sql.DB handle: %v", err)
	}
	defer sqlDB.Close()

	acSvc := accesscontrol.NewService(internalDB, accesscontrol.SessionConfig{})
	dbmSvc := dbmanagement.NewService(internalDB)
	defer dbmSvc.CloseAllTargetConnections()
	migSvc := migration.NewService(internalDB, dbmSvc)
	migSvc.SetJobValidator(func(ctx context.Context, job models.MigrationJob) error {
		userID, err := strconv.Atoi(job.Actor)
		if err != nil {
			return err
		}
		return acSvc.EvaluateDBAccess(ctx, userID, job.TargetDBID, accesscontrol.AccessLevelViewEdit)
	})
	if err := migSvc.Initialize(context.Background()); err != nil {
		log.Fatalf("failed to initialize migration history: %v", err)
	}
	if err := observability.RegisterDatabaseMetrics(sqlDB); err != nil {
		log.Fatalf("failed to initialize database metrics: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	rabbitURL := os.Getenv("RABBITMQ_URL")
	if rabbitURL == "" {
		log.Fatal("RABBITMQ_URL must be set")
	}
	runRabbit(ctx, rabbitURL, func(conn *amqp.Connection) error {
		return migSvc.RunMigrationWorker(ctx, conn, "migration_jobs")
	})
}

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
