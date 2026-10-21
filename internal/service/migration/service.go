package migration

import (
	"context"

	"gorm.io/gorm"
	"wbd/internal/models"
	"wbd/internal/service/interfaces"
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

func (s *Service) CreateDraft(ctx context.Context, targetDBID int, sqlQuery string) (int, error) {
	return 0, nil
}

func (s *Service) UpdateDraft(ctx context.Context, targetDBID, migrationID int, sqlQuery string) error {
	return nil
}

func (s *Service) GetMigrationDetail(ctx context.Context, targetDBID, migrationID int) (*models.MigrationRecord, error) {
	return nil, nil
}

func (s *Service) GetLinearHistory(ctx context.Context, targetDBID int) ([]models.MigrationRecord, error) {
	return nil, nil
}

func (s *Service) ExecuteMigration(ctx context.Context, targetDBID, migrationID int, token string) (*models.MigrationRecord, error) {
	//get target db connection using dbmanagement's gettargetconnection and execute migration in transaction
	return nil, nil
}
