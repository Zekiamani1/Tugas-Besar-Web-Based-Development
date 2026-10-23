package dbmanagement

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"wbd/internal/models"

	_ "github.com/lib/pq"

	"gorm.io/gorm"
)

type Service struct {
	internalDB *gorm.DB
}

func NewService(internalDB *gorm.DB) *Service {
	internalDB.AutoMigrate(&models.TargetDBInfo{})
	return &Service{
		internalDB: internalDB,
	}
}

func (s *Service) RegisterTargetDB(ctx context.Context, info models.TargetDBInfo) (int, error) {
	s.internalDB.Create(&info)
	return info.ID, nil
}

func (s *Service) GetTargetConnection(ctx context.Context, targetDBID int) (*sql.DB, error) {
	var info models.TargetDBInfo
	s.internalDB.Model(&models.TargetDBInfo{}).Where("id = ?", targetDBID).Scan(&info)
	conn, err := sql.Open(info.DBType, fmt.Sprintf("%s://%s:%s@%s:%s/%s?sslmode=disable", info.DBType, info.User, info.Password, info.Host, strconv.Itoa(info.Port), info.DBName))
	return conn, err
}

func (s *Service) GetSchemas(ctx context.Context, targetDBID int) ([]string, error) {
	conn, err := s.GetTargetConnection(ctx, targetDBID)
	if err != nil {
		return nil, err
	}

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

func (s *Service) GetTables(ctx context.Context, targetDBID int, schema string) ([]string, error) {
	conn, err := s.GetTargetConnection(ctx, targetDBID)
	if err != nil {
		return nil, err
	}

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

func (s *Service) GetTableDetails(ctx context.Context, targetDBID int, schema, tableName string) (*models.TableMetadata, error) {
	conn, err := s.GetTargetConnection(ctx, targetDBID)
	if err != nil {
		return nil, err
	}

	metadata := &models.TableMetadata{
		Schema:    schema,
		TableName: tableName,
	}

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

func (s *Service) GetTableData(ctx context.Context, targetDBID int, schema, tableName string, limit, offset int) ([]map[string]any, error) {
	conn, err := s.GetTargetConnection(ctx, targetDBID)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`SELECT * FROM "%s"."%s" LIMIT $1 OFFSET $2`, schema, tableName)
	datas, err := conn.QueryContext(ctx, query, limit, offset)
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
			rowMap[col] = values[i]
		}
		result = append(result, rowMap)
	}
	return result, nil
}

func (s *Service) CloseAllTargetConnections() {
}
