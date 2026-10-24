package migration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"wbd/internal/service/interfaces"
)

type targetProvider struct {
	interfaces.DBManagementService
	conn    *sql.DB
	connErr error
}

func (p targetProvider) GetTargetConnection(context.Context, int) (*sql.DB, error) {
	return p.conn, p.connErr
}

func newMockService(t *testing.T, target *sql.DB) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	internal, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { internal.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: internal}), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	return NewService(db, targetProvider{conn: target}), mock
}

func draftRow() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "target_db_id", "version", "sql_query", "executed_by", "status", "executed_at"}).
		AddRow(7, 2, 0, "CREATE TABLE demo (id integer)", "", StatusDraft, time.Time{})
}

func TestCreateAndUpdateDraft(t *testing.T) {
	svc, mock := newMockService(t, nil)
	ctx := context.Background()
	if _, err := svc.CreateDraft(ctx, 2, "  "); !errors.Is(err, ErrInvalidMigration) {
		t.Fatalf("empty SQL: expected ErrInvalidMigration, got %v", err)
	}

	mock.ExpectQuery(`INSERT INTO "migration_records"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	id, err := svc.CreateDraft(ctx, 2, "CREATE TABLE demo (id integer)")
	if err != nil || id != 7 {
		t.Fatalf("CreateDraft: id=%d err=%v", id, err)
	}

	mock.ExpectExec(`UPDATE "migration_records"`).WillReturnResult(sqlmock.NewResult(0, 0))
	if err := svc.UpdateDraft(ctx, 2, 7, "ALTER TABLE demo ADD name text"); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("draft that cannot be edited: expected ErrDraftNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMigrationCommitsAndAssignsVersion(t *testing.T) {
	target, targetMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	svc, metaMock := newMockService(t, target)
	metaMock.ExpectBegin()
	metaMock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	metaMock.ExpectQuery(`SELECT .*FROM "migration_records"`).WillReturnRows(draftRow())
	metaMock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\)`).WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(3))
	targetMock.ExpectBegin()
	targetMock.ExpectExec(`CREATE TABLE demo`).WillReturnResult(sqlmock.NewResult(0, 0))
	targetMock.ExpectCommit()
	metaMock.ExpectExec(`UPDATE "migration_records"`).WillReturnResult(sqlmock.NewResult(0, 1))
	metaMock.ExpectCommit()

	record, err := svc.ExecuteMigration(context.Background(), 2, 7, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != StatusApplied || record.Version != 4 || record.ExecutedBy != "alice" || record.ExecutedAt.IsZero() {
		t.Fatalf("unexpected applied record: %+v", record)
	}
	if err := metaMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := targetMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMigrationReportsUncertainHistoryAfterTargetCommit(t *testing.T) {
	target, targetMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	svc, metaMock := newMockService(t, target)
	metaMock.ExpectBegin()
	metaMock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	metaMock.ExpectQuery(`SELECT .*FROM "migration_records"`).WillReturnRows(draftRow())
	metaMock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\)`).WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0))
	targetMock.ExpectBegin()
	targetMock.ExpectExec(`CREATE TABLE demo`).WillReturnResult(sqlmock.NewResult(0, 0))
	targetMock.ExpectCommit()
	metaMock.ExpectExec(`UPDATE "migration_records"`).WillReturnError(errors.New("history write failed"))
	metaMock.ExpectRollback()

	record, err := svc.ExecuteMigration(context.Background(), 2, 7, "alice")
	if record != nil || !errors.Is(err, ErrHistoryUpdateUncertain) {
		t.Fatalf("expected uncertain history after target commit, got record=%+v err=%v", record, err)
	}
	if err := metaMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := targetMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMigrationDoesNotRecordFailedOnUncertainTargetCommit(t *testing.T) {
	target, targetMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	svc, metaMock := newMockService(t, target)
	metaMock.ExpectBegin()
	metaMock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	metaMock.ExpectQuery(`SELECT .*FROM "migration_records"`).WillReturnRows(draftRow())
	metaMock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\)`).WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0))
	targetMock.ExpectBegin()
	targetMock.ExpectExec(`CREATE TABLE demo`).WillReturnResult(sqlmock.NewResult(0, 0))
	targetMock.ExpectCommit().WillReturnError(errors.New("commit response lost"))
	metaMock.ExpectRollback()

	record, err := svc.ExecuteMigration(context.Background(), 2, 7, "alice")
	if record != nil || !errors.Is(err, ErrTargetCommitUncertain) {
		t.Fatalf("expected uncertain target commit, got record=%+v err=%v", record, err)
	}
	if err := metaMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := targetMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMigrationRollsBackAndRecordsFailure(t *testing.T) {
	target, targetMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	svc, metaMock := newMockService(t, target)
	metaMock.ExpectBegin()
	metaMock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	metaMock.ExpectQuery(`SELECT .*FROM "migration_records"`).WillReturnRows(draftRow())
	metaMock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\)`).WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(3))
	targetMock.ExpectBegin()
	targetMock.ExpectExec(`CREATE TABLE demo`).WillReturnError(errors.New("SQL failed"))
	targetMock.ExpectRollback()
	metaMock.ExpectExec(`UPDATE "migration_records"`).WillReturnResult(sqlmock.NewResult(0, 1))
	metaMock.ExpectCommit()

	record, err := svc.ExecuteMigration(context.Background(), 2, 7, "alice")
	if err == nil || record == nil {
		t.Fatalf("expected execution error and recorded result, got record=%+v err=%v", record, err)
	}
	if record.Status != StatusFailed || record.Version != 0 || record.ExecutedBy != "alice" {
		t.Fatalf("unexpected failed record: %+v", record)
	}
	if err := metaMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := targetMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMigrationRejectsMissingDraft(t *testing.T) {
	svc, metaMock := newMockService(t, nil)
	metaMock.ExpectBegin()
	metaMock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	metaMock.ExpectQuery(`SELECT .*FROM "migration_records"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	metaMock.ExpectRollback()

	record, err := svc.ExecuteMigration(context.Background(), 2, 7, "alice")
	if record != nil || !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("expected missing draft, got record=%+v err=%v", record, err)
	}
	if err := metaMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMigrationRecordsConnectionFailure(t *testing.T) {
	svc, metaMock := newMockService(t, nil)
	svc.dbMgmt = targetProvider{connErr: errors.New("connection refused")}
	metaMock.ExpectBegin()
	metaMock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	metaMock.ExpectQuery(`SELECT .*FROM "migration_records"`).WillReturnRows(draftRow())
	metaMock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\)`).WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(1))
	metaMock.ExpectExec(`UPDATE "migration_records"`).WillReturnResult(sqlmock.NewResult(0, 1))
	metaMock.ExpectCommit()

	record, err := svc.ExecuteMigration(context.Background(), 2, 7, "alice")
	if err == nil || !strings.Contains(err.Error(), "connection refused") || record == nil || record.Status != StatusFailed || record.Version != 0 {
		t.Fatalf("connection failure was not recorded: record=%+v err=%v", record, err)
	}
	if err := metaMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMigrationRecordsBeginFailure(t *testing.T) {
	target, targetMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	svc, metaMock := newMockService(t, target)
	metaMock.ExpectBegin()
	metaMock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	metaMock.ExpectQuery(`SELECT .*FROM "migration_records"`).WillReturnRows(draftRow())
	metaMock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\)`).WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(1))
	targetMock.ExpectBegin().WillReturnError(errors.New("cannot begin"))
	metaMock.ExpectExec(`UPDATE "migration_records"`).WillReturnResult(sqlmock.NewResult(0, 1))
	metaMock.ExpectCommit()

	record, err := svc.ExecuteMigration(context.Background(), 2, 7, "alice")
	if err == nil || !strings.Contains(err.Error(), "cannot begin") || record == nil || record.Status != StatusFailed {
		t.Fatalf("begin failure was not recorded: record=%+v err=%v", record, err)
	}
	if err := metaMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := targetMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
