package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"wbd/internal/models"
	"wbd/internal/service/dbmanagement"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	testPGImage    = "postgres:16-alpine"
	testPGUser     = "postgres"
	testPGPassword = "secret"
)

func startTestPostgres(t *testing.T, dbName string) *tcpostgres.PostgresContainer {
	t.Helper()
	container, err := tcpostgres.Run(context.Background(), testPGImage,
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername(testPGUser),
		tcpostgres.WithPassword(testPGPassword),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("failed to start postgres container for %s: %v", dbName, err)
	}
	return container
}

func openGorm(t *testing.T, container *tcpostgres.PostgresContainer) *gorm.DB {
	t.Helper()
	dsn, err := container.ConnectionString(context.Background(), "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return db
}

func TestIntegrationDBManagement(t *testing.T) {
	ctx := context.Background()
	const targetDBName = "target_db"

	internalContainer := startTestPostgres(t, "internal_db")
	targetContainer := startTestPostgres(t, targetDBName)

	host, err := targetContainer.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get target host: %v", err)
	}
	mappedPort, err := targetContainer.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("failed to get target port: %v", err)
	}
	portNum := int(mappedPort.Num())

	targetDB := openGorm(t, targetContainer)
	for _, stmt := range []string{
		`CREATE SCHEMA shop`,
		`CREATE TABLE shop.customers (
			id    SERIAL PRIMARY KEY,
			email VARCHAR(255) NOT NULL UNIQUE,
			name  TEXT
		)`,
		`INSERT INTO shop.customers (email, name) VALUES ('alice@example.com', 'Alice'), ('bob@example.com', 'Bob')`,
	} {
		if err := targetDB.Exec(stmt).Error; err != nil {
			t.Fatalf("failed to seed target database: %v", err)
		}
	}

	internalDB := openGorm(t, internalContainer)

	dbmSvc := dbmanagement.NewService(internalDB)
	defer dbmSvc.CloseAllTargetConnections()
	var dbID int
	mockAC := &mockAccessControlService{
		validateSessionFunc: func(_ context.Context, sessionID string) (int, error) {
			if sessionID != "integration-session" {
				t.Errorf("unexpected session ID: %s", sessionID)
			}
			return 7, nil
		},
		isAdminFunc: func(context.Context, int) (bool, error) { return false, nil },
		evaluateDBAccessFunc: func(_ context.Context, userID, targetDBID int, requiredLevel string) error {
			if userID != 7 || targetDBID != dbID || requiredLevel != "VIEW_ONLY" {
				t.Errorf("unexpected dashboard access check: user=%d db=%d level=%s", userID, targetDBID, requiredLevel)
			}
			return nil
		},
	}
	app := NewApp(mockAC, dbmSvc, nil)

	t.Run("RegisterTargetDB", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPost, "/admin/databases", models.TargetDBInfo{
			Name:     "integration_target",
			DBType:   "postgres",
			Host:     host,
			Port:     portNum,
			User:     testPGUser,
			Password: testPGPassword,
			DBName:   targetDBName,
		})
		rec := httptest.NewRecorder()
		app.handleRegisterTargetDB(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var stored models.TargetDBInfo
		if err := internalDB.Where("db_name = ?", targetDBName).First(&stored).Error; err != nil {
			t.Fatalf("expected target db to be stored in internal database, got error: %v", err)
		}
		if stored.ID <= 0 || stored.DBType != "postgres" || stored.Host != host || stored.Port != portNum {
			t.Fatalf("wrong stored target db info: %+v", stored)
		}
		if !strings.Contains(rec.Body.String(), fmt.Sprintf("DB ditambahkan dengan id %d", stored.ID)) {
			t.Errorf("wrong register response, got:\n %s", rec.Body.String())
		}
		dbID = stored.ID
	})

	if dbID == 0 {
		t.Fatal("target db was not registered, skipping remaining steps")
	}
	id := strconv.Itoa(dbID)

	t.Run("ListDatabasesAndDashboard", func(t *testing.T) {
		dbs, err := dbmSvc.ListDatabases(ctx)
		if err != nil {
			t.Fatalf("expected ListDatabases to succeed, got error: %v", err)
		}
		if len(dbs) == 0 {
			t.Fatalf("expected at least 1 database, got 0")
		}

		req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		req.AddCookie(&http.Cookie{Name: "session_id", Value: "integration-session"})
		rec := httptest.NewRecorder()
		app.handleDashboard(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "integration_target") || !strings.Contains(body, "/databases/"+id+"/schemas") || !strings.Contains(body, "/databases/"+id+"/migrations") {
			t.Errorf("expected database name, schemas link, and migrations link in dashboard, got:\n%s", body)
		}
	})

	t.Run("GetSchemas", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/databases/"+id+"/schemas", nil)
		req.SetPathValue("dbID", id)
		rec := httptest.NewRecorder()
		app.handleGetSchemas(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `shop`) || !strings.Contains(body, `public`) {
			t.Errorf("wrong schemas, got:\n %s", body)
		}
	})

	t.Run("GetTables", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/databases/"+id+"/schemas/shop/tables", nil)
		req.SetPathValue("dbID", id)
		req.SetPathValue("schema", "shop")
		rec := httptest.NewRecorder()
		app.handleGetTables(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `customers`) {
			t.Errorf("wrong tables, got:\n %s", body)
		}
	})

	t.Run("GetTableDetails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/databases/"+id+"/schemas/shop/tables/customers", nil)
		req.SetPathValue("dbID", id)
		req.SetPathValue("schema", "shop")
		req.SetPathValue("table", "customers")
		rec := httptest.NewRecorder()
		app.handleGetTableDetails(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `id`) || !strings.Contains(body, `integer`) || !strings.Contains(body, `email`) || !strings.Contains(body, `character varying`) || !strings.Contains(body, `name`) || !strings.Contains(body, `text`) || !strings.Contains(body, `customers_pkey`) || !strings.Contains(body, `PRIMARY KEY`) || !strings.Contains(body, `customers_email_key`) || !strings.Contains(body, `UNIQUE`) {
			t.Errorf("wrong table details, got:\n %s", body)
		}
	})

	t.Run("GetTableData", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/databases/"+id+"/schemas/shop/tables/customers/data?limit=10&offset=0", nil)
		req.SetPathValue("dbID", id)
		req.SetPathValue("schema", "shop")
		req.SetPathValue("table", "customers")
		rec := httptest.NewRecorder()
		app.handleGetTableData(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `alice@example.com`) || !strings.Contains(body, `Alice`) || !strings.Contains(body, `bob@example.com`) || !strings.Contains(body, `Bob`) {
			t.Errorf("wrong table data, got:\n %s", body)
		}
	})

	t.Run("GetTableDataPaginated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/databases/"+id+"/schemas/shop/tables/customers/data?limit=1&offset=0", nil)
		req.SetPathValue("dbID", id)
		req.SetPathValue("schema", "shop")
		req.SetPathValue("table", "customers")
		rec := httptest.NewRecorder()
		app.handleGetTableData(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if strings.Contains(body, `alice@example.com`) == strings.Contains(body, `bob@example.com`) {
			t.Errorf("expected exactly one row with limit=1, got:\n %s", body)
		}
	})

	t.Run("UnknownTargetDB", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/databases/9999/schemas", nil)
		req.SetPathValue("dbID", "9999")
		rec := httptest.NewRecorder()
		app.handleGetSchemas(rec, req)

		if rec.Code == http.StatusOK {
			t.Errorf("expected error status for unregistered target db, got 200:\n %s", rec.Body.String())
		}
	})
}
