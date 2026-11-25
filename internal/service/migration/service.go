package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm/clause"

	"gorm.io/gorm"

	"wbd/internal/models"
	"wbd/internal/service/interfaces"
)

const (
	StatusDraft           = "DRAFT"
	StatusApplied         = "APPLIED"
	StatusFailed          = "FAILED"
	StatusRunning         = "RUNNING"
	StatusUncertain       = "UNCERTAIN"
	StatusPartial         = "PARTIAL"
	StatusPartialReverted = "PARTIALLY_REVERTED"
	StatusCompensated     = "COMPENSATED"
	ActionApply           = "APPLY"
	upOnlyReason          = "SQL down was not supplied by the M1 SQL-up-only interface."
)

var (
	ErrStaleRevision          = errors.New("migration draft no longer matches the approved revision")
	ErrInvalidMigration       = errors.New("invalid migration")
	ErrDraftNotFound          = errors.New("migration draft not found")
	ErrDatabaseBusy           = errors.New("database has a running or uncertain migration")
	ErrTargetCommitUncertain  = errors.New("target commit could not be confirmed; inspect the target database before retrying")
	ErrHistoryUpdateUncertain = errors.New("target migration committed but history update could not be confirmed; inspect the target database before retrying")
)

var batchIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const batchPredicate = "{{batch_predicate}}"

func validateBatchStatement(job models.MigrationJob, query string) error {
	parts := strings.Split(job.BatchTable, ".")
	if len(parts) > 2 || len(parts) == 0 || strings.TrimSpace(job.BatchTable) == "" || !batchIdentifier.MatchString(job.BatchAttribute) {
		return ErrInvalidJob
	}
	for _, part := range parts {
		if !batchIdentifier.MatchString(part) {
			return ErrInvalidJob
		}
	}
	if strings.Count(query, batchPredicate) != 1 || strings.Count(query, ";") > 1 || (strings.Contains(query, ";") && !strings.HasSuffix(strings.TrimSpace(query), ";")) {
		return ErrInvalidJob
	}
	return nil
}

func batchTableSQL(job models.MigrationJob) string {
	parts := strings.Split(job.BatchTable, ".")
	for i := range parts {
		parts[i] = `"` + parts[i] + `"`
	}
	return strings.Join(parts, ".")
}

func batchAttributeSQL(job models.MigrationJob) string { return `"` + job.BatchAttribute + `"` }

type Service struct {
	internalDB   *gorm.DB
	dbMgmt       interfaces.DBManagementService
	jobValidator JobValidator
}

func NewService(internalDB *gorm.DB, dbMgmt interfaces.DBManagementService) *Service {
	return &Service{
		internalDB: internalDB,
		dbMgmt:     dbMgmt,
	}
}

func (s *Service) SetJobValidator(validate JobValidator) { s.jobValidator = validate }

func lockDatabase(db *gorm.DB, targetDBID int) error {
	return db.Exec("SELECT pg_advisory_xact_lock(3110, ?)", targetDBID).Error
}

func (s *Service) CreateDraft(ctx context.Context, targetDBID int, sqlQuery string) (int, error) {
	return s.CreateDraftDefinition(ctx, targetDBID, models.MigrationDraft{
		SQLUp: sqlQuery, Irreversible: true, IrreversibleReason: upOnlyReason,
	})
}

func validateDraft(draft models.MigrationDraft) error {
	hasDown := strings.TrimSpace(draft.SQLDown) != ""
	hasReason := strings.TrimSpace(draft.IrreversibleReason) != ""
	if strings.TrimSpace(draft.SQLUp) == "" ||
		(draft.Irreversible && (hasDown || !hasReason)) ||
		(!draft.Irreversible && (!hasDown || hasReason)) {
		return ErrInvalidMigration
	}
	return nil
}

func (s *Service) CreateDraftDefinition(ctx context.Context, targetDBID int, draft models.MigrationDraft) (int, error) {
	if targetDBID <= 0 || validateDraft(draft) != nil {
		return 0, ErrInvalidMigration
	}
	definition := models.Migration{
		TargetDBID: targetDBID, Revision: 1, SQLUp: draft.SQLUp, SQLDown: draft.SQLDown,
		Irreversible: draft.Irreversible, IrreversibleReason: strings.TrimSpace(draft.IrreversibleReason),
	}
	if err := s.internalDB.WithContext(ctx).Create(&definition).Error; err != nil {
		return 0, err
	}
	return definition.ID, nil
}

