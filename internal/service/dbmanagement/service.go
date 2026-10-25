package dbmanagement

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"wbd/internal/models"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"

	"gorm.io/gorm"
)

type Service struct {
	internalDB *gorm.DB
	conns      sync.Map // targetDBID (int) -> *sql.DB
	infos      sync.Map // targetDBID (int) -> models.TargetDBInfo
}

func NewService(internalDB *gorm.DB) *Service {
	internalDB.AutoMigrate(&models.TargetDBInfo{})
	return &Service{
		internalDB: internalDB,
	}
}

func (s *Service) RegisterTargetDB(ctx context.Context, info models.TargetDBInfo) (int, error) {
	if err := s.internalDB.WithContext(ctx).Create(&info).Error; err != nil {
		return 0, err
	}
	return info.ID, nil
}

func (s *Service) ListDatabases(ctx context.Context) ([]models.TargetDBInfo, error) {
	var dbs []models.TargetDBInfo
	if err := s.internalDB.WithContext(ctx).Find(&dbs).Error; err != nil {
		return nil, err
	}
	return dbs, nil
}

func (s *Service) GetDatabases(ctx context.Context) ([]models.TargetDBInfo, error) {
	return s.ListDatabases(ctx)
}

func buildTargetDSN(info models.TargetDBInfo) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(info.DBType)) {
	case "postgres", "postgresql":
		port := info.Port
		if port == 0 {
			port = 5432
		}
		dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
			info.User, info.Password, info.Host, port, info.DBName)
		return "postgres", dsn, nil

	case "mysql":
		port := info.Port
		if port == 0 {
			port = 3306
		}
		host := info.Host
		if host == "" {
			host = "127.0.0.1"
		}
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
			info.User, info.Password, host, port, info.DBName)
		return "mysql", dsn, nil

	case "sqlite", "sqlite3":
		dsn := info.DBName
		if dsn == "" {
			dsn = info.Host
		}
		if dsn == "" {
			dsn = ":memory:"
		}
		return "sqlite", dsn, nil

	case "sqlmock":
		port := info.Port
		if port == 0 {
			port = 5432
		}
		dsn := fmt.Sprintf("sqlmock://%s:%s@%s:%s/%s?sslmode=disable",
			info.User, info.Password, info.Host, strconv.Itoa(port), info.DBName)
		return "sqlmock", dsn, nil

	default:
		return info.DBType, fmt.Sprintf("%s://%s:%s@%s:%d/%s?sslmode=disable",
			info.DBType, info.User, info.Password, info.Host, info.Port, info.DBName), nil
	}
}

func (s *Service) getTargetDBInfo(ctx context.Context, targetDBID int) (models.TargetDBInfo, error) {
	if val, ok := s.infos.Load(targetDBID); ok {
		if info, ok := val.(models.TargetDBInfo); ok {
			return info, nil
		}
	}

	var dbInfo models.TargetDBInfo
	if err := s.internalDB.WithContext(ctx).Model(&models.TargetDBInfo{}).Where("id = ?", targetDBID).Take(&dbInfo).Error; err != nil {
		return models.TargetDBInfo{}, err
	}

	s.infos.Store(targetDBID, dbInfo)
	return dbInfo, nil
}

func (s *Service) GetTargetConnection(ctx context.Context, targetDBID int) (*sql.DB, error) {
	if val, ok := s.conns.Load(targetDBID); ok {
		if conn, ok := val.(*sql.DB); ok && conn != nil {
			return conn, nil
		}
	}

	var info models.TargetDBInfo
	if err := s.internalDB.WithContext(ctx).Model(&models.TargetDBInfo{}).Where("id = ?", targetDBID).Take(&info).Error; err != nil {
		return nil, err
	}

	driver, dsn, err := buildTargetDSN(info)
	if err != nil {
		return nil, err
	}

	conn, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}

	actual, loaded := s.conns.LoadOrStore(targetDBID, conn)
	if loaded {
		_ = conn.Close()
		return actual.(*sql.DB), nil
	}

	s.infos.Store(targetDBID, info)
	return conn, nil
}

