package dbmanagement

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"wbd/internal/models"
	"wbd/internal/service/interfaces"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Compile-time check bahwa *Service mengimplementasikan kontrak DBManagementService.
var _ interfaces.DBManagementService = (*Service)(nil)

func newMockGormDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open gorm with sqlmock: %v", err)
	}
	return gormDB, mock, sqlDB
}

func TestConnectionManagement(t *testing.T) {
	ctx := context.Background()
	internalDB, internalMock, rawInternalDB := newMockGormDB(t)
	defer rawInternalDB.Close()

	svc := NewService(internalDB)

	t.Run("RegisterTargetDB", func(t *testing.T) {
		internalMock.ExpectBegin()
		internalMock.ExpectQuery(`INSERT INTO`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
		internalMock.ExpectCommit()

		dbID, err := svc.RegisterTargetDB(ctx, models.TargetDBInfo{
			Name:     "production_pg",
			DBType:   "postgres",
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "secret",
			DBName:   "prod_db",
		})
		if err != nil {
			t.Fatalf("expected RegisterTargetDB to succeed, got error: %v", err)
		}
		if dbID <= 0 {
			t.Errorf("expected positive targetDBID, got %d", dbID)
		}
	})

	t.Run("ListDatabases", func(t *testing.T) {
		internalMock.ExpectQuery(`SELECT .* FROM "target_db_infos"`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "db_type", "host", "port", "user", "password", "db_name"}).
				AddRow(1, "production_pg", "postgres", "localhost", 5432, "postgres", "secret", "prod_db").
				AddRow(2, "analytics_pg", "postgres", "localhost", 5432, "postgres", "secret", "analytics_db"))

		dbs, err := svc.ListDatabases(ctx)
		if err != nil {
			t.Fatalf("expected ListDatabases to succeed, got error: %v", err)
		}
		if len(dbs) != 2 || dbs[0].Name != "production_pg" || dbs[1].Name != "analytics_pg" {
			t.Errorf("unexpected databases list: %+v", dbs)
		}
	})

	t.Run("GetTargetConnection", func(t *testing.T) {
		targetDB, targetMock, err := sqlmock.NewWithDSN("sqlmock://postgres:secret@localhost:5432/prod_db?sslmode=disable")
		if err != nil {
			t.Fatalf("failed to create target sqlmock: %v", err)
		}
		targetMock.ExpectClose()
		defer targetDB.Close()

		internalMock.ExpectQuery(`SELECT .*`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "db_type", "host", "port", "user", "password", "db_name"}).
				AddRow(1, "production_pg", "sqlmock", "localhost", 5432, "postgres", "secret", "prod_db"))

		conn, err := svc.GetTargetConnection(ctx, 1)
		if err != nil || conn == nil {
			t.Fatalf("expected GetTargetConnection to succeed, got conn=%v err=%v", conn, err)
		}
		defer conn.Close()
	})

	t.Run("UnknownTargetDB", func(t *testing.T) {
		internalMock.ExpectQuery(`SELECT .*`).
			WillReturnError(sql.ErrNoRows)

		conn, err := svc.GetTargetConnection(ctx, 9999)
		if err == nil {
			t.Errorf("expected error for unregistered targetDBID, got nil")
		}
		if conn != nil {
			t.Errorf("expected nil connection on error, got %v", conn)
		}
	})
}

