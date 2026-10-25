package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"wbd/internal/models"
	"wbd/internal/service/dbmanagement"

	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
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

func startTestMySQL(t *testing.T, dbName string) (host string, port int, user, password string) {
	t.Helper()
	ctx := context.Background()
	container, err := tcmysql.Run(ctx, "mysql:8.0",
		tcmysql.WithDatabase(dbName),
		tcmysql.WithUsername("testuser"),
		tcmysql.WithPassword("secret"),
	)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("failed to start mysql container for %s: %v", dbName, err)
	}
	host, err = container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get mysql host: %v", err)
	}
	mappedPort, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("failed to get mysql port: %v", err)
	}
	return host, int(mappedPort.Num()), "testuser", "secret"
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
				return 0, fmt.Errorf("unexpected session ID: %s", sessionID)
			}
			return 1, nil
		},
		isAdminFunc: func(_ context.Context, userID int) (bool, error) {
			return true, nil
		},
		evaluateDBAccessFunc: func(_ context.Context, userID, targetDBID int, requiredLevel string) error {
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

	t.Run("SQLite_RegisterAndTableInfo", func(t *testing.T) {
		sqliteFile := t.TempDir() + "/sqlite_integration.db"
		rawDB, err := sql.Open("sqlite", sqliteFile)
		if err != nil {
			t.Fatalf("failed to open raw sqlite: %v", err)
		}
		for _, stmt := range []string{
			`CREATE TABLE catalog (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				sku TEXT NOT NULL UNIQUE,
				name TEXT NOT NULL,
				stock INTEGER
			)`,
			`INSERT INTO catalog (sku, name, stock) VALUES ('SKU-001', 'Monitor', 15), ('SKU-002', 'Mouse', 40)`,
		} {
			if _, err := rawDB.Exec(stmt); err != nil {
				t.Fatalf("failed to seed sqlite: %v", err)
			}
		}
		rawDB.Close()

		req := newJSONRequest(t, http.MethodPost, "/admin/databases", models.TargetDBInfo{
			Name:   "Target SQLite Integration",
			DBType: "sqlite",
			DBName: sqliteFile,
		})
		rec := httptest.NewRecorder()
		app.handleRegisterTargetDB(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 on SQLite registration, got %d: %s", rec.Code, rec.Body.String())
		}

		var stored models.TargetDBInfo
		if err := internalDB.Where("name = ?", "Target SQLite Integration").First(&stored).Error; err != nil {
			t.Fatalf("failed to find stored sqlite target db: %v", err)
		}
		sqliteID := strconv.Itoa(stored.ID)

		// 1. Get Schemas
		reqSchemas := httptest.NewRequest(http.MethodGet, "/databases/"+sqliteID+"/schemas", nil)
		reqSchemas.SetPathValue("dbID", sqliteID)
		recSchemas := httptest.NewRecorder()
		app.handleGetSchemas(recSchemas, reqSchemas)
		if recSchemas.Code != http.StatusOK || !strings.Contains(recSchemas.Body.String(), "main") {
			t.Fatalf("expected 200 and schema 'main', got code=%d body=%s", recSchemas.Code, recSchemas.Body.String())
		}

		// 2. Get Tables
		reqTables := httptest.NewRequest(http.MethodGet, "/databases/"+sqliteID+"/schemas/main/tables", nil)
		reqTables.SetPathValue("dbID", sqliteID)
		reqTables.SetPathValue("schema", "main")
		recTables := httptest.NewRecorder()
		app.handleGetTables(recTables, reqTables)
		if recTables.Code != http.StatusOK || !strings.Contains(recTables.Body.String(), "catalog") {
			t.Fatalf("expected 200 and table 'catalog', got code=%d body=%s", recTables.Code, recTables.Body.String())
		}

		// 3. Get Table Details
		reqDetails := httptest.NewRequest(http.MethodGet, "/databases/"+sqliteID+"/schemas/main/tables/catalog", nil)
		reqDetails.SetPathValue("dbID", sqliteID)
		reqDetails.SetPathValue("schema", "main")
		reqDetails.SetPathValue("table", "catalog")
		recDetails := httptest.NewRecorder()
		app.handleGetTableDetails(recDetails, reqDetails)
		if recDetails.Code != http.StatusOK || !strings.Contains(recDetails.Body.String(), "sku") || !strings.Contains(recDetails.Body.String(), "PRIMARY KEY") {
			t.Fatalf("expected 200 and columns/constraints for catalog, got code=%d body=%s", recDetails.Code, recDetails.Body.String())
		}
	})

	t.Run("MySQL_RegisterAndTableInfo", func(t *testing.T) {
		const mysqlDBName = "integration_mysql"
		mHost, mPort, mUser, mPass := startTestMySQL(t, mysqlDBName)

		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true", mUser, mPass, mHost, mPort, mysqlDBName)
		rawDB, err := sql.Open("mysql", dsn)
		if err != nil {
			t.Fatalf("failed to open test mysql: %v", err)
		}
		for _, stmt := range []string{
			`CREATE TABLE products (
				id INT AUTO_INCREMENT PRIMARY KEY,
				name VARCHAR(255) NOT NULL,
				price DECIMAL(10, 2)
			)`,
			`INSERT INTO products (name, price) VALUES ('Mechanical Keyboard', 89.99)`,
		} {
			if _, err := rawDB.Exec(stmt); err != nil {
				t.Fatalf("failed to seed mysql: %v", err)
			}
		}
		rawDB.Close()

		req := newJSONRequest(t, http.MethodPost, "/admin/databases", models.TargetDBInfo{
			Name:     "Target MySQL Integration",
			DBType:   "mysql",
			Host:     mHost,
			Port:     mPort,
			User:     mUser,
			Password: mPass,
			DBName:   mysqlDBName,
		})
		rec := httptest.NewRecorder()
		app.handleRegisterTargetDB(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 on MySQL registration, got %d: %s", rec.Code, rec.Body.String())
		}

		var stored models.TargetDBInfo
		if err := internalDB.Where("name = ?", "Target MySQL Integration").First(&stored).Error; err != nil {
			t.Fatalf("failed to find stored mysql target db: %v", err)
		}
		mysqlID := strconv.Itoa(stored.ID)

		// 1. Get Schemas
		reqSchemas := httptest.NewRequest(http.MethodGet, "/databases/"+mysqlID+"/schemas", nil)
		reqSchemas.SetPathValue("dbID", mysqlID)
		recSchemas := httptest.NewRecorder()
		app.handleGetSchemas(recSchemas, reqSchemas)
		if recSchemas.Code != http.StatusOK || !strings.Contains(recSchemas.Body.String(), mysqlDBName) {
			t.Fatalf("expected 200 and schema '%s', got code=%d body=%s", mysqlDBName, recSchemas.Code, recSchemas.Body.String())
		}

		// 2. Get Tables
		reqTables := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/databases/%s/schemas/%s/tables", mysqlID, mysqlDBName), nil)
		reqTables.SetPathValue("dbID", mysqlID)
		reqTables.SetPathValue("schema", mysqlDBName)
		recTables := httptest.NewRecorder()
		app.handleGetTables(recTables, reqTables)
		if recTables.Code != http.StatusOK || !strings.Contains(recTables.Body.String(), "products") {
			t.Fatalf("expected 200 and table 'products', got code=%d body=%s", recTables.Code, recTables.Body.String())
		}

		// 3. Get Table Details
		reqDetails := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/databases/%s/schemas/%s/tables/products", mysqlID, mysqlDBName), nil)
		reqDetails.SetPathValue("dbID", mysqlID)
		reqDetails.SetPathValue("schema", mysqlDBName)
		reqDetails.SetPathValue("table", "products")
		recDetails := httptest.NewRecorder()
		app.handleGetTableDetails(recDetails, reqDetails)
		if recDetails.Code != http.StatusOK || !strings.Contains(recDetails.Body.String(), "price") || !strings.Contains(recDetails.Body.String(), "PRIMARY KEY") {
			t.Fatalf("expected 200 and details for products, got code=%d body=%s", recDetails.Code, recDetails.Body.String())
		}
	})
}
