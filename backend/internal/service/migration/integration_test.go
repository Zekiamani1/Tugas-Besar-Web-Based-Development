package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"wbd/internal/models"
)

type postgresTargetProvider struct {
	targetProvider
	dsn string
	t   *testing.T
}

func (p postgresTargetProvider) GetTargetConnection(_ context.Context, _ int) (*sql.DB, error) {
	conn, err := sql.Open("pgx", p.dsn)
	if err == nil {
		p.t.Cleanup(func() { conn.Close() })
	}
	return conn, err
}

func newPostgresService(t *testing.T) (*Service, *gorm.DB, *sql.DB, string) {
	t.Helper()
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
	t.Cleanup(func() { internalSQL.Close() })

	svc := NewService(internalDB, postgresTargetProvider{dsn: dsn, t: t})
	return svc, internalDB, admin, schema
}

func TestMigrationWithPostgres(t *testing.T) {
	svc, internalDB, admin, schema := newPostgresService(t)
	ctx := context.Background()
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
	detail, err := svc.GetMigrationDetail(ctx, 1, id)
	if err != nil || detail.ExecutionID != applied.ExecutionID || detail.SQLQuery != applied.SQLQuery || detail.Status != StatusApplied {
		t.Fatalf("detail does not reflect its execution: detail=%+v err=%v", detail, err)
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

	var definition models.Migration
	if err := internalDB.First(&definition, id).Error; err != nil {
		t.Fatal(err)
	}
	if definition.SQLUp != applied.SQLQuery || definition.Revision != 2 || !definition.Irreversible {
		t.Fatalf("execution changed definition: %+v", definition)
	}
	attempts, err := svc.GetExecutionHistory(ctx, 1)
	if err != nil || len(attempts) != 3 {
		t.Fatalf("attempts=%+v err=%v", attempts, err)
	}
	if attempts[0].MigrationRevision != 2 || attempts[0].SQLSnapshot != definition.SQLUp || attempts[0].FinishedAt == nil || attempts[1].Error == "" {
		t.Fatalf("incomplete attempts: %+v", attempts)
	}
	if err := svc.UpdateDraft(ctx, 1, failedID, "SELECT 1"); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("failed attempt's SQL was editable: %v", err)
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
			deadline := time.Now().Add(5 * time.Second)
			for errors.Is(execErr, ErrDatabaseBusy) && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
				record, execErr = svc.ExecuteMigration(ctx, 1, id, "alice")
			}
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

func TestImportM1IsIdempotent(t *testing.T) {
	svc, db, _, _ := newPostgresService(t)
	ctx := context.Background()
	if err := db.Exec(`CREATE TABLE migration_records (
		id BIGSERIAL PRIMARY KEY, target_db_id BIGINT, version BIGINT, sql_query TEXT,
		executed_by TEXT, status TEXT, executed_at TIMESTAMPTZ)`).Error; err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 11, 1, 1, 2, 3, 0, time.UTC)
	for _, row := range []struct {
		id, target, version int
		sql, status, actor  string
	}{
		{41, 1, 0, "SELECT 1", StatusDraft, ""},
		{42, 1, 1, "SELECT 2", StatusApplied, "alice"},
		{43, 1, 0, "SELECT broken", StatusFailed, "bob"},
		{44, 2, 1, "SELECT 4", StatusApplied, "carol"},
	} {
		if err := db.Exec(`INSERT INTO migration_records VALUES (?, ?, ?, ?, ?, ?, ?)`,
			row.id, row.target, row.version, row.sql, row.actor, row.status, when).Error; err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := svc.Initialize(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var definitions []models.Migration
	if err := db.Order("id").Find(&definitions).Error; err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 4 {
		t.Fatalf("definitions=%+v", definitions)
	}
	for _, definition := range definitions {
		if !definition.Irreversible || definition.IrreversibleReason == "" || definition.SQLDown != "" || definition.Revision != 1 {
			t.Fatalf("unsafe imported definition: %+v", definition)
		}
	}
	var count int64
	if err := db.Table("migration_records").Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("M1 archive changed: count=%d err=%v", count, err)
	}
	history, err := svc.GetExecutionHistory(ctx, 1)
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if history[0].MigrationID != 42 || history[0].SQLSnapshot != "SELECT 2" || history[0].Version != 1 || history[0].ExecutedBy != "alice" || !history[0].StartedAt.Equal(when) || history[0].LegacyMigrationID == nil {
		t.Fatalf("M1 success changed: %+v", history[0])
	}
	if history[1].Status != StatusFailed || history[1].SQLSnapshot != "SELECT broken" {
		t.Fatalf("M1 failure lost: %+v", history[1])
	}
	drafts, err := svc.GetDrafts(ctx, 1)
	if err != nil || len(drafts) != 1 || drafts[0].ID != 41 {
		t.Fatalf("drafts=%+v err=%v", drafts, err)
	}
	other, err := svc.GetExecutionHistory(ctx, 2)
	if err != nil || len(other) != 1 || other[0].MigrationID != 44 {
		t.Fatalf("other=%+v err=%v", other, err)
	}
	if err := svc.UpdateDraft(ctx, 1, 42, "SELECT 0"); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("imported execution editable: %v", err)
	}
	if err := svc.UpdateDraft(ctx, 1, 41, "SELECT 100"); err != nil {
		t.Fatal(err)
	}
	newID, err := svc.CreateDraft(ctx, 1, "SELECT 200")
	if err != nil || newID <= 44 {
		t.Fatalf("sequence collides: id=%d err=%v", newID, err)
	}
	if err := svc.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.GetMigrationDetail(ctx, 1, 41)
	if err != nil || detail.SQLQuery != "SELECT 100" {
		t.Fatalf("reimport overwrote draft: detail=%+v err=%v", detail, err)
	}
	again, err := svc.GetExecutionHistory(ctx, 1)
	if err != nil || len(again) != 2 || again[0].ID != history[0].ID || again[1].ID != history[1].ID {
		t.Fatalf("reimport duplicated history: %+v err=%v", again, err)
	}
}

