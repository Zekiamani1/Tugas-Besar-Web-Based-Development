package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"wbd/internal/models"
	"wbd/internal/service/interfaces"
)

const (
	StatusDraft     = "DRAFT"
	StatusApplied   = "APPLIED"
	StatusFailed    = "FAILED"
	StatusRunning   = "RUNNING"
	StatusUncertain = "UNCERTAIN"
	ActionApply     = "APPLY"
	upOnlyReason    = "SQL down was not supplied by the M1 SQL-up-only interface."
)

var (
	ErrStaleRevision          = errors.New("migration draft no longer matches the approved revision")
	ErrInvalidMigration       = errors.New("invalid migration")
	ErrDraftNotFound          = errors.New("migration draft not found")
	ErrDatabaseBusy           = errors.New("database has a running or uncertain migration")
	ErrTargetCommitUncertain  = errors.New("target commit could not be confirmed; inspect the target database before retrying")
	ErrHistoryUpdateUncertain = errors.New("target migration committed but history update could not be confirmed; inspect the target database before retrying")
)

type Service struct {
	internalDB *gorm.DB
	dbMgmt     interfaces.DBManagementService
}

func NewService(internalDB *gorm.DB, dbMgmt interfaces.DBManagementService) *Service {
	return &Service{
		internalDB: internalDB,
		dbMgmt:     dbMgmt,
	}
}

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
	if targetDBID <= 0 || migrationID <= 0 || revision <= 0 || strings.TrimSpace(actor) == "" {
		return nil, ErrInvalidMigration
	}
	execution, version, err := s.reserveApplyRevision(ctx, targetDBID, migrationID, revision, actor)
	if err != nil {
		return nil, err
	}
	return s.runAttempt(ctx, execution, version)
}

func (s *Service) reserveApplyRevision(ctx context.Context, targetDBID, migrationID, revision int, executedBy string) (models.MigrationExecution, int, error) {
	return s.reserveAttempt(ctx, targetDBID, func(meta *gorm.DB) (models.MigrationExecution, int, error) {
		var definition models.Migration
		if err := unexecuted(meta).Where("id = ? AND target_db_id = ?", migrationID, targetDBID).
			First(&definition).Error; err != nil {
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
	var execution models.MigrationExecution
	var nextVersion int
	err := s.internalDB.WithContext(ctx).Transaction(func(meta *gorm.DB) error {
		if err := lockDatabase(meta, targetDBID); err != nil {
			return err
		}
		var active int64
		if err := meta.Model(&models.MigrationExecution{}).
			Where("target_db_id = ? AND status IN ?", targetDBID, []string{StatusRunning, StatusUncertain}).
			Count(&active).Error; err != nil {
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
		return meta.Create(&execution).Error
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
	targetTx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fail(err)
	}
	if _, err := targetTx.ExecContext(ctx, execution.SQLSnapshot); err != nil {
		if rollbackErr := targetTx.Rollback(); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("rollback target migration: %w", rollbackErr))
		}
		return fail(err)
	}
	if err := targetTx.Commit(); err != nil {
		uncertainErr := fmt.Errorf("%w: %v", ErrTargetCommitUncertain, err)
		if saveErr := s.finishAttempt(ctx, &execution, StatusUncertain, 0, uncertainErr); saveErr != nil {
			return nil, errors.Join(uncertainErr, saveErr)
		}
		return nil, uncertainErr
	}
	if err := s.finishAttempt(ctx, &execution, StatusApplied, version, nil); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrHistoryUpdateUncertain, err)
	}
	return &execution, nil
}
