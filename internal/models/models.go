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

type MigrationRecord struct {
	ID         int
	TargetDBID int
	Version    int
	SQLQuery   string
	ExecutedBy string
	Status     string
	ExecutedAt time.Time
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

// GroupSummary contains the group fields needed by admin pages.
type GroupSummary struct {
	ID   int
	Name string
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