func (s *Service) GetSchemas(ctx context.Context, targetDBID int) ([]string, error) {
	conn, err := s.GetTargetConnection(ctx, targetDBID)
	if err != nil {
		return nil, err
	}
	info, err := s.getTargetDBInfo(ctx, targetDBID)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(strings.TrimSpace(info.DBType)) {
	case "sqlite", "sqlite3":
		rows, err := conn.QueryContext(ctx, "PRAGMA database_list")
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var schemas []string
		for rows.Next() {
			var seq int
			var name, file string
			if err := rows.Scan(&seq, &name, &file); err != nil {
				return nil, err
			}
			schemas = append(schemas, name)
		}
		if len(schemas) == 0 {
			schemas = []string{"main"}
		}
		return schemas, nil

	case "mysql":
		query := "SELECT schema_name FROM information_schema.schemata"
		var rows *sql.Rows
		if info.DBName != "" {
			query = "SELECT schema_name FROM information_schema.schemata WHERE schema_name = ?"
			rows, err = conn.QueryContext(ctx, query, info.DBName)
		} else {
			rows, err = conn.QueryContext(ctx, query)
		}
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var schemas []string
		for rows.Next() {
			var schema string
			if err := rows.Scan(&schema); err != nil {
				return nil, err
			}
			schemas = append(schemas, schema)
		}
		if len(schemas) == 0 && info.DBName != "" {
			schemas = []string{info.DBName}
		}
		return schemas, nil

	default: // postgres, sqlmock
		res, err := conn.QueryContext(ctx, `SELECT schema_name FROM information_schema.schemata`)
		if err != nil {
			return nil, err
		}
		defer res.Close()

		var schemas []string
		for res.Next() {
			var schema string
			if err := res.Scan(&schema); err != nil {
				return nil, err
			}
			schemas = append(schemas, schema)
		}
		return schemas, nil
	}
}

func (s *Service) GetTables(ctx context.Context, targetDBID int, schema string) ([]string, error) {
	conn, err := s.GetTargetConnection(ctx, targetDBID)
	if err != nil {
		return nil, err
	}
	info, err := s.getTargetDBInfo(ctx, targetDBID)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(strings.TrimSpace(info.DBType)) {
	case "sqlite", "sqlite3":
		rows, err := conn.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var tables []string
		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				return nil, err
			}
			tables = append(tables, table)
		}
		return tables, nil

	case "mysql":
		rows, err := conn.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = ?`, schema)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var tables []string
		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				return nil, err
			}
			tables = append(tables, table)
		}
		return tables, nil

	default: // postgres, sqlmock
		res, err := conn.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = $1`, schema)
		if err != nil {
			return nil, err
		}
		defer res.Close()

		var tables []string
		for res.Next() {
			var table string
			if err := res.Scan(&table); err != nil {
				return nil, err
			}
			tables = append(tables, table)
		}
		return tables, nil
	}
}

