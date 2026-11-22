package models

import "time"

type TargetDBInfo struct {
	ID       int `gorm:"primaryKey"`
	Name     string
	DBType   string
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
}

type TableMetadata struct {
	Schema      string
	TableName   string
	Columns     []ColumnInfo
	Constraints []ConstraintInfo
	Indexes     []IndexInfo
}

type ColumnInfo struct {
	Name       string
	DataType   string
	IsNullable bool
}

type ConstraintInfo struct {
	Name string
	Type string
}

type IndexInfo struct {
	Name     string
	IsUnique bool
}

// MigrationDraft is the editable SQL definition supplied by a caller.
type MigrationDraft struct {
	SQLUp              string `json:"sql_up"`
	SQLDown            string `json:"sql_down"`
	Irreversible       bool   `json:"irreversible"`
	IrreversibleReason string `json:"irreversible_reason"`
}

// Migration stores the definition, independently of its execution history.
type Migration struct {
	ID                 int    `gorm:"primaryKey"`
	TargetDBID         int    `gorm:"not null;index"`
	Revision           int    `gorm:"not null;default:1"`
	SQLUp              string `gorm:"not null"`
	SQLDown            string
	Irreversible       bool `gorm:"not null"`
	IrreversibleReason string
}

// MigrationExecution stores one attempt. SQLSnapshot is the SQL used by that
// attempt, not a reference to the current migration definition.
type MigrationExecution struct {
	ID                 int       `gorm:"primaryKey"`
	MigrationID        int       `gorm:"not null;index"`
	MigrationRevision  int       `gorm:"not null"`
	TargetDBID         int       `gorm:"not null;index"`
	Action             string    `gorm:"not null"`
	SQLSnapshot        string    `gorm:"not null"`
	Version            int       `gorm:"not null;default:0"`
	ExecutedBy         string    `gorm:"not null"`
	Status             string    `gorm:"not null"`
	StartedAt          time.Time `gorm:"not null"`
	FinishedAt         *time.Time
	Error              string
	RevertsExecutionID *int
	// Set only when importing an M1 record, so repeated initialization is safe.
	LegacyMigrationID *int `gorm:"uniqueIndex"`
}

// MigrationRecord is the M1-compatible read view, not the writable migration
// model. It also describes the archived migration_records table during import.
type MigrationRecord struct {
	ID          int
	ExecutionID int
	TargetDBID  int
	Version     int
	SQLQuery    string
	ExecutedBy  string
	Status      string
	ExecutedAt  time.Time
}

type User struct {
	ID           int    `gorm:"primaryKey"`
	Username     string `gorm:"uniqueIndex;not null"`
	PasswordHash string `gorm:"not null"`
	IsAdmin      bool   `gorm:"not null;default:false"`
}

// UserSummary contains the user fields that are safe to display in admin pages.
type UserSummary struct {
	ID       int
	Username string
	IsAdmin  bool
}

// SessionValidation contains the authenticated user and the current session ID.
type SessionValidation struct {
	UserID           int
	CurrentSessionID string
	UsedGracePeriod  bool
}

type Session struct {
	ID            int       `gorm:"primaryKey"`
	UserID        int       `gorm:"not null;index"`
	SessionID     string    `gorm:"uniqueIndex;not null"`
	CreatedAt     time.Time `gorm:"not null"`
	LastSeenAt    time.Time `gorm:"not null"`
	LastRotatedAt time.Time `gorm:"not null;default:CURRENT_TIMESTAMP"`
	ExpiresAt     time.Time `gorm:"not null"`
}

// SessionAlias keeps each rotated identifier valid only for its grace period.
type SessionAlias struct {
	SessionID       string    `gorm:"primaryKey"`
	SessionRecordID int       `gorm:"not null;index"`
	ExpiresAt       time.Time `gorm:"not null;index"`
}

type Group struct {
	ID   int    `gorm:"primaryKey"`
	Name string `gorm:"uniqueIndex;not null"`
}

// GroupMemberSummary contains safe user details for a group's member list.
type GroupMemberSummary struct {
	GroupID  int
	UserID   int
	Username string
}

// GroupDBAccessSummary describes one database permission assigned to a group.
type GroupDBAccessSummary struct {
	GroupID      int
	TargetDBID   int
	DatabaseName string
	AccessLevel  string
}

type UserGroup struct {
	UserID  int `gorm:"primaryKey;autoIncrement:false"`
	GroupID int `gorm:"primaryKey;autoIncrement:false"`
}

type GroupDBAccess struct {
	GroupID     int    `gorm:"primaryKey;autoIncrement:false"`
	TargetDBID  int    `gorm:"primaryKey;autoIncrement:false"`
	AccessLevel string `gorm:"not null"`
}