func TestDatabaseInformation(t *testing.T) {
	ctx := context.Background()
	internalDB, internalMock, rawInternalDB := newMockGormDB(t)
	defer rawInternalDB.Close()

	targetDB, targetMock, err := sqlmock.NewWithDSN("sqlmock://postgres:secret@localhost:5432/prod_db?sslmode=disable")
	if err != nil {
		t.Fatalf("failed to create target sqlmock: %v", err)
	}
	defer targetDB.Close()

	svc := NewService(internalDB)

	t.Run("GetSchemas", func(t *testing.T) {
		internalMock.ExpectQuery(`SELECT .*`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "db_type", "host", "port", "user", "password", "db_name"}).
				AddRow(1, "production_pg", "sqlmock", "localhost", 5432, "postgres", "secret", "prod_db"))

		targetMock.ExpectQuery(regexp.QuoteMeta(`SELECT schema_name FROM information_schema.schemata`)).
			WillReturnRows(sqlmock.NewRows([]string{"schema_name"}).
				AddRow("public").
				AddRow("analytics"))

		schemas, err := svc.GetSchemas(ctx, 1)
		if err != nil {
			t.Fatalf("expected GetSchemas to succeed, got error: %v", err)
		}
		if len(schemas) != 2 || schemas[0] != "public" || schemas[1] != "analytics" {
			t.Errorf("unexpected schemas: %v", schemas)
		}
	})

	t.Run("GetTables", func(t *testing.T) {
		internalMock.ExpectQuery(`SELECT .*`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "db_type", "host", "port", "user", "password", "db_name"}).
				AddRow(1, "production_pg", "sqlmock", "localhost", 5432, "postgres", "secret", "prod_db"))

		targetMock.ExpectQuery(regexp.QuoteMeta(`SELECT table_name FROM information_schema.tables`)).
			WithArgs("public").
			WillReturnRows(sqlmock.NewRows([]string{"table_name"}).
				AddRow("users").
				AddRow("orders"))

		tables, err := svc.GetTables(ctx, 1, "public")
		if err != nil {
			t.Fatalf("expected GetTables to succeed, got error: %v", err)
		}
		if len(tables) != 2 || tables[0] != "users" || tables[1] != "orders" {
			t.Errorf("unexpected tables: %v", tables)
		}
	})

	t.Run("GetTableDetails", func(t *testing.T) {
		internalMock.ExpectQuery(`SELECT .*`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "db_type", "host", "port", "user", "password", "db_name"}).
				AddRow(1, "production_pg", "sqlmock", "localhost", 5432, "postgres", "secret", "prod_db"))

		targetMock.ExpectQuery(regexp.QuoteMeta(`SELECT column_name, data_type, is_nullable FROM information_schema.columns`)).
			WithArgs("public", "users").
			WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable"}).
				AddRow("id", "integer", "NO").
				AddRow("email", "character varying", "YES"))

		targetMock.ExpectQuery(regexp.QuoteMeta(`SELECT constraint_name, constraint_type FROM information_schema.table_constraints`)).
			WithArgs("public", "users").
			WillReturnRows(sqlmock.NewRows([]string{"constraint_name", "constraint_type"}).
				AddRow("users_pkey", "PRIMARY KEY"))

		targetMock.ExpectQuery(regexp.QuoteMeta(`SELECT indexname, indexdef FROM pg_indexes`)).
			WithArgs("public", "users").
			WillReturnRows(sqlmock.NewRows([]string{"indexname", "indexdef"}).
				AddRow("users_pkey", "CREATE UNIQUE INDEX users_pkey ON public.users USING btree (id)"))

		meta, err := svc.GetTableDetails(ctx, 1, "public", "users")
		if err != nil || meta == nil {
			t.Fatalf("expected TableMetadata, got meta=%v err=%v", meta, err)
		}
		if meta.Schema != "public" || meta.TableName != "users" {
			t.Errorf("expected public.users, got %s.%s", meta.Schema, meta.TableName)
		}
		if len(meta.Columns) != 2 || meta.Columns[0].Name != "id" || meta.Columns[0].IsNullable {
			t.Errorf("unexpected columns mapping: %+v", meta.Columns)
		}
		if len(meta.Constraints) != 1 || meta.Constraints[0].Type != "PRIMARY KEY" {
			t.Errorf("unexpected constraints mapping: %+v", meta.Constraints)
		}
		if len(meta.Indexes) != 1 || !meta.Indexes[0].IsUnique {
			t.Errorf("unexpected indexes mapping: %+v", meta.Indexes)
		}
	})

	t.Run("GetTableData", func(t *testing.T) {
		internalMock.ExpectQuery(`SELECT .*`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "db_type", "host", "port", "user", "password", "db_name"}).
				AddRow(1, "production_pg", "sqlmock", "localhost", 5432, "postgres", "secret", "prod_db"))

		targetMock.ExpectQuery(`SELECT \* FROM "public"\."users" LIMIT \$1 OFFSET \$2`).
			WithArgs(10, 0).
			WillReturnRows(sqlmock.NewRows([]string{"id", "email"}).
				AddRow(1, "alice@example.com"))

		rows, err := svc.GetTableData(ctx, 1, "public", "users", 10, 0)
		if err != nil {
			t.Fatalf("expected GetTableData to succeed, got error: %v", err)
		}
		if len(rows) != 1 || rows[0]["email"] != "alice@example.com" {
			t.Errorf("unexpected table data rows: %+v", rows)
		}
	})

	t.Run("InvalidIdentifier", func(t *testing.T) {
		if _, err := svc.GetTableData(ctx, 1, "public; DROP TABLE users;--", "users", 10, 0); err == nil {
			t.Errorf("expected error for invalid schema identifier, got nil")
		}
	})

	t.Run("CloseAllTargetConnections", func(t *testing.T) {
		targetMock.ExpectClose()
		svc.CloseAllTargetConnections()
	})
}