func unexecuted(db *gorm.DB) *gorm.DB {
	return db.Where(`NOT EXISTS (SELECT 1 FROM migration_executions e WHERE e.migration_id = migrations.id)`)
}

func (s *Service) UpdateDraft(ctx context.Context, targetDBID, migrationID int, sqlQuery string) error {
	if targetDBID <= 0 || migrationID <= 0 || strings.TrimSpace(sqlQuery) == "" {
		return ErrInvalidMigration
	}
	return s.updateDraft(ctx, targetDBID, migrationID, map[string]any{"sql_up": sqlQuery}, true)
}

func (s *Service) UpdateDraftDefinition(ctx context.Context, targetDBID, migrationID int, draft models.MigrationDraft) error {
	if targetDBID <= 0 || migrationID <= 0 || validateDraft(draft) != nil {
		return ErrInvalidMigration
	}
	return s.updateDraft(ctx, targetDBID, migrationID, map[string]any{
		"sql_up": draft.SQLUp, "sql_down": draft.SQLDown, "irreversible": draft.Irreversible,
		"irreversible_reason": strings.TrimSpace(draft.IrreversibleReason),
	}, false)
}

func (s *Service) updateDraft(ctx context.Context, targetDBID, migrationID int, fields map[string]any, legacy bool) error {
	fields["revision"] = gorm.Expr("revision + 1")
	return s.internalDB.WithContext(ctx).Transaction(func(meta *gorm.DB) error {
		if err := lockDatabase(meta, targetDBID); err != nil {
			return err
		}
		query := unexecuted(meta.Model(&models.Migration{})).
			Where("id = ? AND target_db_id = ?", migrationID, targetDBID)
		if legacy {
			query = query.Where("irreversible AND COALESCE(sql_down, '') = ''")
		}
		result := query.Updates(fields)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrDraftNotFound
		}
		return nil
	})
}

func draftView(definition models.Migration) models.MigrationRecord {
	return models.MigrationRecord{ID: definition.ID, TargetDBID: definition.TargetDBID,
		SQLQuery: definition.SQLUp, Status: StatusDraft, Revision: definition.Revision}
}

func executionView(execution models.MigrationExecution) models.MigrationRecord {
	return models.MigrationRecord{ID: execution.MigrationID, ExecutionID: execution.ID,
		Action: execution.Action, RevertsExecutionID: execution.RevertsExecutionID,
		TargetDBID: execution.TargetDBID, Version: execution.Version,
		SQLQuery: execution.SQLSnapshot, Status: execution.Status,
		ExecutedBy: execution.ExecutedBy, ExecutedAt: execution.StartedAt,
		Revision: execution.MigrationRevision, FinishedAt: execution.FinishedAt}
}

func (s *Service) GetDefinition(ctx context.Context, targetDBID, migrationID int) (models.Migration, error) {
	var definition models.Migration
	if targetDBID <= 0 || migrationID <= 0 {
		return definition, ErrInvalidMigration
	}
	err := s.internalDB.WithContext(ctx).
		Where("id = ? AND target_db_id = ?", migrationID, targetDBID).First(&definition).Error
	return definition, err
}

func (s *Service) GetMigrationDetail(ctx context.Context, targetDBID, migrationID int) (*models.MigrationRecord, error) {
	definition, err := s.GetDefinition(ctx, targetDBID, migrationID)
	if err != nil {
		return nil, err
	}
	var execution models.MigrationExecution
	err = s.internalDB.WithContext(ctx).
		Where("migration_id = ? AND target_db_id = ?", migrationID, targetDBID).
		Order("id DESC").Take(&execution).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		view := draftView(definition)
		return &view, nil
	}
	if err != nil {
		return nil, err
	}
	view := executionView(execution)
	return &view, nil
}

func (s *Service) GetDrafts(ctx context.Context, targetDBID int) ([]models.MigrationRecord, error) {
	if targetDBID <= 0 {
		return nil, ErrInvalidMigration
	}
	var definitions []models.Migration
	if err := unexecuted(s.internalDB.WithContext(ctx)).
		Where("target_db_id = ?", targetDBID).Order("id ASC").Find(&definitions).Error; err != nil {
		return nil, err
	}
	views := make([]models.MigrationRecord, 0, len(definitions))
	for _, definition := range definitions {
		views = append(views, draftView(definition))
	}
	return views, nil
}

