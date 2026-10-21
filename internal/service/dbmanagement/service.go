package dbmanagement

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

type Service struct {
	internalDB *gorm.DB
	pools      map[int]*sql.DB
}

func NewService(internalDB *gorm.DB) *Service {
	return &Service{
		internalDB: internalDB,
		pools:      make(map[int]*sql.DB),
	}
}

func (s *Service) RegisterTargetDB(ctx context.Context, info TargetDBInfo) error {
	return nil
}

func (s *Service) GetTargetConnection(ctx context.Context, targetDBID int) (*sql.DB, error) {
	return nil, nil
}

func (s *Service) GetSchemas(ctx context.Context, targetDBID int) ([]string, error) {
	return nil, nil
}

func (s *Service) GetTables(ctx context.Context, targetDBID int, schema string) ([]string, error) {
	return nil, nil
}

func (s *Service) GetTableDetails(ctx context.Context, targetDBID int, schema, tableName string) (string, error) {
	return "", nil
}

func (s *Service) GetTableData(ctx context.Context, targetDBID int, schema, tableName string, limit, offset int) ([]map[string]any, error) {
	return nil, nil
}

func (s *Service) CloseAllTargetConnections() {
}
