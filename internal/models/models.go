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