func (s *Service) GetExecutionHistory(ctx context.Context, targetDBID int) ([]models.MigrationExecution, error) {
	if targetDBID <= 0 {
		return nil, ErrInvalidMigration
	}
	var executions []models.MigrationExecution
	err := s.internalDB.WithContext(ctx).Where("target_db_id = ?", targetDBID).
		Order("started_at ASC, legacy_migration_id ASC NULLS LAST, id ASC").Find(&executions).Error
	return executions, err
}

func (s *Service) GetLinearHistory(ctx context.Context, targetDBID int) ([]models.MigrationRecord, error) {
	executions, err := s.GetExecutionHistory(ctx, targetDBID)
	if err != nil {
		return nil, err
	}
	views := make([]models.MigrationRecord, 0, len(executions))
	for _, execution := range executions {
		views = append(views, executionView(execution))
	}
	return views, nil
}

func (s *Service) ExplainMigration(ctx context.Context, targetDBID, migrationID int) (string, error) {
	if targetDBID <= 0 || migrationID <= 0 {
		return "", ErrInvalidMigration
	}

	var record models.Migration
	err := unexecuted(s.internalDB.WithContext(ctx)).
		Where("id = ? AND target_db_id = ?", migrationID, targetDBID).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrDraftNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find migration draft for explain: %w", err)
	}

	conn, err := s.dbMgmt.GetTargetConnection(ctx, targetDBID)
	if err != nil {
		return "", fmt.Errorf("connect to target database for explain: %w", err)
	}
	if conn == nil {
		return "", errors.New("target database connection is nil")
	}
	stmt, err := conn.PrepareContext(ctx, "EXPLAIN "+record.SQLUp)
	if err != nil {
		return "", fmt.Errorf("prepare migration query plan: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.QueryContext(ctx)
	if err != nil {
		return "", fmt.Errorf("query migration plan: %w", err)
	}
	defer rows.Close()

	var planLines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", fmt.Errorf("read migration query plan: %w", err)
		}
		planLines = append(planLines, line)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("read migration query plan: %w", err)
	}
	return strings.Join(planLines, "\n"), nil
}

func (s *Service) reserveApply(ctx context.Context, targetDBID, migrationID int, executedBy string) (models.MigrationExecution, int, error) {
	return s.reserveApplyRevision(ctx, targetDBID, migrationID, 0, executedBy)
}

func (s *Service) ExecuteApprovedApply(ctx context.Context, targetDBID, migrationID, revision int, actor string) (*models.MigrationExecution, error) {
	return s.executeApprovedApply(ctx, 0, targetDBID, migrationID, revision, actor)
}

func (s *Service) executeApprovedApply(ctx context.Context, jobID, targetDBID, migrationID, revision int, actor string) (*models.MigrationExecution, error) {
	if targetDBID <= 0 || migrationID <= 0 || revision <= 0 || strings.TrimSpace(actor) == "" {
		return nil, ErrInvalidMigration
	}
	execution, version, err := s.reserveApplyRevisionForJob(ctx, jobID, targetDBID, migrationID, revision, actor)
	if err != nil {
		return nil, err
	}
	return s.runAttemptForJob(ctx, execution, version, jobID)
}

func (s *Service) ExecuteAsyncJob(ctx context.Context, job models.MigrationJob) (*models.MigrationExecution, error) {
	if job.Action == ActionApply {
		return s.executeApprovedApply(ctx, job.ID, job.TargetDBID, job.MigrationID, job.Revision, job.Actor)
	}
	return s.executeReversal(ctx, job.ID, job.TargetDBID, job.MigrationID, job.ExpectedExecutionID, job.Action, job.Actor)
}

func (s *Service) reserveApplyRevision(ctx context.Context, targetDBID, migrationID, revision int, executedBy string) (models.MigrationExecution, int, error) {
	return s.reserveApplyRevisionForJob(ctx, 0, targetDBID, migrationID, revision, executedBy)
}

