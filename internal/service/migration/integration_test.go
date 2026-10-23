package migration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type postgresTargetProvider struct {
	targetProvider
	dsn string
}

func (p postgresTargetProvider) GetTargetConnection(_ context.Context, _ int) (*sql.DB, error) {
	return sql.Open("pgx", p.dsn)
}

func TestMigrationWithPostgres(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set MIGRATION_TEST_DSN to a disposable PostgreSQL database URL")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatal("MIGRATION_TEST_DSN must be a PostgreSQL URL")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })

	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean up test schema: %v", err)
		}
	})

	internalURL := *parsed
	query := internalURL.Query()
	query.Set("search_path", schema)
	internalURL.RawQuery = query.Encode()
	internalDB, err := gorm.Open(postgres.Open(internalURL.String()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	internalSQL, err := internalDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer internalSQL.Close()

	svc := NewService(internalDB, postgresTargetProvider{dsn: dsn})
	if err := svc.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	id, err := svc.CreateDraft(ctx, 1, "CREATE TABLE "+schema+".example (id integer)")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateDraft(ctx, 1, id, "CREATE TABLE "+schema+".example (id integer PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetMigrationDetail(ctx, 2, id); err == nil {
		t.Fatal("detail was visible through another target database")
	}
	applied, err := svc.ExecuteMigration(ctx, 1, id, "alice")
	if err != nil || applied.Status != StatusApplied || applied.Version != 1 {
		t.Fatalf("first execution: record=%+v err=%v", applied, err)
	}
	if err := svc.UpdateDraft(ctx, 1, id, "SELECT 1"); err != ErrDraftNotFound {
		t.Fatalf("applied migration was editable: %v", err)
	}
	if _, err := svc.ExecuteMigration(ctx, 1, id, "alice"); err != ErrDraftNotFound {
		t.Fatalf("applied migration was executable again: %v", err)
	}

	failingSQL := "DO $$ BEGIN CREATE TABLE " + schema + ".rolled_back (id integer); RAISE EXCEPTION 'expected failure'; END $$"
	failedID, err := svc.CreateDraft(ctx, 1, failingSQL)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := svc.ExecuteMigration(ctx, 1, failedID, "alice")
	if err == nil || failed == nil || failed.Status != StatusFailed || failed.Version != 0 {
		t.Fatalf("failed execution: record=%+v err=%v", failed, err)
	}
	var count int
	if err := admin.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'rolled_back'", schema,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed SQL was not rolled back: count=%d err=%v", count, err)
	}

	secondID, err := svc.CreateDraft(ctx, 1, "ALTER TABLE "+schema+".example ADD COLUMN name text")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ExecuteMigration(ctx, 1, secondID, "bob")
	if err != nil || second.Version != 2 {
		t.Fatalf("second applied version: record=%+v err=%v", second, err)
	}
	history, err := svc.GetLinearHistory(ctx, 1)
	if err != nil || len(history) != 3 {
		t.Fatalf("history: count=%d err=%v", len(history), err)
	}
	if history[0].ID != id || history[1].ID != failedID || history[2].ID != secondID {
		t.Fatalf("history is not in execution order: %+v", history)
	}
	for _, record := range history {
		if strings.TrimSpace(record.SQLQuery) == "" || record.ExecutedAt.IsZero() {
			t.Fatalf("incomplete history entry: %+v", record)
		}
	}

	firstConcurrentID, err := svc.CreateDraft(ctx, 1, "CREATE TABLE "+schema+".concurrent_a (id integer)")
	if err != nil {
		t.Fatal(err)
	}
	secondConcurrentID, err := svc.CreateDraft(ctx, 1, "CREATE TABLE "+schema+".concurrent_b (id integer)")
	if err != nil {
		t.Fatal(err)
	}
	type executionResult struct {
		recordVersion int
		err           error
	}
	results := make(chan executionResult, 2)
	for _, migrationID := range []int{firstConcurrentID, secondConcurrentID} {
		go func(id int) {
			record, execErr := svc.ExecuteMigration(ctx, 1, id, "alice")
			if execErr != nil {
				results <- executionResult{err: execErr}
				return
			}
			results <- executionResult{recordVersion: record.Version}
		}(migrationID)
	}
	versions := make(map[int]bool)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		versions[result.recordVersion] = true
	}
	if !versions[3] || !versions[4] || len(versions) != 2 {
		t.Fatalf("concurrent executions did not receive linear versions: %+v", versions)
	}

	otherSchema := schema + "_other"
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+otherSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+otherSchema+" CASCADE"); err != nil {
			t.Errorf("clean up second target schema: %v", err)
		}
	})
	otherID, err := svc.CreateDraft(ctx, 2, "CREATE TABLE "+otherSchema+".example (id integer)")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.ExecuteMigration(ctx, 2, otherID, "carol")
	if err != nil || other.Version != 1 || other.Status != StatusApplied {
		t.Fatalf("second target did not start at version 1: record=%+v err=%v", other, err)
	}
	otherHistory, err := svc.GetLinearHistory(ctx, 2)
	if err != nil || len(otherHistory) != 1 || otherHistory[0].ID != otherID {
		t.Fatalf("second target history is not isolated: history=%+v err=%v", otherHistory, err)
	}
	firstHistory, err := svc.GetLinearHistory(ctx, 1)
	if err != nil || len(firstHistory) != 5 {
		t.Fatalf("first target history changed: history=%+v err=%v", firstHistory, err)
	}
}