func TestSQLiteSupport(t *testing.T) {
	ctx := context.Background()
	internalDB, internalMock, rawInternalDB := newMockGormDB(t)
	defer rawInternalDB.Close()

	dbFile := t.TempDir() + "/sqlite_test.db"
	rawSQLite, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("failed to open raw sqlite: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sku TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			price REAL
		)`,
		`INSERT INTO items (sku, title, price) VALUES ('SKU-1', 'Keyboard', 49.99)`,
	} {
		if _, err := rawSQLite.Exec(stmt); err != nil {
			t.Fatalf("failed to seed sqlite: %v", err)
		}
	}
	rawSQLite.Close()

	svc := NewService(internalDB)

	// 1. Register SQLite database
	internalMock.ExpectBegin()
	internalMock.ExpectQuery(`INSERT INTO`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(5))
	internalMock.ExpectCommit()

	dbID, err := svc.RegisterTargetDB(ctx, models.TargetDBInfo{
		Name:   "SQLite Catalog",
		DBType: "sqlite",
		DBName: dbFile,
	})
	if err != nil || dbID != 5 {
		t.Fatalf("expected SQLite register to succeed with id=5, got id=%d err=%v", dbID, err)
	}

	// Mock DB info lookup
	internalMock.ExpectQuery(`SELECT .*`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "db_type", "host", "port", "user", "password", "db_name"}).
			AddRow(5, "SQLite Catalog", "sqlite", "", 0, "", "", dbFile))

	// 2. Fetch Schemas
	schemas, err := svc.GetSchemas(ctx, 5)
	if err != nil {
		t.Fatalf("expected GetSchemas to succeed for SQLite: %v", err)
	}
	if len(schemas) == 0 || schemas[0] != "main" {
		t.Errorf("unexpected schemas for SQLite: %v", schemas)
	}

	// 3. Fetch Tables
	tables, err := svc.GetTables(ctx, 5, "main")
	if err != nil {
		t.Fatalf("expected GetTables to succeed for SQLite: %v", err)
	}
	if len(tables) != 1 || tables[0] != "items" {
		t.Fatalf("expected [items] table for SQLite, got %v", tables)
	}

	// 4. Fetch Table Details (columns, constraints, indexes)
	details, err := svc.GetTableDetails(ctx, 5, "main", "items")
	if err != nil {
		t.Fatalf("expected GetTableDetails to succeed for SQLite: %v", err)
	}
	if details.TableName != "items" {
		t.Errorf("expected items table name, got %s", details.TableName)
	}
	if len(details.Columns) != 4 {
		t.Errorf("expected 4 columns for SQLite items table, got %d", len(details.Columns))
	}
	hasPK := false
	for _, c := range details.Constraints {
		if c.Type == "PRIMARY KEY" {
			hasPK = true
		}
	}
	if !hasPK {
		t.Errorf("expected PRIMARY KEY constraint for SQLite items, got %v", details.Constraints)
	}
	hasUnique := false
	for _, idx := range details.Indexes {
		if idx.IsUnique {
			hasUnique = true
		}
	}
	if !hasUnique {
		t.Errorf("expected unique index on sku for SQLite items, got %v", details.Indexes)
	}

	// 5. Fetch Table Data
	rows, err := svc.GetTableData(ctx, 5, "main", "items", 10, 0)
	if err != nil {
		t.Fatalf("expected GetTableData to succeed for SQLite: %v", err)
	}
	if len(rows) != 1 || rows[0]["sku"] != "SKU-1" {
		t.Errorf("unexpected rows data from SQLite items: %v", rows)
	}

	svc.CloseAllTargetConnections()
}

func TestMySQLSupport(t *testing.T) {
	ctx := context.Background()
	internalDB, internalMock, rawInternalDB := newMockGormDB(t)
	defer rawInternalDB.Close()

	// Use sqlmock to mock MySQL target behavior
	targetDB, targetMock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create target sqlmock: %v", err)
	}
	defer targetDB.Close()

	svc := NewService(internalDB)

	// Register MySQL target DB
	internalMock.ExpectBegin()
	internalMock.ExpectQuery(`INSERT INTO`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))
	internalMock.ExpectCommit()

	dbID, err := svc.RegisterTargetDB(ctx, models.TargetDBInfo{
		Name:     "MySQL Store",
		DBType:   "mysql",
		Host:     "127.0.0.1",
		Port:     3306,
		User:     "root",
		Password: "secret",
		DBName:   "store_db",
	})
	if err != nil || dbID != 8 {
		t.Fatalf("expected RegisterTargetDB for MySQL to succeed with id=8, got id=%d err=%v", dbID, err)
	}

	// Mock internal DB lookup
	internalMock.ExpectQuery(`SELECT .*`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "db_type", "host", "port", "user", "password", "db_name"}).
			AddRow(8, "MySQL Store", "mysql", "127.0.0.1", 3306, "root", "secret", "store_db"))

	// Pre-populate connection in service so it doesn't try to open real TCP connection to 127.0.0.1:3306
	svc.conns.Store(8, targetDB)
	svc.infos.Store(8, models.TargetDBInfo{
		ID:       8,
		Name:     "MySQL Store",
		DBType:   "mysql",
		Host:     "127.0.0.1",
		Port:     3306,
		User:     "root",
		Password: "secret",
		DBName:   "store_db",
	})

	// 1. Get Schemas
	targetMock.ExpectQuery(regexp.QuoteMeta(`SELECT schema_name FROM information_schema.schemata WHERE schema_name = ?`)).
		WithArgs("store_db").
		WillReturnRows(sqlmock.NewRows([]string{"schema_name"}).AddRow("store_db"))

	schemas, err := svc.GetSchemas(ctx, 8)
	if err != nil {
		t.Fatalf("expected GetSchemas for MySQL to succeed: %v", err)
	}
	if len(schemas) != 1 || schemas[0] != "store_db" {
		t.Errorf("unexpected schemas for MySQL: %v", schemas)
	}

	// 2. Get Tables
	targetMock.ExpectQuery(regexp.QuoteMeta(`SELECT table_name FROM information_schema.tables WHERE table_schema = ?`)).
		WithArgs("store_db").
		WillReturnRows(sqlmock.NewRows([]string{"table_name"}).AddRow("orders").AddRow("customers"))

	tables, err := svc.GetTables(ctx, 8, "store_db")
	if err != nil {
		t.Fatalf("expected GetTables for MySQL to succeed: %v", err)
	}
	if len(tables) != 2 || tables[0] != "orders" || tables[1] != "customers" {
		t.Errorf("unexpected tables for MySQL: %v", tables)
	}

	// 3. Get Table Details
	targetMock.ExpectQuery(regexp.QuoteMeta(`SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = ? AND table_name = ?`)).
		WithArgs("store_db", "orders").
		WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable"}).
			AddRow("id", "bigint", "NO").
			AddRow("total", "decimal", "YES"))

	targetMock.ExpectQuery(regexp.QuoteMeta(`SELECT constraint_name, constraint_type FROM information_schema.table_constraints WHERE table_schema = ? AND table_name = ?`)).
		WithArgs("store_db", "orders").
		WillReturnRows(sqlmock.NewRows([]string{"constraint_name", "constraint_type"}).
			AddRow("PRIMARY", "PRIMARY KEY"))

	targetMock.ExpectQuery(regexp.QuoteMeta(`SELECT DISTINCT index_name, non_unique FROM information_schema.statistics WHERE table_schema = ? AND table_name = ?`)).
		WithArgs("store_db", "orders").
		WillReturnRows(sqlmock.NewRows([]string{"index_name", "non_unique"}).
			AddRow("PRIMARY", 0))

	details, err := svc.GetTableDetails(ctx, 8, "store_db", "orders")
	if err != nil {
		t.Fatalf("expected GetTableDetails for MySQL to succeed: %v", err)
	}
	if details.TableName != "orders" || len(details.Columns) != 2 || len(details.Constraints) != 1 || len(details.Indexes) != 1 {
		t.Errorf("unexpected MySQL table details: %+v", details)
	}
	if !details.Indexes[0].IsUnique {
		t.Errorf("expected MySQL PRIMARY index to be marked unique")
	}

	// 4. Get Table Data
	targetMock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `store_db`.`orders` LIMIT ? OFFSET ?")).
		WithArgs(10, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "total"}).AddRow(1, 100.5))

	rows, err := svc.GetTableData(ctx, 8, "store_db", "orders", 10, 0)
	if err != nil {
		t.Fatalf("expected GetTableData for MySQL to succeed: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("unexpected rows count for MySQL: %v", rows)
	}

	targetMock.ExpectClose()
	svc.CloseAllTargetConnections()
}

func TestErrorHandling(t *testing.T) {
	ctx := context.Background()
	internalDB, internalMock, rawInternalDB := newMockGormDB(t)
	defer rawInternalDB.Close()

	svc := NewService(internalDB)

	t.Run("UnknownTargetDB", func(t *testing.T) {
		internalMock.ExpectQuery(`SELECT .*`).
			WillReturnError(sql.ErrNoRows)

		conn, err := svc.GetTargetConnection(ctx, 8888)
		if err == nil {
			t.Errorf("expected error for unregistered targetDBID, got nil")
		}
		if conn != nil {
			t.Errorf("expected nil connection on error, got %v", conn)
		}
	})
}
