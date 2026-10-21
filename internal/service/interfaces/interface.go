package interfaces

import (
	"context"
	"database/sql"
	"wbd/internal/service/dbmanagement"
	"wbd/internal/service/migration"
)

type AccessControlService interface {
	Register(ctx context.Context, username, password string) error
	Login(ctx context.Context, username, password string) (string, error)
	Logout(ctx context.Context, sessionID string) error
	ValidateSession(ctx context.Context, sessionID string) error
	CreateGroup(ctx context.Context, groupName string) error
	AssignUserToGroup(ctx context.Context, userID, groupID int) error
	SetGroupDBAccess(ctx context.Context, groupID, targetDBID int, accessLevel string) error
	EvaluateDBAccess(ctx context.Context, userID, targetDBID int) error
}

type DBManagementService interface {
	RegisterTargetDB(ctx context.Context, info dbmanagement.TargetDBInfo) (int, error)
	GetTargetConnection(ctx context.Context, targetDBID int) (*sql.DB, error)
	GetSchemas(ctx context.Context, targetDBID int) ([]string, error)
	GetTables(ctx context.Context, targetDBID int, schema string) ([]string, error)
	GetTableDetails(ctx context.Context, targetDBID int, schema, tableName string) (string, error)
	GetTableData(ctx context.Context, targetDBID int, schema, tableName string, limit, offset int) ([]map[string]any, error)
	CloseAllTargetConnections()
}

type MigrationService interface {
	CreateDraft(ctx context.Context, targetDBID int, sqlQuery string) (int, error)
	UpdateDraft(ctx context.Context, targetDBID, migrationID int, sqlQuery string) error
	GetMigrationDetail(ctx context.Context, targetDBID, migrationID int) (*migration.MigrationRecord, error)
	GetLinearHistory(ctx context.Context, targetDBID int) ([]migration.MigrationRecord, error)
	ExecuteMigration(ctx context.Context, targetDBID, migrationID, userID int) (*migration.MigrationRecord, error)
}
