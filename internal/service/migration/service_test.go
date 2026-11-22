package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"wbd/internal/models"
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

func definitionRow() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "target_db_id", "revision", "sql_up", "irreversible", "irreversible_reason"}).
		AddRow(7, 2, 3, "CREATE TABLE demo (id integer)", true, upOnlyReason)
}

func expectLock(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WithArgs(2).WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectReservation(mock sqlmock.Sqlmock) {
	expectLock(mock)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "migration_executions"`).
		WithArgs(2, StatusRunning, StatusUncertain).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT .*FROM "migrations"`).WithArgs(7, 2, 1).WillReturnRows(definitionRow())
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\)`).WithArgs(2, StatusApplied).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(3))
	mock.ExpectQuery(`INSERT INTO "migration_executions"`).
		WithArgs(7, 3, 2, ActionApply, "CREATE TABLE demo (id integer)", 0, "alice", StatusRunning,
			sqlmock.AnyArg(), nil, "", nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(19))
	mock.ExpectCommit()
}

func expectFinish(mock sqlmock.Sqlmock, status string, version int, errorPattern string) {
	mock.ExpectExec(`UPDATE "migration_executions"`).
		WithArgs(errorPattern, sqlmock.AnyArg(), status, version, 19, StatusRunning).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestCreateAndUpdateDraft(t *testing.T) {
	svc, mock := newMockService(t, nil)
	ctx := context.Background()
	if _, err := svc.CreateDraft(ctx, 2, "  "); !errors.Is(err, ErrInvalidMigration) {
		t.Fatalf("empty SQL: %v", err)
	}
	mock.ExpectQuery(`INSERT INTO "migrations"`).
		WithArgs(2, 1, "SELECT 1", "", true, upOnlyReason).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	id, err := svc.CreateDraft(ctx, 2, "SELECT 1")
	if err != nil || id != 7 {
		t.Fatalf("CreateDraft: id=%d err=%v", id, err)
	}
	for _, affected := range []int64{1, 0} {
		expectLock(mock)
		mock.ExpectExec(`UPDATE "migrations" SET .*NOT EXISTS.*migration_executions`).
			WithArgs("SELECT 2", 7, 2).WillReturnResult(sqlmock.NewResult(0, affected))
		if affected == 1 {
			mock.ExpectCommit()
		} else {
			mock.ExpectRollback()
		}
		err := svc.UpdateDraft(ctx, 2, 7, "SELECT 2")
		if (affected == 1 && err != nil) || (affected == 0 && !errors.Is(err, ErrDraftNotFound)) {
			t.Fatalf("update affected=%d: %v", affected, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetDraftsExcludesAnyAttempt(t *testing.T) {
	svc, mock := newMockService(t, nil)
	if _, err := svc.GetDrafts(context.Background(), 0); !errors.Is(err, ErrInvalidMigration) {
		t.Fatalf("invalid target: %v", err)
	}
	mock.ExpectQuery(`SELECT .*FROM "migrations" WHERE .*NOT EXISTS.*migration_executions`).
		WithArgs(2).WillReturnRows(definitionRow())
	drafts, err := svc.GetDrafts(context.Background(), 2)
	if err != nil || len(drafts) != 1 || drafts[0].SQLQuery != "CREATE TABLE demo (id integer)" || drafts[0].Status != StatusDraft {
		t.Fatalf("drafts=%+v err=%v", drafts, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMigrationOutcomes(t *testing.T) {
	for _, outcome := range []string{"success", "sql failure", "connection failure", "begin failure", "uncertain commit", "history failure"} {
		t.Run(outcome, func(t *testing.T) {
			target, targetMock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { target.Close() })
			svc, metaMock := newMockService(t, target)
			expectReservation(metaMock)
			cause := errors.New(outcome)
			switch outcome {
			case "connection failure":
				svc.dbMgmt = targetProvider{connErr: cause}
				expectFinish(metaMock, StatusFailed, 0, cause.Error())
			case "begin failure":
				targetMock.ExpectBegin().WillReturnError(cause)
				expectFinish(metaMock, StatusFailed, 0, cause.Error())
			default:
				targetMock.ExpectBegin()
				if outcome == "sql failure" {
					targetMock.ExpectExec(`CREATE TABLE demo`).WillReturnError(cause)
					targetMock.ExpectRollback()
					expectFinish(metaMock, StatusFailed, 0, cause.Error())
				} else {
					targetMock.ExpectExec(`CREATE TABLE demo`).WillReturnResult(sqlmock.NewResult(0, 0))
					if outcome == "uncertain commit" {
						targetMock.ExpectCommit().WillReturnError(cause)
						expectFinish(metaMock, StatusUncertain, 0, fmt.Sprintf("%s: %s", ErrTargetCommitUncertain, cause))
					} else {
						targetMock.ExpectCommit()
						if outcome == "history failure" {
							metaMock.ExpectExec(`UPDATE "migration_executions"`).WillReturnError(cause)
						} else {
							expectFinish(metaMock, StatusApplied, 4, "")
						}
					}
				}
			}
			record, err := svc.ExecuteMigration(context.Background(), 2, 7, "alice")
			switch outcome {
			case "success":
				if err != nil || record == nil || record.Status != StatusApplied || record.Version != 4 || record.ExecutionID != 19 || record.ExecutedAt.IsZero() {
					t.Fatalf("success: record=%+v err=%v", record, err)
				}
			case "uncertain commit":
				if record != nil || !errors.Is(err, ErrTargetCommitUncertain) {
					t.Fatalf("record=%+v err=%v", record, err)
				}
			case "history failure":
				if record != nil || !errors.Is(err, ErrHistoryUpdateUncertain) {
					t.Fatalf("record=%+v err=%v", record, err)
				}
			default:
				if err == nil || record == nil || record.Status != StatusFailed || record.Version != 0 || !strings.Contains(err.Error(), outcome) {
					t.Fatalf("failure: record=%+v err=%v", record, err)
				}
			}
			if err := metaMock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
			if err := targetMock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExecuteMigrationRejectsBusyOrMissingDraft(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(fmt.Sprintf("busy=%t", busy), func(t *testing.T) {
			svc, mock := newMockService(t, nil)
			expectLock(mock)
			count := 0
			if busy {
				count = 1
			}
			mock.ExpectQuery(`SELECT count\(\*\) FROM "migration_executions"`).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
			if !busy {
				mock.ExpectQuery(`SELECT .*FROM "migrations"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			}
			mock.ExpectRollback()
			record, err := svc.ExecuteMigration(context.Background(), 2, 7, "alice")
			want := ErrDraftNotFound
			if busy {
				want = ErrDatabaseBusy
			}
			if record != nil || !errors.Is(err, want) {
				t.Fatalf("record=%+v err=%v", record, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHistoryUsesExecutionSnapshots(t *testing.T) {
	svc, mock := newMockService(t, nil)
	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT .*FROM "migration_executions" WHERE target_db_id`).WithArgs(2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "migration_id", "target_db_id", "sql_snapshot", "status", "started_at"}).
			AddRow(19, 7, 2, "original SQL", StatusFailed, now).
			AddRow(20, 7, 2, "original SQL", StatusApplied, now))
	history, err := svc.GetLinearHistory(context.Background(), 2)
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if history[0].ExecutionID != 19 || history[1].ExecutionID != 20 || history[0].ID != history[1].ID || history[0].SQLQuery != "original SQL" {
		t.Fatalf("attempts were merged or snapshot lost: %+v", history)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateDraftReversibility(t *testing.T) {
	for _, tc := range []struct {
		name         string
		down         string
		irreversible bool
		reason       string
		valid        bool
	}{
		{"reversible", "DROP TABLE demo", false, "", true},
		{"irreversible", "", true, "Deleted rows have no backup", true},
		{"missing down", " ", false, "", false},
		{"missing reason", "", true, " ", false},
		{"conflicting down", "DROP TABLE demo", true, "No backup", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDraft(models.MigrationDraft{
				SQLUp: "CREATE TABLE demo (id integer)", SQLDown: tc.down,
				Irreversible: tc.irreversible, IrreversibleReason: tc.reason,
			})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, got %v", tc.valid, err)
			}
		})
	}
}

func TestExecuteApprovedApplyRejectsStaleRevision(t *testing.T) {
	svc, mock := newMockService(t, nil)
	expectLock(mock)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "migration_executions"`).
		WithArgs(2, StatusRunning, StatusUncertain).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT .*FROM "migrations"`).WithArgs(7, 2, 1).WillReturnRows(definitionRow())
	mock.ExpectRollback()

	if _, err := svc.ExecuteApprovedApply(context.Background(), 2, 7, 2, "alice"); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("expected stale revision error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
