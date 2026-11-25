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

type MigrationRecord struct {
	ID         int       `json:"id"`
	TargetDBID int       `json:"targetDbId"`
	Version    int       `json:"version"`
	SQLQuery   string    `json:"sqlQuery"`
	ExecutedBy string    `json:"executedBy"`
	Status     string    `json:"status"`
	ExecutedAt time.Time `json:"executedAt"`
}

func (m MigrationRecord) MarshalJSON() ([]byte, error) {
	type migrationJSON struct {
		ID         int    `json:"id"`
		TargetDBID int    `json:"targetDbId"`
		Version    int    `json:"version"`
		SQLQuery   string `json:"sqlQuery"`
		ExecutedBy string `json:"executedBy"`
		Status     string `json:"status"`
		ExecutedAt string `json:"executedAt"`
	}
	executedAt := ""
	if !m.ExecutedAt.IsZero() {
		executedAt = m.ExecutedAt.UTC().Format(time.RFC3339)
	}
	return json.Marshal(migrationJSON{
		ID:         m.ID,
		TargetDBID: m.TargetDBID,
		Version:    m.Version,
		SQLQuery:   m.SQLQuery,
		ExecutedBy: m.ExecutedBy,
		Status:     m.Status,
		ExecutedAt: executedAt,
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
	ID   int    `gorm:"primaryKey" json:"id"`
	Name string `gorm:"uniqueIndex;not null" json:"name"`
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