func TestPartialBatchCompensationPreservesPreviousPosition(t *testing.T) {
	svc, db, target, schema := newPostgresService(t)
	ctx := context.Background()
	targetURL, err := url.Parse(os.Getenv("MIGRATION_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	query := targetURL.Query()
	query.Set("search_path", schema)
	targetURL.RawQuery = query.Encode()
	svc.dbMgmt = postgresTargetProvider{dsn: targetURL.String(), t: t}
	if err := svc.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	priorID, err := svc.CreateDraft(ctx, 1, "CREATE TABLE "+schema+".prior (id integer)")
	if err != nil {
		t.Fatal(err)
	}
	prior, err := svc.ExecuteMigration(ctx, 1, priorID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ExecContext(ctx, "CREATE TABLE "+schema+".items (id bigint PRIMARY KEY, value integer NOT NULL); INSERT INTO "+schema+".items SELECT n, 0 FROM generate_series(1, 4) n"); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ExecContext(ctx, "CREATE FUNCTION "+schema+".reject_later_batch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id = 3 AND NEW.value = 1 THEN RAISE EXCEPTION 'later batch failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_later_batch BEFORE UPDATE ON "+schema+".items FOR EACH ROW EXECUTE FUNCTION "+schema+".reject_later_batch()"); err != nil {
		t.Fatal(err)
	}
	migrationID, err := svc.CreateDraftDefinition(ctx, 1, models.MigrationDraft{SQLUp: "UPDATE " + schema + ".items SET value=value+1 WHERE {{batch_predicate}}", SQLDown: "UPDATE " + schema + ".items SET value=value-1 WHERE {{batch_predicate}}"})
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := svc.SubmitJob(ctx, models.MigrationJob{TargetDBID: 1, MigrationID: migrationID, Actor: "alice", Action: ActionApply, Mode: "BATCH", BatchSize: 2, BatchTable: schema + ".items", BatchAttribute: "id", Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessJob(ctx, jobID, svc.validateAsyncJob, svc.executeAsyncJob); err != nil {
		t.Fatal(err)
	}
	partial, err := svc.FindJob(ctx, 1, migrationID, jobID)
	if err != nil || partial.Status != StatusPartial || partial.CommittedRows != 2 {
		t.Fatalf("partial job: %+v, err=%v", partial, err)
	}
	rollbackID, err := svc.SubmitJob(ctx, models.MigrationJob{TargetDBID: 1, MigrationID: migrationID, Actor: "alice", Action: ActionRollback, Mode: "BATCH", BatchSize: 2, BatchTable: schema + ".items", BatchAttribute: "id", ExpectedExecutionID: int(partial.ExecutionID)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE FUNCTION reject_partial_finalization() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.status = 'PARTIAL' AND NEW.status = 'FAILED' THEN RAISE EXCEPTION 'metadata write failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_partial_finalization BEFORE UPDATE ON migration_jobs FOR EACH ROW EXECUTE FUNCTION reject_partial_finalization()").Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessJob(ctx, rollbackID, svc.validateAsyncJob, svc.executeAsyncJob); !errors.Is(err, ErrIntegrationPending) {
		t.Fatalf("metadata failure should leave compensation uncertain: %v", err)
	}
	var sourceExecution models.MigrationExecution
	if err := db.First(&sourceExecution, partial.ExecutionID).Error; err != nil || sourceExecution.Status != StatusPartial {
		t.Fatalf("source execution should remain partial: %+v, err=%v", sourceExecution, err)
	}
	if err := db.Exec("DROP TRIGGER reject_partial_finalization ON migration_jobs; DROP FUNCTION reject_partial_finalization()").Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessJob(ctx, rollbackID, svc.validateAsyncJob, svc.executeAsyncJob); err != nil {
		t.Fatalf("resume compensation finalization: %v", err)
	}
	rollback, err := svc.FindJob(ctx, 1, migrationID, rollbackID)
	if err != nil || rollback.Status != "SUCCEEDED" {
		t.Fatalf("compensation job: %+v, err=%v", rollback, err)
	}
	var remaining int
	if err := target.QueryRowContext(ctx, "SELECT count(*) FROM "+schema+".items WHERE value <> 0").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining changed rows=%d, err=%v", remaining, err)
	}
	position, err := svc.GetPosition(ctx, 1)
	if err != nil || position.ActiveHead == nil || position.ActiveHead.ExecutionID != prior.ExecutionID {
		t.Fatalf("position after compensation: %+v, err=%v", position, err)
	}
	var execution models.MigrationExecution
	if err := db.First(&execution, rollback.ExecutionID).Error; err != nil || execution.RevertsExecutionID == nil || *execution.RevertsExecutionID != int(partial.ExecutionID) {
		t.Fatalf("compensation history: %+v, err=%v", execution, err)
	}
	if err := db.First(&sourceExecution, partial.ExecutionID).Error; err != nil || sourceExecution.Status != StatusFailed || !strings.Contains(sourceExecution.Error, "later batch failure") {
		t.Fatalf("original failure history: %+v, err=%v", sourceExecution, err)
	}
}

func TestPersistedReservationBlocksSameDatabase(t *testing.T) {
	svc, db, _, _ := newPostgresService(t)
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	firstID, err := svc.CreateDraft(ctx, 1, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := svc.CreateDraft(ctx, 1, "SELECT 2")
	if err != nil {
		t.Fatal(err)
	}
	attempt, _, err := svc.reserveApply(ctx, 1, firstID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewService(db, svc.dbMgmt)
	if err := restarted.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ExecuteMigration(ctx, 1, secondID, "bob"); !errors.Is(err, ErrDatabaseBusy) {
		t.Fatalf("running attempt did not block: %v", err)
	}
	if err := restarted.UpdateDraft(ctx, 1, firstID, "SELECT 3"); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("running attempt editable: %v", err)
	}
	if err := restarted.finishAttempt(ctx, &attempt, StatusUncertain, 0, ErrTargetCommitUncertain); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ExecuteMigration(ctx, 1, secondID, "bob"); !errors.Is(err, ErrDatabaseBusy) {
		t.Fatalf("uncertain attempt did not block: %v", err)
	}
	otherID, err := restarted.CreateDraft(ctx, 2, "SELECT 4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ExecuteMigration(ctx, 2, otherID, "carol"); err != nil {
		t.Fatalf("other database blocked: %v", err)
	}
	if err := restarted.finishAttempt(ctx, &attempt, StatusApplied, 1, nil); err == nil {
		t.Fatal("terminal attempt overwritten")
	}
}

func TestRollbackForwardWithPostgres(t *testing.T) {
	svc, _, db, schema := newPostgresService(t)
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	table := schema + ".reversible"
	a, err := svc.CreateDraftDefinition(ctx, 1, models.MigrationDraft{
		SQLUp: "CREATE TABLE " + table + " (id integer)", SQLDown: "DROP TABLE " + table,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateDraftDefinition(ctx, 1, models.MigrationDraft{
		SQLUp:   "ALTER TABLE " + table + " ADD COLUMN note text",
		SQLDown: "ALTER TABLE " + table + " DROP COLUMN note",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExecuteApprovedApply(ctx, 1, a, 2, "alice"); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale approval accepted: %v", err)
	}
	appliedA, err := svc.ExecuteApprovedApply(ctx, 1, a, 1, "alice")
	if err != nil {
		t.Fatal(err)
	}
	appliedB, err := svc.ExecuteApprovedApply(ctx, 1, b, 1, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExecuteReversal(ctx, 1, a, appliedA.ID, ActionRollback, "alice"); !errors.Is(err, ErrInvalidPosition) {
		t.Fatalf("rollback of non-head accepted: %v", err)
	}
	rolledBack, err := svc.ExecuteReversal(ctx, 1, b, appliedB.ID, ActionRollback, "alice")
	if err != nil {
		t.Fatal(err)
	}
	checkColumn := func(want bool) {
		t.Helper()
		var exists bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = 'reversible' AND column_name = 'note'
		)`, schema).Scan(&exists)
		if err != nil || exists != want {
			t.Fatalf("column exists=%t, want %t: %v", exists, want, err)
		}
	}
	checkColumn(false)
	forward, err := svc.ExecuteReversal(ctx, 1, b, rolledBack.ID, ActionForward, "alice")
	if err != nil {
		t.Fatal(err)
	}
	checkColumn(true)
	if _, err := svc.ExecuteReversal(ctx, 1, b, appliedB.ID, ActionRollback, "alice"); !errors.Is(err, ErrInvalidPosition) {
		t.Fatalf("stale rollback accepted: %v", err)
	}
	history, err := svc.GetLinearHistory(ctx, 1)
	if err != nil || len(history) != 4 {
		t.Fatalf("unexpected full history: %+v, %v", history, err)
	}
	filtered := HideRollbackPairs(history)
	if len(filtered) != 2 || filtered[0].ExecutionID != appliedA.ID || filtered[1].ExecutionID != forward.ID {
		t.Fatalf("unexpected filtered history: %+v", filtered)
	}
}
