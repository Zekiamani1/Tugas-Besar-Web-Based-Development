package migration

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"wbd/internal/models"
)

type lockedLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *lockedLogBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }

func TestAsyncQueueAndWorker(t *testing.T) {
	dsn, rabbitURL := os.Getenv("MIGRATION_TEST_DSN"), os.Getenv("RABBITMQ_TEST_URL")
	if dsn == "" || rabbitURL == "" {
		t.Skip("requires disposable MIGRATION_TEST_DSN and RABBITMQ_TEST_URL")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("invalid test PostgreSQL URL")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("async_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	target, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	svc := NewService(db, targetProvider{conn: target})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	migrationID, err := svc.CreateDraft(ctx, 1, "CREATE TABLE async_example (id integer)")
	if err != nil {
		t.Fatal(err)
	}
	id, err := svc.SubmitJob(ctx, models.MigrationJob{MigrationID: migrationID, TargetDBID: 1, Actor: "alice", Action: "APPLY", Mode: "STANDARD", Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	queue := "async_" + schema
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = ch.QueueDelete(queue+".retry", false, false, false)
		_, _ = ch.QueueDelete(queue, false, false, false)
	}()
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 2)
	validate := svc.validateAsyncJob
	execute := svc.executeAsyncJob
	validateAny := func(context.Context, models.MigrationJob) error { return nil }
	go func() { done <- svc.RunPublisher(workerCtx, conn, queue) }()
	go func() { done <- svc.RunWorker(workerCtx, conn, queue, validate, execute) }()
	joined := false
	defer func() {
		stop()
		if !joined {
			<-done
			<-done
		}
	}()
	for {
		job, err := svc.FindJob(ctx, 1, migrationID, id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "SUCCEEDED" {
			break
		}
		if job.Status == "FAILED" || job.Status == "UNCERTAIN" {
			t.Fatalf("unexpected job result: %+v", job)
		}
		select {
		case <-ctx.Done():
			t.Fatal("job did not finish")
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !db.Migrator().HasTable("async_example") {
		t.Fatal("target SQL did not commit")
	}
	if err := svc.ProcessJob(ctx, id, validate, execute); err != nil {
		t.Fatal(err)
	}
	job, err := svc.FindJob(ctx, 1, migrationID, id)
	if err != nil || job.Attempts != 1 {
		t.Fatalf("duplicate executed: job=%+v err=%v", job, err)
	}
	if err := db.Model(&models.MigrationJob{}).Where("id=?", id).Update("status", "RUNNING").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.MigrationExecution{}).Where("id=?", job.ExecutionID).Update("status", StatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessJob(ctx, id, validate, execute); err != nil {
		t.Fatal(err)
	}
	job, err = svc.FindJob(ctx, 1, migrationID, id)
	if err != nil || job.Status != "SUCCEEDED" {
		t.Fatalf("commit evidence did not recover completed SQL: job=%+v err=%v", job, err)
	}
	var recovered models.MigrationExecution
	if err := db.First(&recovered, job.ExecutionID).Error; err != nil || recovered.Status != StatusApplied {
		t.Fatalf("history did not recover from target commit evidence: execution=%+v err=%v", recovered, err)
	}
	stop()
	<-done
	<-done
	joined = true
	t.Run("RabbitMQ TTL dead-letter retry", func(t *testing.T) {
		retryJobID, err := svc.SubmitJob(ctx, models.MigrationJob{MigrationID: migrationID, TargetDBID: 1, Actor: "alice", Action: ActionApply, Mode: "STANDARD", Revision: 1})
		if err != nil {
			t.Fatal(err)
		}
		retryQueue := "retry_" + schema
		var calls atomic.Int32
		retryExecutor := func(context.Context, models.MigrationJob) (JobResult, error) {
			if calls.Add(1) == 1 {
				return JobResult{}, ErrRetrySafe
			}
			return JobResult{}, nil
		}
		retryCtx, retryStop := context.WithCancel(ctx)
		retryDone := make(chan error, 2)
		go func() { retryDone <- svc.RunPublisher(retryCtx, conn, retryQueue) }()
		go func() { retryDone <- svc.RunWorker(retryCtx, conn, retryQueue, validateAny, retryExecutor) }()
		defer func() {
			retryStop()
			<-retryDone
			<-retryDone
			cleanup, e := conn.Channel()
			if e == nil {
				_, _ = cleanup.QueueDelete(retryQueue+".retry", false, false, false)
				_, _ = cleanup.QueueDelete(retryQueue, false, false, false)
				_ = cleanup.Close()
			}
		}()
		deadline := time.NewTimer(8 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			current, e := svc.FindJob(ctx, 1, migrationID, retryJobID)
			if e != nil {
				t.Fatal(e)
			}
			if current.Status == "SUCCEEDED" {
				if calls.Load() != 2 || current.Attempts != 2 {
					t.Fatalf("retry delivery count=%d attempts=%d", calls.Load(), current.Attempts)
				}
				break
			}
			if current.Status == "FAILED" {
				t.Fatalf("retry failed: %+v", current)
			}
			select {
			case <-deadline.C:
				t.Fatalf("RabbitMQ retry did not arrive: %+v", current)
			case <-ticker.C:
			}
		}
	})
	t.Run("bounded retry and restart recovery", func(t *testing.T) {
		j := models.MigrationJob{MigrationID: migrationID, TargetDBID: 20, Actor: "alice", Action: "APPLY", Mode: "STANDARD", Status: "QUEUED"}
		if err := db.Create(&j).Error; err != nil {
			t.Fatal(err)
		}
		retry := func(context.Context, models.MigrationJob) (JobResult, error) { return JobResult{}, ErrRetrySafe }
		for i := 0; i < 3; i++ {
			if err := db.Model(&j).Update("next_attempt_at", time.Now().UTC()).Error; err != nil {
				t.Fatal(err)
			}
			err := svc.ProcessJob(ctx, j.ID, validateAny, retry)
			if i < 2 && !errors.Is(err, ErrScheduledRetry) {
				t.Fatalf("attempt %d should schedule broker retry: %v", i+1, err)
			}
			if i == 2 && err != nil {
				t.Fatal(err)
			}
		}
		if err := db.First(&j, j.ID).Error; err != nil {
			t.Fatal(err)
		}
		if j.Status != "FAILED" || j.Attempts != 3 {
			t.Fatalf("unbounded retry: %+v", j)
		}
		noReplay := func(context.Context, models.MigrationJob) (JobResult, error) {
			t.Error("terminal job must not replay SQL")
			return JobResult{}, nil
		}
		if err := svc.ProcessJob(ctx, j.ID, validateAny, noReplay); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&j, j.ID).Error; err != nil {
			t.Fatal(err)
		}
		if j.Status != "FAILED" {
			t.Fatalf("terminal failed job was replayed: %s", j.Status)
		}
	})
	t.Run("same database serial and different database parallel", func(t *testing.T) {
		makeJob := func(dbID int) int {
			job := models.MigrationJob{MigrationID: migrationID, TargetDBID: dbID, Actor: "alice", Action: "APPLY", Mode: "STANDARD", Status: "QUEUED"}
			if err := db.Create(&job).Error; err != nil {
				t.Fatal(err)
			}
			return job.ID
		}
		first, second, other := makeJob(10), makeJob(10), makeJob(11)
		started, release := make(chan struct{}), make(chan struct{})
		finished := make(chan error, 1)
		hold := func(ctx context.Context, _ models.MigrationJob) (JobResult, error) {
			close(started)
			select {
			case <-release:
				return JobResult{}, nil
			case <-ctx.Done():
				return JobResult{}, ctx.Err()
			}
		}
		go func() { finished <- svc.ProcessJob(ctx, first, validateAny, hold) }()
		released := false
		defer func() {
			if !released {
				close(release)
				if err := <-finished; err != nil {
					t.Error(err)
				}
			}
		}()
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("first job did not start")
		}
		instant := func(context.Context, models.MigrationJob) (JobResult, error) { return JobResult{}, nil }
		wait := &lockWaitState{}
		waitingCtx := context.WithValue(ctx, lockWaitContextKey{}, wait)
		if err := svc.ProcessJob(waitingCtx, second, validateAny, instant); !errors.Is(err, ErrIntegrationPending) {
			t.Fatalf("same database was not locked: %v", err)
		}
		if wait.started.IsZero() {
			t.Fatal("lock wait start was not recorded")
		}
		if err := svc.ProcessJob(ctx, other, validateAny, instant); err != nil {
			t.Fatalf("different database blocked: %v", err)
		}
		close(release)
		released = true
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		if err := svc.ProcessJob(waitingCtx, second, validateAny, instant); err != nil {
			t.Fatal(err)
		}
		if !wait.started.IsZero() {
			t.Fatal("lock wait not cleared after acquisition")
		}
	})
}

func TestValidateBatchStatement(t *testing.T) {
	for _, tc := range []struct {
		name  string
		job   models.MigrationJob
		sql   string
		valid bool
	}{
		{"valid marker", models.MigrationJob{BatchTable: "public.items", BatchAttribute: "id"}, "UPDATE public.items SET value = value + 1 WHERE {{batch_predicate}}", true},
		{"missing marker", models.MigrationJob{BatchTable: "items", BatchAttribute: "id"}, "UPDATE items SET value = 1", false},
		{"invalid table", models.MigrationJob{BatchTable: "items; DROP TABLE users", BatchAttribute: "id"}, "UPDATE items SET value = 1 WHERE {{batch_predicate}}", false},
		{"invalid attribute", models.MigrationJob{BatchTable: "items", BatchAttribute: "id) OR TRUE --"}, "UPDATE items SET value = 1 WHERE {{batch_predicate}}", false},
		{"multiple markers", models.MigrationJob{BatchTable: "items", BatchAttribute: "id"}, "UPDATE items SET value = 1 WHERE {{batch_predicate}} OR {{batch_predicate}}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateBatchStatement(tc.job, tc.sql) == nil; got != tc.valid {
				t.Fatalf("valid=%t, want %t", got, tc.valid)
			}
		})
	}
}

func TestBatchMigrationCheckpointAndResume(t *testing.T) {
	dsn, rabbitURL := os.Getenv("MIGRATION_TEST_DSN"), os.Getenv("RABBITMQ_TEST_URL")
	if dsn == "" || rabbitURL == "" {
		t.Skip("requires disposable MIGRATION_TEST_DSN and RABBITMQ_TEST_URL")
	}
	logs := &lockedLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	defer slog.SetDefault(previous)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid test PostgreSQL URL")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("batch_async_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	target, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	svc := NewService(db, targetProvider{conn: target})
	if err := svc.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ExecContext(ctx, `CREATE TABLE batch_items(id BIGINT PRIMARY KEY, value INTEGER NOT NULL); INSERT INTO batch_items SELECT n*10,0 FROM generate_series(1,7) n`); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ExecContext(ctx, `CREATE FUNCTION reject_batch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id=40 THEN RAISE EXCEPTION 'retry this batch'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_batch BEFORE UPDATE ON batch_items FOR EACH ROW EXECUTE FUNCTION reject_batch()`); err != nil {
		t.Fatal(err)
	}
	migrationID, err := svc.CreateDraftDefinition(ctx, 1, models.MigrationDraft{SQLUp: `UPDATE batch_items SET value=value+1 WHERE {{batch_predicate}}`, SQLDown: `UPDATE batch_items SET value=value-1 WHERE {{batch_predicate}}`})
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := svc.SubmitJob(ctx, models.MigrationJob{MigrationID: migrationID, TargetDBID: 1, Actor: "alice", Action: ActionApply, Mode: "BATCH", BatchSize: 3, BatchTable: "batch_items", BatchAttribute: "id", Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	queue := "batch_" + schema
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = ch.QueueDelete(queue+".retry", false, false, false)
		_, _ = ch.QueueDelete(queue, false, false, false)
	}()
	_ = ch.Close()
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 2)
	go func() { done <- svc.RunPublisher(workerCtx, conn, queue) }()
	go func() { done <- svc.RunWorker(workerCtx, conn, queue, svc.validateAsyncJob, svc.executeAsyncJob) }()
	defer func() { stop(); <-done; <-done }()
	waitStatus := func(id int, want string) models.MigrationJob {
		t.Helper()
		for {
			job, e := svc.FindJob(ctx, 1, migrationID, id)
			if e != nil {
				t.Fatal(e)
			}
			if job.Status == want {
				return *job
			}
			if job.Status == "FAILED" || job.Status == "UNCERTAIN" {
				t.Fatalf("job became %s: %+v", job.Status, job)
			}
			select {
			case <-ctx.Done():
				t.Fatalf("waiting for %s, last job=%+v", want, job)
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	partial := waitStatus(jobID, StatusPartial)
	if output := logs.String(); !strings.Contains(output, "migration.batch.sql.start") || strings.Contains(output, `"cursor":`) {
		t.Fatalf("batch logs missing or contain cursor: %s", output)
	}
	if partial.CommittedBatches != 1 || partial.CommittedRows != 3 || partial.Cursor != 30 {
		t.Fatalf("wrong first checkpoint: %+v", partial)
	}
	var updated int
	if err := target.QueryRowContext(ctx, `SELECT count(*) FROM batch_items WHERE value=1`).Scan(&updated); err != nil || updated != 3 {
		t.Fatalf("first batch not isolated: updated=%d err=%v", updated, err)
	}
	if _, err := target.ExecContext(ctx, `DROP TRIGGER reject_batch ON batch_items; DROP FUNCTION reject_batch()`); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResumeJob(ctx, 1, migrationID, jobID, "alice"); err != nil {
		t.Fatal(err)
	}
	finished := waitStatus(jobID, "SUCCEEDED")
	if finished.CommittedBatches != 3 || finished.CommittedRows != 7 || finished.TotalRows != 7 || finished.Cursor != 70 {
		t.Fatalf("wrong resumed result: %+v", finished)
	}
	if err := target.QueryRowContext(ctx, `SELECT count(*) FROM batch_items WHERE value=1`).Scan(&updated); err != nil || updated != 7 {
		t.Fatalf("batch resume repeated or skipped rows: updated=%d err=%v", updated, err)
	}
	rollbackID, err := svc.SubmitJob(ctx, models.MigrationJob{MigrationID: migrationID, TargetDBID: 1, Actor: "alice", Action: ActionRollback, Mode: "BATCH", BatchSize: 3, BatchTable: "batch_items", BatchAttribute: "id", ExpectedExecutionID: int(finished.ExecutionID)})
	if err != nil {
		t.Fatal(err)
	}
	rolledBack := waitStatus(rollbackID, "SUCCEEDED")
	if rolledBack.CommittedBatches != 3 || rolledBack.CommittedRows != 7 {
		t.Fatalf("rollback batch progress: %+v", rolledBack)
	}
	if err := target.QueryRowContext(ctx, `SELECT count(*) FROM batch_items WHERE value=0`).Scan(&updated); err != nil || updated != 7 {
		t.Fatalf("batch rollback did not compensate all committed ranges: rows=%d err=%v", updated, err)
	}
	forwardID, err := svc.SubmitJob(ctx, models.MigrationJob{MigrationID: migrationID, TargetDBID: 1, Actor: "alice", Action: ActionForward, Mode: "BATCH", BatchSize: 3, BatchTable: "batch_items", BatchAttribute: "id", ExpectedExecutionID: int(rolledBack.ExecutionID)})
	if err != nil {
		t.Fatal(err)
	}
	forwarded := waitStatus(forwardID, "SUCCEEDED")
	if forwarded.CommittedBatches != 3 || forwarded.CommittedRows != 7 {
		t.Fatalf("forward batch progress: %+v", forwarded)
	}
	if err := target.QueryRowContext(ctx, `SELECT count(*) FROM batch_items WHERE value=1`).Scan(&updated); err != nil || updated != 7 {
		t.Fatalf("batch forward did not reapply all ranges: rows=%d err=%v", updated, err)
	}
	if _, err := target.ExecContext(ctx, `CREATE FUNCTION reject_compensation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id=40 AND NEW.value=0 THEN RAISE EXCEPTION 'retry compensation'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_compensation BEFORE UPDATE ON batch_items FOR EACH ROW EXECUTE FUNCTION reject_compensation()`); err != nil {
		t.Fatal(err)
	}
	partialRollbackID, err := svc.SubmitJob(ctx, models.MigrationJob{MigrationID: migrationID, TargetDBID: 1, Actor: "alice", Action: ActionRollback, Mode: "BATCH", BatchSize: 3, BatchTable: "batch_items", BatchAttribute: "id", ExpectedExecutionID: int(forwarded.ExecutionID)})
	if err != nil {
		t.Fatal(err)
	}
	partialRollback := waitStatus(partialRollbackID, StatusPartialReverted)
	if partialRollback.CommittedBatches != 1 || partialRollback.CommittedRows != 1 {
		t.Fatalf("partial compensation checkpoint not retained: %+v", partialRollback)
	}
	if err := target.QueryRowContext(ctx, `SELECT count(*) FROM batch_items WHERE value=0`).Scan(&updated); err != nil || updated != 1 {
		t.Fatalf("failed compensation did not preserve committed batch: rows=%d err=%v", updated, err)
	}
	if _, err := target.ExecContext(ctx, `DROP TRIGGER reject_compensation ON batch_items; DROP FUNCTION reject_compensation()`); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResumeJob(ctx, 1, migrationID, partialRollbackID, "alice"); err != nil {
		t.Fatal(err)
	}
	resumedRollback := waitStatus(partialRollbackID, "SUCCEEDED")
	if resumedRollback.CommittedBatches != 3 || resumedRollback.CommittedRows != 7 {
		t.Fatalf("resumed compensation progress: %+v", resumedRollback)
	}
	if err := target.QueryRowContext(ctx, `SELECT count(*) FROM batch_items WHERE value=0`).Scan(&updated); err != nil || updated != 7 {
		t.Fatalf("resumed compensation failed to finish: rows=%d err=%v", updated, err)
	}
}

func TestLargeBatchDatasetPerformance(t *testing.T) {
	if os.Getenv("MIGRATION_PERF_TEST") != "1" {
		t.Skip("set MIGRATION_PERF_TEST=1 to run the large-dataset performance test")
	}
	dsn, rabbitURL := os.Getenv("MIGRATION_TEST_DSN"), os.Getenv("RABBITMQ_TEST_URL")
	if dsn == "" || rabbitURL == "" {
		t.Fatal("requires disposable MIGRATION_TEST_DSN and RABBITMQ_TEST_URL")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid test PostgreSQL URL")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("large_batch_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	internal, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer internal.Close()
	target, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	svc := NewService(db, targetProvider{conn: target})
	if err := svc.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ExecContext(ctx, `CREATE TABLE large_batch_items(id BIGINT PRIMARY KEY, value INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	var rowCount int64
	var fullUpdate time.Duration
	for rowCount < 16_000_000 {
		nextCount := int64(1_000_000)
		if rowCount > 0 {
			nextCount = rowCount
		}
		if _, err := target.ExecContext(ctx, `INSERT INTO large_batch_items SELECT n,0 FROM generate_series($1::bigint,$2::bigint) n`, rowCount+1, rowCount+nextCount); err != nil {
			t.Fatal(err)
		}
		rowCount += nextCount
		started := time.Now()
		if _, err := target.ExecContext(ctx, `UPDATE large_batch_items SET value=value+1`); err != nil {
			t.Fatal(err)
		}
		fullUpdate = time.Since(started)
		if fullUpdate > time.Second {
			break
		}
	}
	t.Logf("full update baseline: rows=%d duration=%s", rowCount, fullUpdate)
	if fullUpdate <= time.Second {
		t.Fatalf("full-table UPDATE did not exceed one second with %d rows: %s", rowCount, fullUpdate)
	}
	if _, err := target.ExecContext(ctx, `UPDATE large_batch_items SET value=0`); err != nil {
		t.Fatal(err)
	}
	migrationID, err := svc.CreateDraftDefinition(ctx, 1, models.MigrationDraft{SQLUp: `UPDATE large_batch_items SET value=value+1 WHERE {{batch_predicate}}`, SQLDown: `UPDATE large_batch_items SET value=value-1 WHERE {{batch_predicate}}`})
	if err != nil {
		t.Fatal(err)
	}
	const batchSize = 10_000
	jobID, err := svc.SubmitJob(ctx, models.MigrationJob{MigrationID: migrationID, TargetDBID: 1, Actor: "perf-test", Action: ActionApply, Mode: "BATCH", BatchSize: batchSize, BatchTable: "large_batch_items", BatchAttribute: "id", Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	queue := "large_batch_" + schema
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 2)
	go func() { done <- svc.RunPublisher(workerCtx, conn, queue) }()
	go func() { done <- svc.RunMigrationWorker(workerCtx, conn, queue) }()
	defer func() {
		stop()
		<-done
		<-done
		ch, e := conn.Channel()
		if e == nil {
			_, _ = ch.QueueDelete(queue+".retry", false, false, false)
			_, _ = ch.QueueDelete(queue, false, false, false)
			_ = ch.Close()
		}
	}()
	for {
		job, err := svc.FindJob(ctx, 1, migrationID, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "SUCCEEDED" {
			wantBatches := (rowCount + batchSize - 1) / batchSize
			if job.CommittedRows != rowCount || job.CommittedBatches != wantBatches {
				t.Fatalf("batch result rows=%d batches=%d, want rows=%d batches=%d", job.CommittedRows, job.CommittedBatches, rowCount, wantBatches)
			}
			break
		}
		if job.Status == "FAILED" || job.Status == "UNCERTAIN" {
			t.Fatalf("large batch migration failed: %+v", job)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("large batch job timed out: %+v", job)
		case <-time.After(100 * time.Millisecond):
		}
	}
	var updated int64
	if err := target.QueryRowContext(ctx, `SELECT count(*) FROM large_batch_items WHERE value=1`).Scan(&updated); err != nil || updated != rowCount {
		t.Fatalf("batched dataset mismatch: updated=%d want=%d err=%v", updated, rowCount, err)
	}
}
