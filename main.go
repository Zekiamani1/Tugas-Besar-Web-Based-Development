package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/dbmanagement"
	"wbd/internal/service/migration"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
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

	acSvc := accesscontrol.NewService(internalDB)
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