func (s *Service) GetTableDetails(ctx context.Context, targetDBID int, schema, tableName string) (*models.TableMetadata, error) {
	conn, err := s.GetTargetConnection(ctx, targetDBID)
	if err != nil {
		return nil, err
	}
	info, err := s.getTargetDBInfo(ctx, targetDBID)
	if err != nil {
		return nil, err
	}

	metadata := &models.TableMetadata{
		Schema:    schema,
		TableName: tableName,
	}

	switch strings.ToLower(strings.TrimSpace(info.DBType)) {
	case "sqlite", "sqlite3":
		cols, err := conn.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info("%s")`, tableName))
		if err != nil {
			return nil, err
		}
		defer cols.Close()

		hasPK := false
		for cols.Next() {
			var cid int
			var name, colType string
			var notnull, pk int
			var dfltValue sql.NullString
			if err := cols.Scan(&cid, &name, &colType, &notnull, &dfltValue, &pk); err != nil {
				return nil, err
			}
			metadata.Columns = append(metadata.Columns, models.ColumnInfo{
				Name:       name,
				DataType:   colType,
				IsNullable: notnull == 0,
			})
			if pk > 0 {
				hasPK = true
			}
		}
		if err := cols.Err(); err != nil {
			return nil, err
		}
		if hasPK {
			metadata.Constraints = append(metadata.Constraints, models.ConstraintInfo{
				Name: "PRIMARY KEY",
				Type: "PRIMARY KEY",
			})
		}

		idxRows, err := conn.QueryContext(ctx, fmt.Sprintf(`PRAGMA index_list("%s")`, tableName))
		if err == nil {
			defer idxRows.Close()
			for idxRows.Next() {
				var seq int
				var idxName string
				var unique int
				var origin string
				var partial int
				if err := idxRows.Scan(&seq, &idxName, &unique, &origin, &partial); err == nil {
					metadata.Indexes = append(metadata.Indexes, models.IndexInfo{
						Name:     idxName,
						IsUnique: unique == 1,
					})
				}
			}
		}
		return metadata, nil

	case "mysql":
		cols, err := conn.QueryContext(ctx, `SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = ? AND table_name = ?`, schema, tableName)
		if err != nil {
			return nil, err
		}
		defer cols.Close()

		for cols.Next() {
			var col models.ColumnInfo
			var nullable string
			if err := cols.Scan(&col.Name, &col.DataType, &nullable); err != nil {
				return nil, err
			}
			col.IsNullable = strings.EqualFold(nullable, "YES")
			metadata.Columns = append(metadata.Columns, col)
		}
		if err := cols.Err(); err != nil {
			return nil, err
		}

		constrains, err := conn.QueryContext(ctx, `SELECT constraint_name, constraint_type FROM information_schema.table_constraints WHERE table_schema = ? AND table_name = ?`, schema, tableName)
		if err != nil {
			return nil, err
		}
		defer constrains.Close()

		for constrains.Next() {
			var c models.ConstraintInfo
			if err := constrains.Scan(&c.Name, &c.Type); err != nil {
				return nil, err
			}
			metadata.Constraints = append(metadata.Constraints, c)
		}
		if err := constrains.Err(); err != nil {
			return nil, err
		}

		indexes, err := conn.QueryContext(ctx, `SELECT DISTINCT index_name, non_unique FROM information_schema.statistics WHERE table_schema = ? AND table_name = ?`, schema, tableName)
		if err != nil {
			return nil, err
		}
		defer indexes.Close()

		for indexes.Next() {
			var name string
			var nonUnique int
			if err := indexes.Scan(&name, &nonUnique); err != nil {
				return nil, err
			}
			metadata.Indexes = append(metadata.Indexes, models.IndexInfo{
				Name:     name,
				IsUnique: nonUnique == 0,
			})
		}
		if err := indexes.Err(); err != nil {
			return nil, err
		}
		return metadata, nil

	default: // postgres, sqlmock
		cols, err := conn.QueryContext(ctx, `SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2`, schema, tableName)
		if err != nil {
			return nil, err
		}
		defer cols.Close()

		for cols.Next() {
			var col models.ColumnInfo
			var nullable string
			if err := cols.Scan(&col.Name, &col.DataType, &nullable); err != nil {
				return nil, err
			}
			col.IsNullable = strings.EqualFold(nullable, "YES")
			metadata.Columns = append(metadata.Columns, col)
		}
		if err := cols.Err(); err != nil {
			return nil, err
		}

		constrains, err := conn.QueryContext(ctx, `SELECT constraint_name, constraint_type FROM information_schema.table_constraints WHERE table_schema = $1 AND table_name = $2`, schema, tableName)
		if err != nil {
			return nil, err
		}
		defer constrains.Close()

		for constrains.Next() {
			var c models.ConstraintInfo
			if err := constrains.Scan(&c.Name, &c.Type); err != nil {
				return nil, err
			}
			metadata.Constraints = append(metadata.Constraints, c)
		}
		if err := constrains.Err(); err != nil {
			return nil, err
		}

		indexes, err := conn.QueryContext(ctx, `SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = $1 AND tablename = $2`, schema, tableName)
		if err != nil {
			return nil, err
		}
		defer indexes.Close()

		for indexes.Next() {
			var name, def string
			if err := indexes.Scan(&name, &def); err != nil {
				return nil, err
			}
			metadata.Indexes = append(metadata.Indexes, models.IndexInfo{
				Name:     name,
				IsUnique: strings.Contains(strings.ToUpper(def), "UNIQUE"),
			})
		}
		if err := indexes.Err(); err != nil {
			return nil, err
		}

		return metadata, nil
	}
}

func (s *Service) GetTableData(ctx context.Context, targetDBID int, schema, tableName string, limit, offset int) ([]map[string]any, error) {
	conn, err := s.GetTargetConnection(ctx, targetDBID)
	if err != nil {
		return nil, err
	}
	info, err := s.getTargetDBInfo(ctx, targetDBID)
	if err != nil {
		return nil, err
	}

	var query string
	var args []any
	switch strings.ToLower(strings.TrimSpace(info.DBType)) {
	case "sqlite", "sqlite3":
		if schema == "" || schema == "main" {
			query = fmt.Sprintf(`SELECT * FROM "%s" LIMIT ? OFFSET ?`, tableName)
		} else {
			query = fmt.Sprintf(`SELECT * FROM "%s"."%s" LIMIT ? OFFSET ?`, schema, tableName)
		}
		args = []any{limit, offset}
	case "mysql":
		if schema == "" {
			query = fmt.Sprintf("SELECT * FROM `%s` LIMIT ? OFFSET ?", tableName)
		} else {
			query = fmt.Sprintf("SELECT * FROM `%s`.`%s` LIMIT ? OFFSET ?", schema, tableName)
		}
		args = []any{limit, offset}
	default: // postgres, sqlmock
		query = fmt.Sprintf(`SELECT * FROM "%s"."%s" LIMIT $1 OFFSET $2`, schema, tableName)
		args = []any{limit, offset}
	}

	datas, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer datas.Close()

	cols, err := datas.Columns()
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for datas.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := datas.Scan(ptrs...); err != nil {
			return nil, err
		}
		rowMap := make(map[string]any, len(cols))
		for i, col := range cols {
			if b, ok := values[i].([]byte); ok {
				rowMap[col] = string(b)
			} else {
				rowMap[col] = values[i]
			}
		}
		result = append(result, rowMap)
	}
	if err := datas.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) CloseAllTargetConnections() {
	s.conns.Range(func(key, value any) bool {
		if conn, ok := value.(*sql.DB); ok && conn != nil {
			_ = conn.Close()
		}
		s.conns.Delete(key)
		return true
	})
	s.infos.Range(func(key, value any) bool {
		s.infos.Delete(key)
		return true
	})
}