func (s *Service) reserveApplyRevisionForJob(ctx context.Context, jobID, targetDBID, migrationID, revision int, executedBy string) (models.MigrationExecution, int, error) {
	return s.reserveAttemptForJob(ctx, targetDBID, jobID, func(meta *gorm.DB) (models.MigrationExecution, int, error) {
		var definition models.Migration
		query := meta.Where("id = ? AND target_db_id = ?", migrationID, targetDBID)
		if jobID == 0 {
			query = unexecuted(query)
		} else {
			var applied int64
			if err := meta.Model(&models.MigrationExecution{}).Where("migration_id = ? AND target_db_id = ? AND status = ?", migrationID, targetDBID, StatusApplied).Count(&applied).Error; err != nil {
				return models.MigrationExecution{}, 0, err
			}
			if applied > 0 {
				return models.MigrationExecution{}, 0, ErrDraftNotFound
			}
		}
		if err := query.First(&definition).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return models.MigrationExecution{}, 0, ErrDraftNotFound
			}
			return models.MigrationExecution{}, 0, err
		}
		if revision != 0 && revision != definition.Revision {
			return models.MigrationExecution{}, 0, ErrStaleRevision
		}
		var lastVersion int
		if err := meta.Model(&models.MigrationExecution{}).
			Where("target_db_id = ? AND status = ?", targetDBID, StatusApplied).
			Select("COALESCE(MAX(version), 0)").Scan(&lastVersion).Error; err != nil {
			return models.MigrationExecution{}, 0, err
		}
		execution := models.MigrationExecution{
			MigrationID: definition.ID, MigrationRevision: definition.Revision,
			TargetDBID: targetDBID, Action: ActionApply, SQLSnapshot: definition.SQLUp,
			ExecutedBy: executedBy, Status: StatusRunning, StartedAt: time.Now().UTC(),
		}
		return execution, lastVersion + 1, nil
	})
}

func (s *Service) reserveAttempt(ctx context.Context, targetDBID int, build func(*gorm.DB) (models.MigrationExecution, int, error)) (models.MigrationExecution, int, error) {
	return s.reserveAttemptForJob(ctx, targetDBID, 0, build)
}

func (s *Service) reserveAttemptForJob(ctx context.Context, targetDBID, jobID int, build func(*gorm.DB) (models.MigrationExecution, int, error)) (models.MigrationExecution, int, error) {
	var execution models.MigrationExecution
	var nextVersion int
	err := s.internalDB.WithContext(ctx).Transaction(func(meta *gorm.DB) error {
		if err := lockDatabase(meta, targetDBID); err != nil {
			return err
		}
		var active int64
		inflight := meta.Model(&models.MigrationExecution{}).Where("target_db_id = ? AND status IN ?", targetDBID, []string{StatusRunning, StatusUncertain, StatusPartial, StatusPartialReverted})
		if jobID > 0 {
			var job models.MigrationJob
			if err := meta.First(&job, jobID).Error; err != nil {
				return err
			}
			if job.Action == ActionRollback && job.Mode == "BATCH" {
				inflight = inflight.Where("id <> ?", job.ExpectedExecutionID)
			}
		}
		if err := inflight.Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return ErrDatabaseBusy
		}
		var err error
		execution, nextVersion, err = build(meta)
		if err != nil {
			return err
		}
		if err := meta.Create(&execution).Error; err != nil {
			return err
		}
		if jobID > 0 {
			result := meta.Model(&models.MigrationJob{}).Where("id = ? AND status = ?", jobID, StatusRunning).Updates(map[string]any{"execution_id": execution.ID, "planned_version": nextVersion})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrJobConflict
			}
		}
		return nil
	})
	return execution, nextVersion, err
}

func (s *Service) finishAttempt(ctx context.Context, execution *models.MigrationExecution, status string, version int, cause error) error {
	finishedAt := time.Now().UTC()
	errorText := ""
	if cause != nil {
		errorText = cause.Error()
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result := s.internalDB.WithContext(writeCtx).Model(&models.MigrationExecution{}).
		Where("id = ? AND status = ?", execution.ID, StatusRunning).
		Updates(map[string]any{"status": status, "version": version, "finished_at": finishedAt, "error": errorText})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("execution is no longer running")
	}
	execution.Status, execution.Version = status, version
	execution.FinishedAt, execution.Error = &finishedAt, errorText
	return nil
}

