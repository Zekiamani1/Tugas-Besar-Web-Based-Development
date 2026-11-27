package observability

import (
	"context"
	"database/sql"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	jobEvents   metric.Int64Counter
	jobDuration metric.Float64Histogram
	queueWait   metric.Float64Histogram
	lockWait    metric.Float64Histogram
	lockAttempt metric.Float64Histogram
	batchRows   metric.Int64Counter
	batches     metric.Int64Counter
)

func initMetrics() error {
	meter := otel.Meter("wbd/observability")
	var err error
	if jobEvents, err = meter.Int64Counter("wbd.migration.job.events"); err != nil {
		return err
	}
	buckets := metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 1800)
	if jobDuration, err = meter.Float64Histogram("wbd.migration.job.duration", metric.WithUnit("s"), buckets); err != nil {
		return err
	}
	if queueWait, err = meter.Float64Histogram("wbd.migration.queue.wait", metric.WithUnit("s"), buckets); err != nil {
		return err
	}
	if lockAttempt, err = meter.Float64Histogram("wbd.migration.lock.attempt.duration", metric.WithUnit("s"), buckets); err != nil {
		return err
	}
	if lockWait, err = meter.Float64Histogram("wbd.migration.lock.wait.duration", metric.WithUnit("s"), buckets); err != nil {
		return err
	}
	if batchRows, err = meter.Int64Counter("wbd.migration.batch.rows"); err != nil {
		return err
	}
	if batches, err = meter.Int64Counter("wbd.migration.batches"); err != nil {
		return err
	}
	return nil
}

func JobEvent(ctx context.Context, event, status string) {
	if jobEvents != nil {
		jobEvents.Add(ctx, 1, metric.WithAttributes(attribute.String("event", event), attribute.String("status", status)))
	}
}

func JobDuration(ctx context.Context, status string, duration time.Duration) {
	if jobDuration != nil {
		jobDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("status", status)))
	}
}

func QueueWait(ctx context.Context, duration time.Duration) {
	if queueWait != nil && duration >= 0 {
		queueWait.Record(ctx, duration.Seconds())
	}
}

func LockWait(ctx context.Context, duration time.Duration) {
	if lockWait != nil {
		lockWait.Record(ctx, duration.Seconds())
	}
}

func LockAttempt(ctx context.Context, duration time.Duration) {
	if lockAttempt != nil {
		lockAttempt.Record(ctx, duration.Seconds())
	}
}

func BatchRows(ctx context.Context, action string, rows int64) {
	if batches != nil {
		batches.Add(ctx, 1, metric.WithAttributes(attribute.String("action", action)))
	}
	if batchRows != nil && rows > 0 {
		batchRows.Add(ctx, rows, metric.WithAttributes(attribute.String("action", action)))
	}
}

func RegisterDatabaseMetrics(pool *sql.DB) error {
	meter := otel.Meter("wbd/observability")
	connections, err := meter.Int64ObservableGauge("wbd.database.connections")
	if err != nil {
		return err
	}
	waits, err := meter.Int64ObservableCounter("wbd.database.connection.waits")
	if err != nil {
		return err
	}
	waitDuration, err := meter.Float64ObservableCounter("wbd.database.connection.wait.duration", metric.WithUnit("s"))
	if err != nil {
		return err
	}
	jobs, err := meter.Int64ObservableGauge("wbd.migration.jobs")
	if err != nil {
		return err
	}
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		stats := pool.Stats()
		observer.ObserveInt64(connections, int64(stats.InUse), metric.WithAttributes(attribute.String("state", "active")))
		observer.ObserveInt64(connections, int64(stats.Idle), metric.WithAttributes(attribute.String("state", "idle")))
		observer.ObserveInt64(waits, stats.WaitCount)
		observer.ObserveFloat64(waitDuration, stats.WaitDuration.Seconds())
		rows, err := pool.QueryContext(ctx, "SELECT status, count(*) FROM migration_jobs GROUP BY status")
		if err != nil {
			return nil
		}
		defer rows.Close()
		for rows.Next() {
			var status string
			var count int64
			if err := rows.Scan(&status, &count); err != nil {
				return err
			}
			switch status {
			case "QUEUED", "RUNNING", "SUCCEEDED", "FAILED", "PARTIAL", "PARTIALLY_REVERTED", "UNCERTAIN":
				observer.ObserveInt64(jobs, count, metric.WithAttributes(attribute.String("status", status)))
			}
		}
		return rows.Err()
	}, connections, waits, waitDuration, jobs)
	return err
}
