package migration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"wbd/internal/models"
	"wbd/internal/observability"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"gorm.io/gorm"
)

func (s *Service) RunWorker(ctx context.Context, conn *amqp.Connection, queue string, validate JobValidator, execute JobExecutor) error {
	if validate == nil || execute == nil {
		return ErrIntegrationPending
	}
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		return err
	}
	if err := declareRetryQueue(ch, queue); err != nil {
		return err
	}
	if err := ch.Confirm(false); err != nil {
		return err
	}
	if err := ch.Qos(4, 0, false); err != nil {
		return err
	}
	messages, err := ch.Consume(queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	var workers sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	workerErrors := make(chan error, 1)
	for {
		select {
		case <-ctx.Done():
			workers.Wait()
			return nil
		case err := <-workerErrors:
			workers.Wait()
			return err
		case message, ok := <-messages:
			if !ok {
				workers.Wait()
				return errors.New("consumer disconnected")
			}
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				_ = message.Nack(false, true)
				workers.Wait()
				return nil
			}
			workers.Add(1)
			go func(delivery amqp.Delivery) {
				defer workers.Done()
				defer func() { <-semaphore }()
				if err := s.handleWorkerDelivery(ctx, ch, queue, delivery, validate, execute); err != nil {
					select {
					case workerErrors <- err:
					default:
					}
				}
			}(message)
		}
	}
}

func (s *Service) handleWorkerDelivery(ctx context.Context, ch *amqp.Channel, queue string, message amqp.Delivery, validate JobValidator, execute JobExecutor) error {
	var payload struct {
		ID int `json:"job_id"`
	}
	if json.Unmarshal(message.Body, &payload) != nil || payload.ID <= 0 {
		return message.Reject(false)
	}
	parent, _ := message.Headers["traceparent"].(string)
	jobCtx := propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": parent})
	wait := &lockWaitState{}
	if value, ok := message.Headers["lock_wait_since"].(string); ok {
		if started, err := time.Parse(time.RFC3339Nano, value); err == nil && !started.After(time.Now()) {
			wait.started = started
		}
	}
	jobCtx = context.WithValue(jobCtx, lockWaitContextKey{}, wait)
	jobCtx, span := otel.Tracer("wbd/async").Start(jobCtx, "worker.process")
	err := s.ProcessJob(jobCtx, payload.ID, validate, execute)
	if err != nil {
		span.RecordError(errors.New("job processing failed"))
		span.SetStatus(codes.Error, "job processing failed")
	}
	span.End()
	if errors.Is(err, ErrIntegrationPending) || errors.Is(err, ErrScheduledRetry) {
		count := retryCount(message.Headers)
		if errors.Is(err, ErrScheduledRetry) {
			count++
		}
		if publishErr := publishDelayedRetry(ctx, ch, queue, message.Body, parent, count, wait.started); publishErr != nil {
			_ = message.Nack(false, true)
			return publishErr
		}
		return message.Ack(false)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return message.Reject(false)
	}
	if err != nil {
		_ = message.Nack(false, true)
		return err
	}
	return message.Ack(false)
}

var ErrScheduledRetry = errors.New("job scheduled for RabbitMQ retry")

func declareRetryQueue(ch *amqp.Channel, queue string) error {
	_, err := ch.QueueDeclare(queue+".retry", true, false, false, false, amqp.Table{
		"x-message-ttl": int32(2000), "x-dead-letter-exchange": "", "x-dead-letter-routing-key": queue,
	})
	return err
}

