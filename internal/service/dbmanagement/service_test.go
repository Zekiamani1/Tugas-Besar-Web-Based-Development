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
