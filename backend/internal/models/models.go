package models

import (
	"encoding/json"
	"time"
)

type TargetDBInfo struct {
	ID       int    `gorm:"primaryKey" json:"id"`
	Name     string `json:"name"`
	DBType   string `json:"dbType"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	DBName   string `json:"dbName"`
}

type TableMetadata struct {
	Schema      string           `json:"schema"`
	TableName   string           `json:"tableName"`
	Columns     []ColumnInfo     `json:"columns"`
	Constraints []ConstraintInfo `json:"constraints"`
	Indexes     []IndexInfo      `json:"indexes"`
}

type ColumnInfo struct {
	Name       string `json:"name"`
	DataType   string `json:"dataType"`
	IsNullable bool   `json:"isNullable"`
}

type ConstraintInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type IndexInfo struct {
	Name     string `json:"name"`
	IsUnique bool   `json:"isUnique"`
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
	ID                 int    `gorm:"primaryKey" json:"migration_id"`
	TargetDBID         int    `gorm:"not null;index" json:"target_db_id"`
	Revision           int    `gorm:"not null;default:1" json:"revision"`
	SQLUp              string `gorm:"not null" json:"sql_up"`
	SQLDown            string `json:"sql_down"`
	Irreversible       bool   `gorm:"not null" json:"irreversible"`
	IrreversibleReason string `json:"irreversible_reason"`
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
	ID                 int        `json:"id"`
	ExecutionID        int        `json:"executionId,omitempty"`
	Action             string     `json:"action,omitempty"`
	Revision           int        `json:"revision,omitempty"`
	RevertsExecutionID *int       `json:"revertsExecutionId,omitempty"`
	TargetDBID         int        `json:"targetDbId"`
	Version            int        `json:"version"`
	SQLQuery           string     `json:"sqlQuery"`
	ExecutedBy         string     `json:"executedBy"`
	Status             string     `json:"status"`
	ExecutedAt         time.Time  `json:"executedAt"`
	FinishedAt         *time.Time `json:"finishedAt,omitempty"`
}

func (m MigrationRecord) MarshalJSON() ([]byte, error) {
	type migrationJSON struct {
		ID                 int        `json:"id"`
		ExecutionID        int        `json:"executionId,omitempty"`
		Action             string     `json:"action,omitempty"`
		Revision           int        `json:"revision,omitempty"`
		RevertsExecutionID *int       `json:"revertsExecutionId,omitempty"`
		TargetDBID         int        `json:"targetDbId"`
		Version            int        `json:"version"`
		SQLQuery           string     `json:"sqlQuery"`
		ExecutedBy         string     `json:"executedBy"`
		Status             string     `json:"status"`
		ExecutedAt         string     `json:"executedAt"`
		FinishedAt         *time.Time `json:"finishedAt,omitempty"`
	}
	executedAt := ""
	if !m.ExecutedAt.IsZero() {
		executedAt = m.ExecutedAt.UTC().Format(time.RFC3339)
	}
	return json.Marshal(migrationJSON{
		ID:                 m.ID,
		ExecutionID:        m.ExecutionID,
		Action:             m.Action,
		Revision:           m.Revision,
		RevertsExecutionID: m.RevertsExecutionID,
		TargetDBID:         m.TargetDBID,
		Version:            m.Version,
		SQLQuery:           m.SQLQuery,
		ExecutedBy:         m.ExecutedBy,
		Status:             m.Status,
		ExecutedAt:         executedAt,
		FinishedAt:         m.FinishedAt,
	})
}

type User struct {
	ID           int    `gorm:"primaryKey"`
	Username     string `gorm:"uniqueIndex;not null"`
	PasswordHash string `gorm:"not null"`
	IsAdmin      bool   `gorm:"not null;default:false"`
}

// UserSummary contains the user fields that are safe to display in admin pages.
type UserSummary struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
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
	ID       int    `gorm:"primaryKey" json:"id"`
	Name     string `gorm:"uniqueIndex;not null" json:"name"`
	LeaderID *int   `json:"leader_id"`
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

type MigrationJob struct {
	ID                  int    `gorm:"primaryKey" json:"job_id"`
	ApprovalID          string `gorm:"uniqueIndex:migration_jobs_approval_id_unique,where:approval_id <> ''" json:"-"`
	Revision            int
	ExpectedExecutionID int
	ExecutionID         int64      `json:"execution_id"`
	SourceJobID         int        `json:"-"`
	MigrationID         int        `json:"migration_id"`
	TargetDBID          int        `gorm:"index" json:"target_db_id"`
	Actor               string     `json:"-"`
	Action              string     `json:"action"`
	Mode                string     `json:"mode"`
	BatchSize           int        `json:"batch_size"`
	BatchTable          string     `json:"batch_table,omitempty"`
	BatchAttribute      string     `json:"batch_attribute,omitempty"`
	Status              string     `gorm:"index" json:"status"`
	Attempts            int        `json:"attempts"`
	PublishFailures     int        `json:"-"`
	CommittedBatches    int64      `json:"committed_batches"`
	CommittedRows       int64      `json:"committed_rows"`
	TotalRows           int64      `json:"total_rows"`
	Cursor              int64      `json:"cursor"`
	ErrorMessage        string     `json:"error,omitempty"`
	ResumedBy           string     `json:"-"`
	PlannedVersion      int        `json:"-"`
	PublishedAt         *time.Time `json:"-"`
	NextAttemptAt       time.Time  `json:"next_attempt_at"`
	TraceParent         string     `json:"-"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}