func retryCount(headers amqp.Table) int {
	switch value := headers["x-retry-count"].(type) {
	case int32:
		return int(value)
	case int64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

type lockWaitContextKey struct{}
type lockWaitState struct{ started time.Time }

func publishDelayedRetry(ctx context.Context, ch *amqp.Channel, queue string, body []byte, traceParent string, count int, lockWaitStarted time.Time) error {
	headers := amqp.Table{"traceparent": traceParent, "x-retry-count": int32(count)}
	if !lockWaitStarted.IsZero() {
		headers["lock_wait_since"] = lockWaitStarted.UTC().Format(time.RFC3339Nano)
	}
	confirm, err := ch.PublishWithDeferredConfirmWithContext(ctx, "", queue+".retry", true, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body,
		Headers: headers,
	})
	if err != nil {
		return err
	}
	if confirm == nil {
		return errors.New("RabbitMQ retry publish confirm missing")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	acked, err := confirm.WaitContext(waitCtx)
	if err != nil {
		return err
	}
	if !acked {
		return errors.New("RabbitMQ retry publish was rejected")
	}
	return nil
}

func (s *Service) RunMigrationWorker(ctx context.Context, conn *amqp.Connection, queue string) error {
	return s.RunWorker(ctx, conn, queue, s.validateAsyncJob, s.executeAsyncJob)
}

func (s *Service) validateAsyncJob(ctx context.Context, job models.MigrationJob) error {
	if job.ApprovalID != "" && job.Status == "QUEUED" && job.Attempts == 0 {
		if _, err := s.GetApprovedMigration(ctx, job.ApprovalID); err != nil {
			return err
		}
	}
	if s.jobValidator != nil {
		if err := s.jobValidator(ctx, job); err != nil {
			return err
		}
	}
	var definition models.Migration
	if err := s.internalDB.WithContext(ctx).Where("id = ? AND target_db_id = ?", job.MigrationID, job.TargetDBID).First(&definition).Error; err != nil {
		return err
	}
	if job.Action == ActionApply && definition.Revision != job.Revision {
		return ErrStaleRevision
	}
	if job.Mode == "BATCH" {
		query := definition.SQLUp
		if job.Action == ActionRollback {
			query = definition.SQLDown
		}
		if err := validateBatchStatement(job, query); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ProcessJob(ctx context.Context, id int, validate JobValidator, execute JobExecutor) error {
	var job models.MigrationJob
	if err := s.internalDB.WithContext(ctx).First(&job, id).Error; err != nil {
		return err
	}
	ctx = context.WithValue(ctx, sqlJobContextKey{}, job)
	pool, err := s.internalDB.DB()
	if err != nil {
		return err
	}
	lock, err := pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	var acquired bool
	lockStarted := time.Now()
	lockCtx, lockSpan := otel.Tracer("wbd/async").Start(ctx, "database.lock")
	err = loggedQueryRow(lockCtx, lock, "SELECT pg_try_advisory_lock(3111, $1::integer)", job.TargetDBID).Scan(&acquired)
	observability.LockAttempt(lockCtx, time.Since(lockStarted))
	if err != nil {
		lockSpan.SetStatus(codes.Error, "lock failed")
	}
	lockSpan.End()
	if err != nil {
		return err
	}
	if !acquired {
		if wait, ok := ctx.Value(lockWaitContextKey{}).(*lockWaitState); ok && wait.started.IsZero() {
			wait.started = lockStarted
		}
		return ErrIntegrationPending
	}
	waitStarted := lockStarted
	if wait, ok := ctx.Value(lockWaitContextKey{}).(*lockWaitState); ok && !wait.started.IsZero() {
		waitStarted = wait.started
		wait.started = time.Time{}
	}
	observability.LockWait(ctx, time.Since(waitStarted))
	jobEvent(ctx, "job.lock.acquired", job)
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		var unlocked bool
		if err := loggedQueryRow(unlockCtx, lock, "SELECT pg_advisory_unlock(3111, $1::integer)", job.TargetDBID).Scan(&unlocked); err != nil || !unlocked {
			_ = lock.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if err := s.internalDB.WithContext(ctx).First(&job, id).Error; err != nil {
		return err
	}
	wasQueued := job.Status == "QUEUED"
	if job.Status == "RUNNING" || job.Status == "UNCERTAIN" {
	} else if job.Status != "QUEUED" {
		return nil
	}
	if job.NextAttemptAt.After(time.Now().UTC()) {
		return ErrIntegrationPending
	}
	var blocked int64
	blockers := s.internalDB.WithContext(ctx).Model(&models.MigrationJob{}).Where("target_db_id = ? AND id <> ? AND status IN ?", job.TargetDBID, id, []string{"RUNNING", "PARTIAL", "PARTIALLY_REVERTED", "UNCERTAIN"})
	if job.Action == ActionRollback && job.Mode == "BATCH" && job.ExpectedExecutionID > 0 {
		blockers = blockers.Where("NOT (migration_id = ? AND execution_id = ? AND status = ? AND mode = ?)", job.MigrationID, job.ExpectedExecutionID, StatusPartial, "BATCH")
	}
	if err := blockers.Count(&blocked).Error; err != nil {
		return err
	}
	if blocked > 0 {
		return ErrIntegrationPending
	}
	if err := validate(ctx, job); err != nil {
		if errors.Is(err, ErrIntegrationPending) {
			jobEvent(ctx, "job.waiting", job)
			return err
		}
		updates := map[string]any{"status": "FAILED", "attempts": job.Attempts + 1, "error_message": err.Error()}
		retryScheduled := false
		if errors.Is(err, ErrRetrySafe) && job.Attempts+1 < 3 {
			updates["status"] = "QUEUED"
			updates["published_at"] = time.Now().UTC()
			updates["next_attempt_at"] = time.Now().UTC().Add(time.Duration((job.Attempts+1)*2) * time.Second)
			retryScheduled = true
		}
		job.Status = updates["status"].(string)
		jobEvent(ctx, "job.validation.result", job)
		if err := s.internalDB.WithContext(ctx).Model(&job).Updates(updates).Error; err != nil {
			return err
		}
		if retryScheduled {
			return ErrScheduledRetry
		}
		return nil
	}
	if wasQueued {
		observability.QueueWait(ctx, time.Since(job.CreatedAt))
		job.Attempts++
	}
	job.Status = "RUNNING"
	if err := s.internalDB.WithContext(ctx).Model(&job).Updates(map[string]any{"status": job.Status, "attempts": job.Attempts, "error_message": ""}).Error; err != nil {
		return err
	}
	jobEvent(ctx, "job.running", job)
	jobStarted := time.Now()
	execCtx, span := otel.Tracer("wbd/async").Start(ctx, "migration.execute")
	result, execErr := execute(execCtx, job)
	if execErr != nil {
		span.RecordError(errors.New("migration execution failed"))
		span.SetStatus(codes.Error, "migration execution failed")
	}
	span.End()
	status := result.Status
	if status == "" {
		status = "SUCCEEDED"
		if execErr != nil {
			status = "FAILED"
		}
	}
	if ctx.Err() != nil || errors.Is(execErr, ErrTargetCommitUncertain) || errors.Is(execErr, ErrHistoryUpdateUncertain) {
		status = "UNCERTAIN"
	}
	if execErr != nil && status == "SUCCEEDED" {
		status = "FAILED"
	}
	updates := map[string]any{"status": status, "error_message": ""}
	if execErr != nil {
		updates["error_message"] = execErr.Error()
	}
	if status != "SUCCEEDED" && status != "FAILED" && status != StatusPartial && status != StatusPartialReverted && status != "UNCERTAIN" {
		updates["status"] = "UNCERTAIN"
	}
	retryScheduled := false
	if errors.Is(execErr, ErrRetrySafe) && result.Status == "" && status != "UNCERTAIN" && job.Attempts < 3 {
		updates["status"] = "QUEUED"
		updates["published_at"] = time.Now().UTC()
		updates["next_attempt_at"] = time.Now().UTC().Add(time.Duration(job.Attempts*2) * time.Second)
		retryScheduled = true
	}
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if errors.Is(execErr, ErrIntegrationPending) {
		updates["status"] = "QUEUED"
		if wasQueued {
			updates["attempts"] = job.Attempts - 1
		}
		updates["published_at"] = time.Now().UTC()
		updates["next_attempt_at"] = time.Now().UTC().Add(2 * time.Second)
		retryScheduled = true
	}
	if result.ExecutionID > 0 {
		updates["execution_id"] = result.ExecutionID
	}
	if result.Status != "" || result.TotalRows > 0 {
		updates["committed_batches"] = result.CommittedBatches
		updates["committed_rows"] = result.CommittedRows
		updates["total_rows"] = result.TotalRows
		updates["cursor"] = result.Cursor
	}
	defer cancel()
	if err := s.internalDB.WithContext(saveCtx).Model(&job).Updates(updates).Error; err != nil {
		return err
	}
	job.Status = updates["status"].(string)
	observability.JobDuration(ctx, job.Status, time.Since(jobStarted))
	event := "job.finished"
	if job.Status == "QUEUED" {
		event = "job.retry"
	}
	jobEvent(ctx, event, job)
	if retryScheduled {
		return ErrScheduledRetry
	}
	if status == "UNCERTAIN" {
		return ErrIntegrationPending
	}
	return nil
}

func (s *Service) ResumeJob(ctx context.Context, dbID, migrationID, jobID int, actor string) error {
	result := s.internalDB.WithContext(ctx).Model(&models.MigrationJob{}).
		Where("id = ? AND target_db_id = ? AND migration_id = ? AND status IN ?", jobID, dbID, migrationID, []string{StatusPartial, StatusPartialReverted}).
		Updates(map[string]any{"status": "QUEUED", "published_at": nil, "next_attempt_at": time.Now().UTC(), "attempts": 0, "error_message": "", "resumed_by": actor})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrJobConflict
	}
	return nil
}

func (s *Service) executeAsyncJob(ctx context.Context, job models.MigrationJob) (JobResult, error) {
	if job.Mode != "BATCH" {
		return s.runStandardJob(ctx, job)
	}
	execution, version, err := s.reserveBatchExecution(ctx, job)
	if errors.Is(err, ErrDatabaseBusy) {
		return JobResult{}, ErrRetrySafe
	}
	if err != nil {
		return JobResult{}, err
	}
	return s.runBatchJob(ctx, job, execution, version)
}

func (s *Service) reserveBatchExecution(ctx context.Context, job models.MigrationJob) (models.MigrationExecution, int, error) {
	if job.ExecutionID > 0 {
		var execution models.MigrationExecution
		if err := s.internalDB.WithContext(ctx).First(&execution, job.ExecutionID).Error; err != nil {
			return execution, 0, err
		}
		return execution, job.PlannedVersion, nil
	}
	if job.Action == ActionApply {
		return s.reserveApplyRevisionForJob(ctx, job.ID, job.TargetDBID, job.MigrationID, job.Revision, job.Actor)
	}
	return s.reserveReversalAttempt(ctx, job.ID, job.TargetDBID, job.MigrationID, job.ExpectedExecutionID, job.Action, job.Actor)
}

func (s *Service) runStandardJob(ctx context.Context, job models.MigrationJob) (JobResult, error) {
	conn, err := s.dbMgmt.GetTargetConnection(ctx, job.TargetDBID)
	if err != nil {
		if job.ExecutionID > 0 {
			return JobResult{}, fmt.Errorf("%w: target connection unavailable while checking commit", ErrTargetCommitUncertain)
		}
		return JobResult{}, ErrRetrySafe
	}
	if err := ensureCommitLedger(ctx, conn); err != nil {
		if job.ExecutionID > 0 {
			return JobResult{}, fmt.Errorf("%w: commit evidence unavailable", ErrTargetCommitUncertain)
		}
		return JobResult{}, ErrRetrySafe
	}
	if committedID, ok, err := findCommitEvidence(ctx, conn, job.ID); err != nil {
		return JobResult{}, fmt.Errorf("%w: commit evidence unavailable", ErrTargetCommitUncertain)
	} else if ok {
		var committed models.MigrationExecution
		if err := s.internalDB.WithContext(ctx).First(&committed, committedID).Error; err != nil {
			return JobResult{}, err
		}
		if err := s.finishAttempt(ctx, &committed, StatusApplied, committed.Version, nil); err != nil {
			return JobResult{}, fmt.Errorf("%w: %v", ErrHistoryUpdateUncertain, err)
		}
		return JobResult{Status: "SUCCEEDED", ExecutionID: int64(committed.ID)}, nil
	}
	if job.ExecutionID > 0 {
		var previous models.MigrationExecution
		if err := s.internalDB.WithContext(ctx).First(&previous, job.ExecutionID).Error; err != nil {
			return JobResult{}, err
		}
		if previous.Status == StatusRunning || previous.Status == StatusUncertain {
			if err := s.finishAsyncExecution(ctx, previous, StatusFailed, 0, errors.New("target ledger confirms the transaction did not commit")); err != nil {
				return JobResult{}, err
			}
			if err := s.internalDB.WithContext(ctx).Model(&models.MigrationJob{}).Where("id = ?", job.ID).Updates(map[string]any{"execution_id": 0, "planned_version": 0}).Error; err != nil {
				return JobResult{}, err
			}
		}
	}
	executionRecord, err := s.ExecuteAsyncJob(ctx, job)
	if errors.Is(err, ErrDatabaseBusy) {
		return JobResult{}, ErrRetrySafe
	}
	if err != nil {
		return JobResult{}, err
	}
	if executionRecord == nil {
		return JobResult{}, errors.New("executor returned no migration execution")
	}
	return JobResult{Status: "SUCCEEDED", ExecutionID: int64(executionRecord.ID)}, nil
}

func ensureCommitLedger(ctx context.Context, conn interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) error {
	_, err := loggedExec(ctx, conn, `CREATE TABLE IF NOT EXISTS wbd_migration_job_commits (job_id BIGINT PRIMARY KEY, execution_id BIGINT NOT NULL, status TEXT NOT NULL, committed_at TIMESTAMPTZ NOT NULL)`)
	return err
}

func commitEvidence(ctx context.Context, conn interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, jobID int, executionID int) (bool, error) {
	var stored int64
	err := loggedQueryRow(ctx, conn, `SELECT execution_id FROM wbd_migration_job_commits WHERE job_id = $1 AND status = 'COMMITTED'`, jobID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return stored == int64(executionID), err
}

func findCommitEvidence(ctx context.Context, conn interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, jobID int) (int, bool, error) {
	var executionID int
	err := loggedQueryRow(ctx, conn, `SELECT execution_id FROM wbd_migration_job_commits WHERE job_id=$1 AND status='COMMITTED'`, jobID).Scan(&executionID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return executionID, err == nil, err
}

func (s *Service) finishAsyncExecution(ctx context.Context, execution models.MigrationExecution, status string, version int, cause error) error {
	return s.internalDB.WithContext(context.WithoutCancel(ctx)).Model(&models.MigrationExecution{}).
		Where("id = ? AND status IN ?", execution.ID, []string{StatusRunning, StatusUncertain, StatusPartial, StatusPartialReverted}).
		Updates(map[string]any{"status": status, "version": version, "finished_at": time.Now().UTC(), "error": errorString(cause)}).Error
}

func (s *Service) failAsyncExecution(ctx context.Context, execution models.MigrationExecution, cause error) error {
	if err := s.finishAsyncExecution(ctx, execution, StatusFailed, 0, cause); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

const (
	batchCheckpointDDL = `CREATE TABLE IF NOT EXISTS wbd_migration_batch_checkpoints (
		job_id BIGINT PRIMARY KEY, execution_id BIGINT NOT NULL, source_job_id BIGINT NOT NULL,
		cursor BIGINT NOT NULL DEFAULT 0, total_rows BIGINT NOT NULL DEFAULT 0,
		committed_batches BIGINT NOT NULL DEFAULT 0, committed_rows BIGINT NOT NULL DEFAULT 0,
		complete BOOLEAN NOT NULL DEFAULT FALSE, status TEXT NOT NULL DEFAULT 'RUNNING', error TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`
	batchScopeDDL   = `CREATE TABLE IF NOT EXISTS wbd_migration_batch_scope (job_id BIGINT NOT NULL, attribute_value BIGINT NOT NULL, PRIMARY KEY(job_id, attribute_value))`
	batchHistoryDDL = `CREATE TABLE IF NOT EXISTS wbd_migration_batch_history (job_id BIGINT NOT NULL, batch_number BIGINT NOT NULL, cursor_start BIGINT NOT NULL, cursor_end BIGINT NOT NULL, committed_rows BIGINT NOT NULL, reverted BOOLEAN NOT NULL DEFAULT FALSE, PRIMARY KEY(job_id, batch_number))`
)

type batchCheckpoint struct {
	Cursor, TotalRows, CommittedBatches, CommittedRows int64
	Complete                                           bool
}

func (s *Service) runBatchJob(ctx context.Context, job models.MigrationJob, execution models.MigrationExecution, version int) (JobResult, error) {
	conn, err := s.dbMgmt.GetTargetConnection(ctx, job.TargetDBID)
	if err != nil {
		return JobResult{}, s.failAsyncExecution(ctx, execution, err)
	}
	if _, err := loggedExec(ctx, conn, batchCheckpointDDL); err != nil {
		return JobResult{}, s.failAsyncExecution(ctx, execution, err)
	}
	if _, err := loggedExec(ctx, conn, batchScopeDDL); err != nil {
		return JobResult{}, s.failAsyncExecution(ctx, execution, err)
	}
	if _, err := loggedExec(ctx, conn, batchHistoryDDL); err != nil {
		return JobResult{}, s.failAsyncExecution(ctx, execution, err)
	}
	if err := s.initializeBatchCheckpoint(ctx, conn, job, execution); err != nil {
		return JobResult{}, s.failAsyncExecution(ctx, execution, err)
	}
	if job.Action != ActionApply {
		return s.runBatchCompensation(ctx, conn, job, execution, version)
	}
	return s.runBatchApply(ctx, conn, job, execution, version)
}

func (s *Service) initializeBatchCheckpoint(ctx context.Context, conn *sql.DB, job models.MigrationJob, execution models.MigrationExecution) error {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing int
	err = loggedQueryRow(ctx, tx, `SELECT 1 FROM wbd_migration_batch_checkpoints WHERE job_id = $1`, job.ID).Scan(&existing)
	if err == nil {
		var storedExecution, committed int64
		if err := loggedQueryRow(ctx, tx, `SELECT execution_id,committed_batches FROM wbd_migration_batch_checkpoints WHERE job_id=$1 FOR UPDATE`, job.ID).Scan(&storedExecution, &committed); err != nil {
			return err
		}
		if storedExecution != int64(execution.ID) {
			if committed != 0 {
				return ErrTargetCommitUncertain
			}
			if _, err := loggedExec(ctx, tx, `UPDATE wbd_migration_batch_checkpoints SET execution_id=$2,status='RUNNING',error='',complete=FALSE,updated_at=now() WHERE job_id=$1`, job.ID, execution.ID); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	table, attr := batchTableSQL(job), batchAttributeSQL(job)
	var total int64
	var minID, maxID sql.NullInt64
	if err := loggedQueryRow(ctx, tx, `SELECT count(`+attr+`), min(`+attr+`::bigint), max(`+attr+`::bigint) FROM `+table+` WHERE `+attr+` IS NOT NULL`).Scan(&total, &minID, &maxID); err != nil {
		return err
	}
	cursor := int64(-1)
	if minID.Valid {
		cursor = minID.Int64 - 1
	}
	sourceJobID := int64(job.ID)
	if job.Action != ActionApply {
		var sourceExecution models.MigrationExecution
		if err := s.internalDB.WithContext(ctx).First(&sourceExecution, job.ExpectedExecutionID).Error; err != nil {
			return err
		}
		for depth := 0; sourceExecution.Action != ActionApply && sourceExecution.RevertsExecutionID != nil && depth < 64; depth++ {
			nextExecutionID := *sourceExecution.RevertsExecutionID
			sourceExecution = models.MigrationExecution{}
			if err := s.internalDB.WithContext(ctx).First(&sourceExecution, nextExecutionID).Error; err != nil {
				return err
			}
		}
		if sourceExecution.Action != ActionApply {
			return ErrInvalidPosition
		}
		sourceID := int64(sourceExecution.ID)
		if err := s.internalDB.WithContext(ctx).Model(&models.MigrationJob{}).Where("execution_id = ?", sourceID).Select("id").Scan(&job.SourceJobID).Error; err != nil {
			return err
		}
		if job.SourceJobID <= 0 {
			return ErrInvalidPosition
		}
		var sourceJob models.MigrationJob
		if err := s.internalDB.WithContext(ctx).First(&sourceJob, job.SourceJobID).Error; err != nil {
			return err
		}
		if sourceJob.Mode != "BATCH" || sourceJob.BatchTable != job.BatchTable || sourceJob.BatchAttribute != job.BatchAttribute {
			return ErrInvalidJob
		}
		sourceJobID = int64(job.SourceJobID)
		var sourceCheckpoint batchCheckpoint
		if err := loggedQueryRow(ctx, tx, `SELECT cursor, total_rows, committed_batches, committed_rows, complete FROM wbd_migration_batch_checkpoints WHERE job_id = $1`, job.SourceJobID).Scan(&sourceCheckpoint.Cursor, &sourceCheckpoint.TotalRows, &sourceCheckpoint.CommittedBatches, &sourceCheckpoint.CommittedRows, &sourceCheckpoint.Complete); err != nil {
			return err
		}
		total = sourceCheckpoint.TotalRows
		cursor = 0
	}
	_, err = loggedExec(ctx, tx, `INSERT INTO wbd_migration_batch_checkpoints(job_id, execution_id, source_job_id, cursor, total_rows) VALUES ($1,$2,$3,$4,$5)`, job.ID, execution.ID, sourceJobID, cursor, total)
	if err != nil {
		return err
	}
	if job.Action == ActionApply {
		if _, err := loggedExec(ctx, tx, `INSERT INTO wbd_migration_batch_scope(job_id, attribute_value) SELECT $1, `+attr+`::bigint FROM `+table+` WHERE `+attr+` IS NOT NULL`, job.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) runBatchApply(ctx context.Context, conn *sql.DB, job models.MigrationJob, execution models.MigrationExecution, version int) (JobResult, error) {
	for {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return s.batchFailure(ctx, conn, job, execution, err)
		}
		cp, err := loadBatchCheckpoint(ctx, tx, job.ID)
		if err != nil {
			_ = tx.Rollback()
			return s.batchFailure(ctx, conn, job, execution, err)
		}
		if cp.Complete {
			_ = tx.Rollback()
			return s.completeBatchJob(ctx, job, execution, version, cp)
		}
		ids, err := nextBatchIDs(ctx, tx, job.ID, cp.Cursor, job.BatchSize)
		if err != nil {
			_ = tx.Rollback()
			return s.batchFailure(ctx, conn, job, execution, err)
		}
		if len(ids) == 0 {
			if _, err := loggedExec(ctx, tx, `UPDATE wbd_migration_batch_checkpoints SET complete=TRUE, status='SUCCEEDED', updated_at=now() WHERE job_id=$1`, job.ID); err != nil {
				_ = tx.Rollback()
				return s.batchFailure(ctx, conn, job, execution, err)
			}
			if err := tx.Commit(); err != nil {
				return s.batchFailure(ctx, conn, job, execution, err)
			}
			return s.completeBatchJob(ctx, job, execution, version, cp)
		}
		end := ids[len(ids)-1]
		batchSQL := batchSQLForRange(job, execution.SQLSnapshot, cp.Cursor, end, job.ID)
		started := time.Now()
		queryCtx, span := otel.Tracer("wbd/async").Start(ctx, "batch.query")
		attrs := traceLogAttrs(queryCtx)
		attrs = append(attrs, "event", "migration.batch.sql.start", "job_id", job.ID, "execution_id", execution.ID, "migration_id", job.MigrationID, "database", job.TargetDBID, "actor", job.Actor, "status", job.Status, "attempt", job.Attempts, "batch", cp.CommittedBatches+1, "query", observability.SafeSQL(batchSQL))
		slog.InfoContext(queryCtx, "batch SQL started", attrs...)
		result, execErr := tx.ExecContext(queryCtx, batchSQL)
		if execErr != nil {
			span.RecordError(errors.New("database query failed"))
			span.SetStatus(codes.Error, "batch SQL failed")
		}
		span.End()
		rows := int64(0)
		if execErr == nil {
			rows, _ = result.RowsAffected()
		}
		attrs = traceLogAttrs(queryCtx)
		attrs = append(attrs, "event", "migration.batch.sql.result", "job_id", job.ID, "execution_id", execution.ID, "migration_id", job.MigrationID, "database", job.TargetDBID, "actor", job.Actor, "status", job.Status, "attempt", job.Attempts, "batch", cp.CommittedBatches+1, "duration_ms", time.Since(started).Milliseconds(), "rows", rows, "error", observability.SafeError(execErr), "sqlstate", sqlState(execErr), "parameters", []string{}, "query", observability.SafeSQL(batchSQL))
		slog.InfoContext(queryCtx, "batch SQL finished", attrs...)
		if execErr != nil {
			rollbackStarted := time.Now()
			slog.InfoContext(queryCtx, "batch transaction rollback started", "event", "migration.batch.rollback.start", "job_id", job.ID, "execution_id", execution.ID, "migration_id", job.MigrationID, "database", job.TargetDBID, "batch", cp.CommittedBatches+1)
			rollbackErr := tx.Rollback()
			slog.InfoContext(queryCtx, "batch transaction rollback finished", "event", "migration.batch.rollback.result", "job_id", job.ID, "execution_id", execution.ID, "migration_id", job.MigrationID, "database", job.TargetDBID, "batch", cp.CommittedBatches+1, "duration_ms", time.Since(rollbackStarted).Milliseconds(), "error", observability.SafeError(rollbackErr))
			return s.batchFailure(ctx, conn, job, execution, execErr)
		}
		batchNumber := cp.CommittedBatches + 1
		if _, err := loggedExec(ctx, tx, `INSERT INTO wbd_migration_batch_history(job_id,batch_number,cursor_start,cursor_end,committed_rows) VALUES($1,$2,$3,$4,$5)`, job.ID, batchNumber, cp.Cursor, end, rows); err != nil {
			_ = tx.Rollback()
			return s.batchFailure(ctx, conn, job, execution, err)
		}
		if _, err := loggedExec(ctx, tx, `UPDATE wbd_migration_batch_checkpoints SET cursor=$2, committed_batches=committed_batches+1, committed_rows=committed_rows+$3, updated_at=now() WHERE job_id=$1`, job.ID, end, rows); err != nil {
			_ = tx.Rollback()
			return s.batchFailure(ctx, conn, job, execution, err)
		}
		commitStarted := time.Now()
		slog.InfoContext(queryCtx, "batch transaction commit started", "event", "migration.batch.commit.start", "job_id", job.ID, "execution_id", execution.ID, "migration_id", job.MigrationID, "database", job.TargetDBID, "batch", batchNumber)
		if err := tx.Commit(); err != nil {
			slog.InfoContext(queryCtx, "batch transaction commit failed", "event", "migration.batch.commit.result", "job_id", job.ID, "execution_id", execution.ID, "migration_id", job.MigrationID, "database", job.TargetDBID, "batch", batchNumber, "duration_ms", time.Since(commitStarted).Milliseconds(), "error", observability.SafeError(err))
			return s.batchFailure(ctx, conn, job, execution, fmt.Errorf("%w: %v", ErrTargetCommitUncertain, err))
		}
		slog.InfoContext(queryCtx, "batch transaction committed", "event", "migration.batch.commit.result", "job_id", job.ID, "execution_id", execution.ID, "migration_id", job.MigrationID, "database", job.TargetDBID, "batch", batchNumber, "duration_ms", time.Since(commitStarted).Milliseconds(), "status", "COMMITTED")
		cp.Cursor, cp.CommittedBatches, cp.CommittedRows = end, batchNumber, cp.CommittedRows+rows
		observability.BatchRows(ctx, job.Action, rows)
		if err := s.saveJobProgress(ctx, job.ID, execution.ID, cp); err != nil {
			return JobResult{Status: StatusPartial, ExecutionID: int64(execution.ID), CommittedBatches: cp.CommittedBatches, CommittedRows: cp.CommittedRows, TotalRows: cp.TotalRows, Cursor: cp.Cursor}, err
		}
	}
}

func (s *Service) runBatchCompensation(ctx context.Context, conn *sql.DB, job models.MigrationJob, execution models.MigrationExecution, version int) (JobResult, error) {
	var sourceJobID int
	if err := loggedQueryRow(ctx, conn, `SELECT source_job_id FROM wbd_migration_batch_checkpoints WHERE job_id=$1`, job.ID).Scan(&sourceJobID); err != nil {
		return s.batchFailure(ctx, conn, job, execution, err)
	}
	for {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return s.batchFailure(ctx, conn, job, execution, err)
		}
		cp, err := loadBatchCheckpoint(ctx, tx, job.ID)
		if err != nil {
			_ = tx.Rollback()
			return s.batchFailure(ctx, conn, job, execution, err)
		}
		if cp.Complete {
			_ = tx.Rollback()
			return s.completeCompensation(ctx, job, execution, version, cp)
		}
		order, flag := "DESC", false
		if job.Action == ActionForward {
			order, flag = "ASC", true
		}
		var batchNumber, after, through int64
		err = loggedQueryRow(ctx, tx, `SELECT batch_number,cursor_start,cursor_end FROM wbd_migration_batch_history WHERE job_id=$1 AND reverted=$2 ORDER BY batch_number `+order+` LIMIT 1 FOR UPDATE`, sourceJobID, flag).Scan(&batchNumber, &after, &through)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := loggedExec(ctx, tx, `UPDATE wbd_migration_batch_checkpoints SET complete=TRUE,status='SUCCEEDED',updated_at=now() WHERE job_id=$1`, job.ID); err != nil {
				_ = tx.Rollback()
				return s.batchFailure(ctx, conn, job, execution, err)
			}
			if err := tx.Commit(); err != nil {
				return s.batchFailure(ctx, conn, job, execution, fmt.Errorf("%w: %v", ErrTargetCommitUncertain, err))
			}
			cp.Complete = true
			return s.completeCompensation(ctx, job, execution, version, cp)
		}
		if err != nil {
			_ = tx.Rollback()
			return s.batchFailure(ctx, conn, job, execution, err)
		}
		query := batchSQLForRange(job, execution.SQLSnapshot, after, through, sourceJobID)
		started := time.Now()
		queryCtx, span := otel.Tracer("wbd/async").Start(ctx, "batch.query")
		slog.InfoContext(queryCtx, "batch compensation SQL started", "event", "migration.batch.sql.start", "job_id", job.ID, "execution_id", execution.ID, "migration_id", job.MigrationID, "database", job.TargetDBID, "attempt", job.Attempts, "batch", batchNumber, "query", observability.SafeSQL(query))
		result, execErr := tx.ExecContext(queryCtx, query)
		if execErr != nil {
			span.RecordError(errors.New("database query failed"))
			span.SetStatus(codes.Error, "batch compensation SQL failed")
		}
		span.End()
		rows := int64(0)
		if execErr == nil {
			rows, _ = result.RowsAffected()
		}
		slog.InfoContext(queryCtx, "batch compensation SQL finished", append(traceLogAttrs(queryCtx), "event", "migration.batch.sql.result", "job_id", job.ID, "execution_id", execution.ID, "migration_id", job.MigrationID, "database", job.TargetDBID, "actor", job.Actor, "attempt", job.Attempts, "batch", batchNumber, "duration_ms", time.Since(started).Milliseconds(), "rows", rows, "error", observability.SafeError(execErr), "sqlstate", sqlState(execErr), "parameters", []string{}, "query", observability.SafeSQL(query))...)
		if execErr != nil {
			_ = tx.Rollback()
			return s.batchFailure(ctx, conn, job, execution, execErr)
		}
		_, err = loggedExec(ctx, tx, `UPDATE wbd_migration_batch_history SET reverted=$3 WHERE job_id=$1 AND batch_number=$2`, sourceJobID, batchNumber, job.Action == ActionRollback)
		if err == nil {
			_, err = loggedExec(ctx, tx, `UPDATE wbd_migration_batch_checkpoints SET committed_batches=committed_batches+1,committed_rows=committed_rows+$2,cursor=$3,updated_at=now() WHERE job_id=$1`, job.ID, rows, batchNumber)
		}
		if err != nil {
			_ = tx.Rollback()
			return s.batchFailure(ctx, conn, job, execution, err)
		}
		if err := tx.Commit(); err != nil {
			return s.batchFailure(ctx, conn, job, execution, fmt.Errorf("%w: %v", ErrTargetCommitUncertain, err))
		}
		cp.CommittedBatches++
		cp.CommittedRows += rows
		observability.BatchRows(ctx, job.Action, rows)
		cp.Cursor = batchNumber
		if err := s.saveJobProgress(ctx, job.ID, execution.ID, cp); err != nil {
			status := StatusPartialReverted
			return JobResult{Status: status, ExecutionID: int64(execution.ID), CommittedBatches: cp.CommittedBatches, CommittedRows: cp.CommittedRows, TotalRows: cp.TotalRows, Cursor: cp.Cursor}, err
		}
	}
}

func (s *Service) completeCompensation(ctx context.Context, job models.MigrationJob, execution models.MigrationExecution, version int, cp batchCheckpoint) (JobResult, error) {
	if job.Action == ActionRollback {
		var source models.MigrationExecution
		if err := s.internalDB.WithContext(ctx).First(&source, job.ExpectedExecutionID).Error; err != nil {
			return JobResult{}, err
		}
		if source.Status == StatusPartial {
			err := s.internalDB.WithContext(context.WithoutCancel(ctx)).Transaction(func(tx *gorm.DB) error {
				sourceResult := tx.Model(&models.MigrationExecution{}).Where("id = ? AND status = ?", source.ID, StatusPartial).Update("status", StatusFailed)
				if sourceResult.Error != nil {
					return sourceResult.Error
				}
				if sourceResult.RowsAffected != 1 {
					return ErrJobConflict
				}
				jobResult := tx.Model(&models.MigrationJob{}).Where("execution_id = ? AND status = ?", source.ID, StatusPartial).Update("status", StatusFailed)
				if jobResult.Error != nil {
					return jobResult.Error
				}
				if jobResult.RowsAffected != 1 {
					return ErrJobConflict
				}
				rollbackResult := tx.Model(&models.MigrationExecution{}).Where("id = ? AND status IN ?", execution.ID, []string{StatusRunning, StatusUncertain, StatusPartial, StatusPartialReverted}).Updates(map[string]any{"status": StatusCompensated, "version": 0, "finished_at": time.Now().UTC(), "error": ""})
				if rollbackResult.Error != nil {
					return rollbackResult.Error
				}
				if rollbackResult.RowsAffected != 1 {
					return ErrJobConflict
				}
				return nil
			})
			if err != nil {
				return JobResult{Status: "UNCERTAIN"}, fmt.Errorf("%w: %v", ErrHistoryUpdateUncertain, err)
			}
		} else if err := s.finishAsyncExecution(ctx, execution, StatusApplied, version, nil); err != nil {
			return JobResult{Status: "UNCERTAIN"}, fmt.Errorf("%w: %v", ErrHistoryUpdateUncertain, err)
		}
	} else if err := s.finishAsyncExecution(ctx, execution, StatusApplied, version, nil); err != nil {
		return JobResult{Status: "UNCERTAIN"}, fmt.Errorf("%w: %v", ErrHistoryUpdateUncertain, err)
	}
	return JobResult{Status: "SUCCEEDED", ExecutionID: int64(execution.ID), CommittedBatches: cp.CommittedBatches, CommittedRows: cp.CommittedRows, TotalRows: cp.TotalRows, Cursor: cp.Cursor}, nil
}

func loadBatchCheckpoint(ctx context.Context, tx *sql.Tx, id int) (batchCheckpoint, error) {
	var cp batchCheckpoint
	err := loggedQueryRow(ctx, tx, `SELECT cursor,total_rows,committed_batches,committed_rows,complete FROM wbd_migration_batch_checkpoints WHERE job_id=$1 FOR UPDATE`, id).Scan(&cp.Cursor, &cp.TotalRows, &cp.CommittedBatches, &cp.CommittedRows, &cp.Complete)
	return cp, err
}

func nextBatchIDs(ctx context.Context, tx *sql.Tx, jobID int, cursor int64, batchSize int) ([]int64, error) {
	rows, err := loggedQuery(ctx, tx, `SELECT attribute_value FROM wbd_migration_batch_scope WHERE job_id=$1 AND attribute_value>$2 ORDER BY attribute_value LIMIT $3`, jobID, cursor, batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, batchSize)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func batchSQLForRange(job models.MigrationJob, query string, after, through int64, sourceJobID int) string {
	predicate := `"` + job.BatchAttribute + `" IN (SELECT attribute_value FROM wbd_migration_batch_scope WHERE job_id = ` + strconv.Itoa(sourceJobID) + ` AND attribute_value > ` + strconv.FormatInt(after, 10) + ` AND attribute_value <= ` + strconv.FormatInt(through, 10) + `)`
	return strings.Replace(query, batchPredicate, predicate, 1)
}

func (s *Service) saveJobProgress(ctx context.Context, jobID int, executionID int, cp batchCheckpoint) error {
	return s.internalDB.WithContext(context.WithoutCancel(ctx)).Model(&models.MigrationJob{}).Where("id=? AND execution_id=?", jobID, executionID).
		Updates(map[string]any{"committed_batches": cp.CommittedBatches, "committed_rows": cp.CommittedRows, "total_rows": cp.TotalRows, "cursor": cp.Cursor}).Error
}

func (s *Service) completeBatchJob(ctx context.Context, job models.MigrationJob, execution models.MigrationExecution, version int, cp batchCheckpoint) (JobResult, error) {
	if err := s.finishAsyncExecution(ctx, execution, StatusApplied, version, nil); err != nil {
		return JobResult{Status: "UNCERTAIN", ExecutionID: int64(execution.ID), CommittedBatches: cp.CommittedBatches, CommittedRows: cp.CommittedRows, TotalRows: cp.TotalRows, Cursor: cp.Cursor}, fmt.Errorf("%w: %v", ErrHistoryUpdateUncertain, err)
	}
	return JobResult{Status: "SUCCEEDED", ExecutionID: int64(execution.ID), CommittedBatches: cp.CommittedBatches, CommittedRows: cp.CommittedRows, TotalRows: cp.TotalRows, Cursor: cp.Cursor}, nil
}

func (s *Service) batchFailure(ctx context.Context, conn *sql.DB, job models.MigrationJob, execution models.MigrationExecution, cause error) (JobResult, error) {
	var cp batchCheckpoint
	err := loggedQueryRow(context.WithoutCancel(ctx), conn, `UPDATE wbd_migration_batch_checkpoints SET status='PARTIAL', error=$2, updated_at=now() WHERE job_id=$1 RETURNING cursor,total_rows,committed_batches,committed_rows,complete`, job.ID, cause.Error()).Scan(&cp.Cursor, &cp.TotalRows, &cp.CommittedBatches, &cp.CommittedRows, &cp.Complete)
	status := "FAILED"
	if cp.CommittedBatches > 0 {
		status = StatusPartial
		if job.Action == ActionRollback || job.Action == ActionForward {
			status = StatusPartialReverted
		}
	}
	if err == nil {
		_ = s.saveJobProgress(ctx, job.ID, execution.ID, cp)
	}
	if retryableDatabaseError(cause) {
		if cp.CommittedBatches == 0 {
			_ = s.finishAsyncExecution(ctx, execution, StatusFailed, 0, cause)
			_ = s.internalDB.WithContext(context.WithoutCancel(ctx)).Model(&models.MigrationJob{}).Where("id=? AND execution_id=?", job.ID, execution.ID).Updates(map[string]any{"execution_id": 0, "planned_version": 0}).Error
		} else {
			_ = s.finishAsyncExecution(ctx, execution, status, 0, cause)
		}
		return JobResult{}, ErrRetrySafe
	}
	_ = s.finishAsyncExecution(ctx, execution, status, 0, cause)
	resultStatus := status
	if status == "FAILED" {
		resultStatus = "FAILED"
	}
	return JobResult{Status: resultStatus, ExecutionID: int64(execution.ID), CommittedBatches: cp.CommittedBatches, CommittedRows: cp.CommittedRows, TotalRows: cp.TotalRows, Cursor: cp.Cursor, Error: cause.Error()}, cause
}
