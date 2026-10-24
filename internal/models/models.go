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
	ID               int    `gorm:"primaryKey"`
	Username         string `gorm:"uniqueIndex;not null"`
	PasswordHash     string `gorm:"not null"`
	IsAdmin  bool  `gorm:"not null;default:false"`
}

type Session struct {
	ID         int       `gorm:"primaryKey"`
	UserID     int       `gorm:"not null;index"`
	SessionID  string    `gorm:"uniqueIndex;not null"`
	CreatedAt  time.Time `gorm:"not null"`
	LastSeenAt time.Time `gorm:"not null"`
	ExpiresAt  time.Time `gorm:"not null"`
}

type Group struct {
    ID   int    `gorm:"primaryKey"`
    Name string `gorm:"uniqueIndex;not null"`
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