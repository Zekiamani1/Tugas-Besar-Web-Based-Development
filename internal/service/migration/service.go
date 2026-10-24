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
	StatusDraft   = "DRAFT"
	StatusApplied = "APPLIED"
	StatusFailed  = "FAILED"
)

var (
	ErrInvalidMigration       = errors.New("invalid migration")
	ErrDraftNotFound          = errors.New("migration draft not found")
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

// Initialize creates the internal history table and prevents duplicate applied versions for the same target database. Call this once during application startup.
func (s *Service) Initialize(ctx context.Context) error {
	if err := s.internalDB.WithContext(ctx).AutoMigrate(&models.MigrationRecord{}); err != nil {
		return err
	}
	return s.internalDB.WithContext(ctx).Exec(`CREATE UNIQUE INDEX IF NOT EXISTS migration_applied_version_unique
		ON migration_records (target_db_id, version) WHERE version > 0`).Error
}

func (s *Service) CreateDraft(ctx context.Context, targetDBID int, sqlQuery string) (int, error) {
	if targetDBID <= 0 || strings.TrimSpace(sqlQuery) == "" {
		return 0, ErrInvalidMigration
	}
	record := models.MigrationRecord{
		TargetDBID: targetDBID,
		SQLQuery:   sqlQuery,
		Status:     StatusDraft,
	}
	if err := s.internalDB.WithContext(ctx).Create(&record).Error; err != nil {
		return 0, err
	}
	return record.ID, nil
}

func (s *Service) UpdateDraft(ctx context.Context, targetDBID, migrationID int, sqlQuery string) error {
	if targetDBID <= 0 || migrationID <= 0 || strings.TrimSpace(sqlQuery) == "" {
		return ErrInvalidMigration
	}
	result := s.internalDB.WithContext(ctx).Model(&models.MigrationRecord{}).
		Where("id = ? AND target_db_id = ? AND status = ?", migrationID, targetDBID, StatusDraft).
		Update("sql_query", sqlQuery)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrDraftNotFound
	}
	return nil
}

func (s *Service) GetMigrationDetail(ctx context.Context, targetDBID, migrationID int) (*models.MigrationRecord, error) {
	if targetDBID <= 0 || migrationID <= 0 {
		return nil, ErrInvalidMigration
	}
	var record models.MigrationRecord
	err := s.internalDB.WithContext(ctx).
		Where("id = ? AND target_db_id = ?", migrationID, targetDBID).
		First(&record).Error
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Service) GetLinearHistory(ctx context.Context, targetDBID int) ([]models.MigrationRecord, error) {
	if targetDBID <= 0 {
		return nil, ErrInvalidMigration
	}
	var records []models.MigrationRecord
	err := s.internalDB.WithContext(ctx).
		Where("target_db_id = ? AND status IN ?", targetDBID, []string{StatusApplied, StatusFailed}).
		Order("executed_at ASC, id ASC").Find(&records).Error
	return records, err
}

// ExecuteMigration expects executedBy to come from a validated server-side session.
// The internal PostgreSQL advisory lock serializes migrations per target database.
func (s *Service) ExecuteMigration(ctx context.Context, targetDBID, migrationID int, executedBy string) (*models.MigrationRecord, error) {
	if targetDBID <= 0 || migrationID <= 0 || strings.TrimSpace(executedBy) == "" {
		return nil, ErrInvalidMigration
	}

	var record models.MigrationRecord
	var executionErr error
	targetCommitted := false
	err := s.internalDB.WithContext(ctx).Transaction(func(meta *gorm.DB) error {
		if err := meta.Exec("SELECT pg_advisory_xact_lock(3110, ?)", targetDBID).Error; err != nil {
			return err
		}
		if err := meta.Where("id = ? AND target_db_id = ? AND status = ?", migrationID, targetDBID, StatusDraft).
			First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrDraftNotFound
			}
			return err
		}

		var lastVersion int
		if err := meta.Model(&models.MigrationRecord{}).
			Where("target_db_id = ? AND status = ?", targetDBID, StatusApplied).
			Select("COALESCE(MAX(version), 0)").Scan(&lastVersion).Error; err != nil {
			return err
		}

		conn, err := s.dbMgmt.GetTargetConnection(ctx, targetDBID)
		if err != nil {
			return s.recordFailure(meta, &record, executedBy, err, &executionErr)
		}
		if conn == nil {
			return s.recordFailure(meta, &record, executedBy, errors.New("target connection is nil"), &executionErr)
		}
		// DB management currently returns a new sql.DB for each call.
		defer conn.Close()

		targetTx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return s.recordFailure(meta, &record, executedBy, err, &executionErr)
		}
		if _, err := targetTx.ExecContext(ctx, record.SQLQuery); err != nil {
			if rollbackErr := targetTx.Rollback(); rollbackErr != nil {
				err = errors.Join(err, fmt.Errorf("rollback target migration: %w", rollbackErr))
			}
			return s.recordFailure(meta, &record, executedBy, err, &executionErr)
		}
		if err := targetTx.Commit(); err != nil {
			// A commit error does not prove whether the target applied the SQL.
			return fmt.Errorf("%w: %v", ErrTargetCommitUncertain, err)
		}
		targetCommitted = true

		record.Version = lastVersion + 1
		record.Status = StatusApplied
		record.ExecutedBy = executedBy
		record.ExecutedAt = time.Now().UTC()
		return meta.Save(&record).Error
	})
	if err != nil {
		if targetCommitted {
			return nil, fmt.Errorf("%w: %v", ErrHistoryUpdateUncertain, err)
		}
		return nil, err
	}
	if executionErr != nil {
		return &record, executionErr
	}
	return &record, nil
}

func (s *Service) recordFailure(meta *gorm.DB, record *models.MigrationRecord, executedBy string, cause error, executionErr *error) error {
	record.Status = StatusFailed
	record.ExecutedBy = executedBy
	record.ExecutedAt = time.Now().UTC()
	if err := meta.Save(record).Error; err != nil {
		return errors.Join(cause, fmt.Errorf("record failed migration: %w", err))
	}
	*executionErr = cause
	return nil
}
