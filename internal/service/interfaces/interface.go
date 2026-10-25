package interfaces

import (
	"context"
	"database/sql"

	"wbd/internal/models"
)

type AccessControlService interface {
	Register(ctx context.Context, username, password string) error
	Login(ctx context.Context, username, password string) (string, error)
	Logout(ctx context.Context, sessionID string) error
	ValidateSession(ctx context.Context, sessionID string) (int, error)
	ValidateSessionWithRotation(ctx context.Context, sessionID string) (models.SessionValidation, error)
	ListUsers(ctx context.Context) ([]models.UserSummary, error)
	ListGroups(ctx context.Context) ([]models.Group, error)
	ListGroupMembers(ctx context.Context) ([]models.GroupMemberSummary, error)
	ListGroupDBAccess(ctx context.Context) ([]models.GroupDBAccessSummary, error)
	CreateGroup(ctx context.Context, groupName string) error
	AssignUserToGroup(ctx context.Context, userID, groupID int) error
	SetGroupDBAccess(ctx context.Context, groupID, targetDBID int, accessLevel string) error
	EvaluateDBAccess(ctx context.Context, userID, targetDBID int, requiredLevel string) error
	IsAdmin(ctx context.Context, userID int) (bool, error)
}

type DBManagementService interface {
	RegisterTargetDB(ctx context.Context, info models.TargetDBInfo) (int, error)
	ListDatabases(ctx context.Context) ([]models.TargetDBInfo, error)
	GetTargetConnection(ctx context.Context, targetDBID int) (*sql.DB, error)
	GetSchemas(ctx context.Context, targetDBID int) ([]string, error)
	GetTables(ctx context.Context, targetDBID int, schema string) ([]string, error)
	GetTableDetails(ctx context.Context, targetDBID int, schema, tableName string) (*models.TableMetadata, error)
	GetTableData(ctx context.Context, targetDBID int, schema, tableName string, limit, offset int) ([]map[string]any, error)
	CloseAllTargetConnections()
}

type MigrationService interface {
	CreateDraft(ctx context.Context, targetDBID int, sqlQuery string) (int, error)
	UpdateDraft(ctx context.Context, targetDBID, migrationID int, sqlQuery string) error
	GetMigrationDetail(ctx context.Context, targetDBID, migrationID int) (*models.MigrationRecord, error)
	GetDrafts(ctx context.Context, targetDBID int) ([]models.MigrationRecord, error)
	GetLinearHistory(ctx context.Context, targetDBID int) ([]models.MigrationRecord, error)
	ExecuteMigration(ctx context.Context, targetDBID, migrationID int, token string) (*models.MigrationRecord, error)
}