func (s *Service) ExecuteMigration(ctx context.Context, targetDBID, migrationID int, executedBy string) (*models.MigrationRecord, error) {
	if targetDBID <= 0 || migrationID <= 0 || strings.TrimSpace(executedBy) == "" {
		return nil, ErrInvalidMigration
	}
	execution, nextVersion, err := s.reserveApply(ctx, targetDBID, migrationID, executedBy)
	if err != nil {
		return nil, err
	}
	result, err := s.runAttempt(ctx, execution, nextVersion)
	if result == nil {
		return nil, err
	}
	view := executionView(*result)
	return &view, err
}

func (s *Service) runAttempt(ctx context.Context, execution models.MigrationExecution, version int) (*models.MigrationExecution, error) {
	return s.runAttemptForJob(ctx, execution, version, 0)
}

func (s *Service) runAttemptForJob(ctx context.Context, execution models.MigrationExecution, version, jobID int) (*models.MigrationExecution, error) {
	fail := func(cause error) (*models.MigrationExecution, error) {
		if err := s.finishAttempt(ctx, &execution, StatusFailed, 0, cause); err != nil {
			return nil, errors.Join(cause, fmt.Errorf("record failed execution: %w", err))
		}
		return &execution, cause
	}
	conn, err := s.dbMgmt.GetTargetConnection(ctx, execution.TargetDBID)
	if err != nil {
		return fail(err)
	}
	if conn == nil {
		return fail(errors.New("target connection is nil"))
	}
	txStarted := time.Now()
	slog.InfoContext(ctx, "migration transaction begin started", "event", "migration.transaction.begin.start", "job_id", jobID, "execution_id", execution.ID, "migration_id", execution.MigrationID, "database", execution.TargetDBID, "actor", execution.ExecutedBy)
	targetTx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fail(err)
	}
	slog.InfoContext(ctx, "migration transaction begin finished", "event", "migration.transaction.begin.result", "job_id", jobID, "execution_id", execution.ID, "migration_id", execution.MigrationID, "database", execution.TargetDBID, "duration_ms", time.Since(txStarted).Milliseconds())
	started := time.Now()
	queryCtx, querySpan := otel.Tracer("wbd/async").Start(ctx, "database.query")
	attrs := traceLogAttrs(queryCtx)
	attrs = append(attrs, "event", "migration.sql.start", "job_id", jobID, "execution_id", execution.ID, "migration_id", execution.MigrationID, "database", execution.TargetDBID, "actor", execution.ExecutedBy, "query", redactSQL(execution.SQLSnapshot))
	slog.InfoContext(queryCtx, "migration SQL started", attrs...)
	result, execErr := targetTx.ExecContext(queryCtx, execution.SQLSnapshot)
	rowsAffected := int64(0)
	if execErr == nil {
		rowsAffected, _ = result.RowsAffected()
	}
	attrs = traceLogAttrs(queryCtx)
	attrs = append(attrs, "event", "migration.sql.result", "job_id", jobID, "execution_id", execution.ID, "migration_id", execution.MigrationID, "database", execution.TargetDBID, "actor", execution.ExecutedBy, "duration_ms", time.Since(started).Milliseconds(), "rows", rowsAffected, "error", redactSQL(errorString(execErr)), "sqlstate", sqlState(execErr), "parameters", []string{}, "query", redactSQL(execution.SQLSnapshot))
	slog.InfoContext(queryCtx, "migration SQL finished", attrs...)
	if execErr != nil {
		querySpan.RecordError(execErr)
		querySpan.SetStatus(codes.Error, "migration SQL failed")
	}
	querySpan.End()
	if execErr != nil {
		rollbackStarted := time.Now()
		slog.InfoContext(ctx, "migration transaction rollback started", "event", "migration.transaction.rollback.start", "job_id", jobID, "execution_id", execution.ID, "migration_id", execution.MigrationID, "database", execution.TargetDBID)
		if rollbackErr := targetTx.Rollback(); rollbackErr != nil {
			execErr = errors.Join(execErr, fmt.Errorf("rollback target migration: %w", rollbackErr))
		} else if jobID > 0 && retryableDatabaseError(execErr) {
			if err := s.finishAttempt(ctx, &execution, StatusFailed, 0, execErr); err != nil {
				return nil, errors.Join(execErr, err)
			}
			return &execution, ErrRetrySafe
		}
		slog.InfoContext(ctx, "migration transaction rollback finished", "event", "migration.transaction.rollback.result", "job_id", jobID, "execution_id", execution.ID, "migration_id", execution.MigrationID, "database", execution.TargetDBID, "duration_ms", time.Since(rollbackStarted).Milliseconds(), "error", redactSQL(errorString(execErr)))
		return fail(execErr)
	}
	if jobID > 0 {
		if _, err := targetTx.ExecContext(ctx, `INSERT INTO wbd_migration_job_commits(job_id, execution_id, status, committed_at) VALUES ($1,$2,'COMMITTED',now())`, jobID, execution.ID); err != nil {
			_ = targetTx.Rollback()
			return fail(err)
		}
	}
	commitStarted := time.Now()
	slog.InfoContext(ctx, "migration transaction commit started", "event", "migration.transaction.commit.start", "job_id", jobID, "execution_id", execution.ID, "migration_id", execution.MigrationID, "database", execution.TargetDBID)
	if err := targetTx.Commit(); err != nil {
		if jobID > 0 {
			if conn, connErr := s.dbMgmt.GetTargetConnection(context.WithoutCancel(ctx), execution.TargetDBID); connErr == nil {
				if committed, verifyErr := commitEvidence(context.WithoutCancel(ctx), conn, jobID, execution.ID); verifyErr == nil {
					if committed {
						if finishErr := s.finishAttempt(ctx, &execution, StatusApplied, version, nil); finishErr == nil {
							return &execution, nil
						}
					} else {
						if finishErr := s.finishAttempt(ctx, &execution, StatusFailed, 0, err); finishErr != nil {
							return nil, errors.Join(err, finishErr)
						}
						return &execution, ErrRetrySafe
					}
				}
			}
		}
		slog.InfoContext(ctx, "migration transaction commit failed", "event", "migration.transaction.commit.result", "job_id", jobID, "execution_id", execution.ID, "migration_id", execution.MigrationID, "database", execution.TargetDBID, "duration_ms", time.Since(commitStarted).Milliseconds(), "error", redactSQL(err.Error()))
		uncertainErr := fmt.Errorf("%w: %v", ErrTargetCommitUncertain, err)
		if saveErr := s.finishAttempt(ctx, &execution, StatusUncertain, 0, uncertainErr); saveErr != nil {
			return nil, errors.Join(uncertainErr, saveErr)
		}
		return nil, uncertainErr
	}
	slog.InfoContext(ctx, "migration transaction committed", "event", "migration.transaction.commit.result", "job_id", jobID, "execution_id", execution.ID, "migration_id", execution.MigrationID, "database", execution.TargetDBID, "duration_ms", time.Since(commitStarted).Milliseconds(), "status", StatusApplied)
	if err := s.finishAttempt(ctx, &execution, StatusApplied, version, nil); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrHistoryUpdateUncertain, err)
	}
	return &execution, nil
}

