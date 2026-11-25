package migration

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"wbd/internal/models"
)

func (s *Service) Initialize(ctx context.Context) error {
	return s.internalDB.WithContext(ctx).Transaction(func(meta *gorm.DB) error {
		if err := meta.Exec("SELECT pg_advisory_xact_lock(3110, 0)").Error; err != nil {
			return err
		}
		if err := meta.AutoMigrate(&models.Migration{}, &models.MigrationExecution{}, &models.MigrationJob{}); err != nil {
			return err
		}
		if err := meta.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS migration_database_inflight_unique
			ON migration_executions (target_db_id) WHERE status IN ('RUNNING', 'UNCERTAIN')`).Error; err != nil {
			return err
		}
		if !meta.Migrator().HasTable("migration_records") {
			return nil
		}
		var unknown int64
		if err := meta.Table("migration_records").
			Where("status IS NULL OR status NOT IN ?", []string{StatusDraft, StatusApplied, StatusFailed}).Count(&unknown).Error; err != nil {
			return err
		}
		if unknown > 0 {
			return fmt.Errorf("cannot import %d M1 migrations with unknown status", unknown)
		}
		if err := meta.Exec(`INSERT INTO migrations
			(id, target_db_id, revision, sql_up, sql_down, irreversible, irreversible_reason)
			SELECT id, target_db_id, 1, sql_query, '', TRUE,
				'M1 migration has no recorded SQL down; safe rollback is unavailable.'
			FROM migration_records
			ON CONFLICT (id) DO NOTHING`).Error; err != nil {
			return err
		}
		if err := meta.Exec(`INSERT INTO migration_executions
			(migration_id, migration_revision, target_db_id, action, sql_snapshot,
			 version, executed_by, status, started_at, finished_at, error, legacy_migration_id)
			SELECT id, 1, target_db_id, 'APPLY', sql_query, COALESCE(version, 0),
				COALESCE(executed_by, ''), status, executed_at, executed_at,
				CASE WHEN status = 'FAILED' THEN 'M1 did not persist the failure details.' ELSE '' END, id
			FROM migration_records WHERE status IN ('APPLIED', 'FAILED')
			ORDER BY target_db_id, version, id
			ON CONFLICT (legacy_migration_id) DO NOTHING`).Error; err != nil {
			return err
		}
		return meta.Exec(`SELECT setval(pg_get_serial_sequence('migrations', 'id'),
			GREATEST(COALESCE((SELECT MAX(id) FROM migrations), 1),
				nextval(pg_get_serial_sequence('migrations', 'id'))), TRUE)`).Error
	})
}
