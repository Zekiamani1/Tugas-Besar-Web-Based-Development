package migration

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"wbd/internal/models"
	"wbd/internal/observability"
)

type sqlJobContextKey struct{}

func sqlJobAttrs(ctx context.Context) []any {
	job, ok := ctx.Value(sqlJobContextKey{}).(models.MigrationJob)
	if !ok {
		return nil
	}
	return []any{"job_id", job.ID, "migration_id", job.MigrationID, "database", job.TargetDBID, "actor", job.Actor, "attempt", job.Attempts}
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
type sqlRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func sqlStarted(ctx context.Context, query string, count int) time.Time {
	attrs := append(sqlJobAttrs(ctx), "event", "migration.internal_sql.start", "query", observability.SafeSQL(query), "parameter_count", count)
	slog.InfoContext(ctx, "migration internal SQL started", attrs...)
	return time.Now()
}

func sqlFinished(ctx context.Context, query string, count int, started time.Time, err error, rows int64) {
	status := "success"
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		status = "failed"
	}
	attrs := append(sqlJobAttrs(ctx), "event", "migration.internal_sql.result", "query", observability.SafeSQL(query), "parameter_count", count, "status", status, "duration_ms", time.Since(started).Milliseconds(), "rows", rows, "error", observability.SafeError(err), "sqlstate", observability.SQLState(err))
	slog.InfoContext(ctx, "migration internal SQL finished", attrs...)
}

func loggedExec(ctx context.Context, conn sqlExecutor, query string, args ...any) (sql.Result, error) {
	started := sqlStarted(ctx, query, len(args))
	result, err := conn.ExecContext(ctx, query, args...)
	var rows int64 = -1
	if err == nil {
		rows, _ = result.RowsAffected()
	}
	sqlFinished(ctx, query, len(args), started, err, rows)
	return result, err
}

type loggedRow struct {
	row     *sql.Row
	ctx     context.Context
	query   string
	count   int
	started time.Time
}

func (r loggedRow) Scan(dest ...any) error {
	err := r.row.Scan(dest...)
	rows := int64(0)
	if err == nil {
		rows = 1
	}
	sqlFinished(r.ctx, r.query, r.count, r.started, err, rows)
	return err
}

func loggedQueryRow(ctx context.Context, conn sqlRowQueryer, query string, args ...any) loggedRow {
	started := sqlStarted(ctx, query, len(args))
	return loggedRow{row: conn.QueryRowContext(ctx, query, args...), ctx: ctx, query: query, count: len(args), started: started}
}

func loggedQuery(ctx context.Context, conn sqlQueryer, query string, args ...any) (*sql.Rows, error) {
	started := sqlStarted(ctx, query, len(args))
	rows, err := conn.QueryContext(ctx, query, args...)
	sqlFinished(ctx, query, len(args), started, err, -1)
	return rows, err
}