var ErrIntegrationPending = errors.New("migration integration pending")
var ErrRetrySafe = errors.New("executor confirmed retry is safe")
var ErrJobConflict = errors.New("migration already has a pending job")
var ErrInvalidJob = errors.New("invalid migration job")

type JobResult struct {
	Status           string
	ExecutionID      int64
	CommittedBatches int64
	CommittedRows    int64
	TotalRows        int64
	Cursor           int64
	Error            string
}

type JobExecutor func(context.Context, models.MigrationJob) (JobResult, error)
type JobValidator func(context.Context, models.MigrationJob) error

func jobEvent(ctx context.Context, event string, job models.MigrationJob) {
	sc := trace.SpanContextFromContext(ctx)
	slog.Info(event, "event", event, "job_id", job.ID, "migration_id", job.MigrationID, "database", job.TargetDBID, "actor", job.Actor, "status", job.Status, "attempt", job.Attempts, "trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
}

func traceLogAttrs(ctx context.Context) []any {
	sc := trace.SpanContextFromContext(ctx)
	return []any{"trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String()}
}

func (s *Service) SubmitJob(ctx context.Context, job models.MigrationJob) (int, error) {
	if job.TargetDBID <= 0 || job.MigrationID <= 0 || strings.TrimSpace(job.Actor) == "" || (job.Action != "APPLY" && job.Action != "ROLLBACK" && job.Action != "FORWARD") || (job.Mode != "STANDARD" && job.Mode != "BATCH") || (job.Mode == "BATCH" && (job.BatchSize <= 0 || job.BatchSize > 10000)) || (job.Mode == "STANDARD" && job.BatchSize != 0) {
		return 0, ErrInvalidJob
	}
	if (job.Action == ActionApply && (job.Revision <= 0 || job.ExpectedExecutionID != 0)) || (job.Action != ActionApply && (job.ExpectedExecutionID <= 0 || job.Revision != 0)) {
		return 0, ErrInvalidJob
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	job.ID, job.Status, job.TraceParent = 0, "QUEUED", carrier.Get("traceparent")
	err := s.internalDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(3112, ?)", job.MigrationID).Error; err != nil {
			return err
		}
		var record models.Migration
		if err := tx.Where("id = ? AND target_db_id = ?", job.MigrationID, job.TargetDBID).First(&record).Error; err != nil {
			return err
		}
		query := record.SQLUp
		if job.Action != ActionApply {
			var execution models.MigrationExecution
			if err := tx.Where("id = ? AND migration_id = ? AND target_db_id = ?", job.ExpectedExecutionID, job.MigrationID, job.TargetDBID).First(&execution).Error; err != nil {
				return err
			}
			if job.Action == ActionRollback {
				query = record.SQLDown
			}
		}
		if job.Mode == "BATCH" {
			if err := validateBatchStatement(job, query); err != nil {
				return err
			}
		} else if job.BatchTable != "" || job.BatchAttribute != "" {
			return ErrInvalidJob
		}
		var count int64
		if err := tx.Model(&models.MigrationJob{}).Where("migration_id = ? AND status IN ?", job.MigrationID, []string{"QUEUED", "RUNNING"}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrJobConflict
		}
		return tx.Create(&job).Error
	})
	if err != nil {
		return 0, err
	}
	jobEvent(ctx, "job.queued", job)
	return job.ID, nil
}

