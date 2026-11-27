package observability

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	quotedSQL       = regexp.MustCompile("('(?:''|[^'])*'|\"(?:\"\"|[^\"])*\"|`[^`]*`)")
	numericSQL      = regexp.MustCompile(`\b[0-9]+(?:\.[0-9]+)?\b`)
	dollarQuotedSQL = regexp.MustCompile(`\$[a-zA-Z_0-9]*\$`)
)

func SafeSQL(query string) string {
	lower := strings.ToLower(query)
	if strings.Contains(query, "\\") || strings.Contains(query, "--") || strings.Contains(query, "/*") ||
		strings.Contains(query, "#") || strings.Count(query, "'")%2 != 0 ||
		strings.Count(query, "\"")%2 != 0 || strings.Count(query, "`")%2 != 0 ||
		dollarQuotedSQL.MatchString(query) || strings.Contains(lower, "password") || strings.Contains(lower, "secret") ||
		strings.Contains(lower, "credential") || strings.Contains(lower, "token") || strings.Contains(lower, "session") {
		return "[REDACTED]"
	}
	return numericSQL.ReplaceAllString(quotedSQL.ReplaceAllString(query, "[REDACTED]"), "[REDACTED]")
}

func SafeError(err error) string {
	if err == nil {
		return ""
	}
	return "database operation failed"
}

func SQLState(err error) string {
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		return state.SQLState()
	}
	return ""
}

type GormLogger struct{ level logger.LogLevel }

func NewGormLogger() logger.Interface { return GormLogger{level: logger.Info} }

// RegisterGormQueryStart logs before GORM executes a database operation.
// SQL is logged by Trace after execution, when GORM has built the statement.
func RegisterGormQueryStart(db *gorm.DB) error {
	start := func(tx *gorm.DB) {
		if tx.Error != nil || tx.DryRun {
			return
		}
		slog.InfoContext(tx.Statement.Context, "platform database query started", "event", "database.query.start", "actor", Actor(tx.Statement.Context), "database", "platform", "status", "started")
	}
	return errors.Join(
		db.Callback().Create().Before("gorm:create").Register("observability:query_start", start),
		db.Callback().Query().Before("gorm:query").Register("observability:query_start", start),
		db.Callback().Update().Before("gorm:update").Register("observability:query_start", start),
		db.Callback().Delete().Before("gorm:delete").Register("observability:query_start", start),
		db.Callback().Row().Before("gorm:row").Register("observability:query_start", start),
		db.Callback().Raw().Before("gorm:raw").Register("observability:query_start", start),
	)
}

func (l GormLogger) LogMode(level logger.LogLevel) logger.Interface { l.level = level; return l }
func (l GormLogger) Info(ctx context.Context, _ string, _ ...any) {
	if l.level >= logger.Info {
		slog.InfoContext(ctx, "database information", "event", "database.info")
	}
}
func (l GormLogger) Warn(ctx context.Context, _ string, _ ...any) {
	if l.level >= logger.Warn {
		slog.WarnContext(ctx, "database warning", "event", "database.warning")
	}
}
func (l GormLogger) Error(ctx context.Context, _ string, _ ...any) {
	if l.level >= logger.Error {
		slog.ErrorContext(ctx, "database error", "event", "database.error")
	}
}
func (l GormLogger) ParamsFilter(_ context.Context, query string, _ ...any) (string, []any) {
	return query, nil
}
func (l GormLogger) Trace(ctx context.Context, started time.Time, query func() (string, int64), err error) {
	if l.level == logger.Silent {
		return
	}
	sql, rows := query()
	status := "success"
	if err != nil {
		status = "failed"
	}
	slog.InfoContext(ctx, "platform database query completed", "event", "database.query.result", "actor", Actor(ctx), "database", "platform", "status", status, "query", SafeSQL(sql), "started_at", started.UTC(), "duration_ms", time.Since(started).Milliseconds(), "rows", rows, "error", SafeError(err), "sqlstate", SQLState(err))
}
