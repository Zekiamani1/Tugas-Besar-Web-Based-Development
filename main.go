package main

import (
	"fmt"
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/dbmanagement"
	"wbd/internal/service/migration"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	sessionConfig, err := loadSessionConfig()
	if err != nil {
		log.Fatalf("invalid session configuration: %v", err)
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

	app := NewApp(acSvc, dbmSvc, migSvc)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, app); err != nil {
		log.Printf("server stopped: %v", err)
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

	return accesscontrol.SessionConfig{
		AbsoluteTTL: absoluteTTL,
		IdleTimeout: idleTimeout,
	}, nil
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