func retryableDatabaseError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func (s *Service) FindJob(ctx context.Context, dbID, migrationID, id int) (*models.MigrationJob, error) {
	var job models.MigrationJob
	query := s.internalDB.WithContext(ctx).Where("target_db_id = ? AND migration_id = ?", dbID, migrationID)
	if id > 0 {
		query = query.Where("id = ?", id)
	}
	err := query.Order("id DESC").First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && id == 0 {
		return nil, nil
	}
	return &job, err
}

func (s *Service) RunPublisher(ctx context.Context, conn *amqp.Connection, queue string) error {
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		return err
	}
	if err := ch.Confirm(false); err != nil {
		return err
	}
	returns := ch.NotifyReturn(make(chan amqp.Return, 1))
	for ctx.Err() == nil {
		attemptedJobID := 0
		err := s.internalDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var job models.MigrationJob
			r := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status = ? AND published_at IS NULL AND next_attempt_at <= ?", "QUEUED", time.Now().UTC()).Order("id").Limit(1).Find(&job)
			if r.Error != nil || r.RowsAffected == 0 {
				return r.Error
			}
			attemptedJobID = job.ID
			publishCtx := propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": job.TraceParent})
			publishCtx, span := otel.Tracer("wbd/async").Start(publishCtx, "queue.publish")
			defer span.End()
			carrier := propagation.MapCarrier{}
			propagation.TraceContext{}.Inject(publishCtx, carrier)
			body, _ := json.Marshal(map[string]int{"job_id": job.ID})
			confirm, err := ch.PublishWithDeferredConfirmWithContext(publishCtx, "", queue, true, false, amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body, Headers: amqp.Table{"traceparent": carrier.Get("traceparent")}})
			if err != nil {
				return err
			}
			waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if confirm == nil {
				return errors.New("publisher confirm missing")
			}
			ok, err := confirm.WaitContext(waitCtx)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("publication rejected")
			}
			select {
			case <-returns:
				return errors.New("publication unroutable")
			default:
			}
			jobEvent(publishCtx, "job.published", job)
			return tx.Model(&job).Updates(map[string]any{"published_at": time.Now().UTC(), "publish_failures": 0}).Error
		})
		if err != nil {
			if attemptedJobID > 0 && ctx.Err() == nil {
				updates := map[string]any{"publish_failures": gorm.Expr("publish_failures + 1"), "next_attempt_at": time.Now().UTC().Add(5 * time.Second)}
				if updateErr := s.internalDB.WithContext(ctx).Model(&models.MigrationJob{}).Where("id = ? AND status = ? AND published_at IS NULL", attemptedJobID, "QUEUED").Updates(updates).Error; updateErr != nil {
					return errors.Join(err, updateErr)
				}
			}
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
	return nil
}
