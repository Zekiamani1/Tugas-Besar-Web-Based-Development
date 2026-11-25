package migration

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
	"wbd/internal/models"

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
	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}
	messages, err := ch.Consume(queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	recovery := time.NewTicker(10 * time.Second)
	defer recovery.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-recovery.C:
			if s.Reconcile != nil {
				var jobs []models.MigrationJob
				if err := s.internalDB.WithContext(ctx).Where("status IN ?", []string{"RUNNING", "UNCERTAIN"}).Order("updated_at").Limit(10).Find(&jobs).Error; err != nil {
					return err
				}
				for _, job := range jobs {
					if err := s.ProcessJob(ctx, job.ID, validate, execute); err != nil && !errors.Is(err, ErrIntegrationPending) {
						return err
					}
				}
			}
		case message, ok := <-messages:
			if !ok {
				return errors.New("consumer disconnected")
			}
			var payload struct {
				ID int `json:"job_id"`
			}
			if json.Unmarshal(message.Body, &payload) != nil || payload.ID <= 0 {
				if err := message.Reject(false); err != nil {
					return err
				}
				continue
			}
			parent, _ := message.Headers["traceparent"].(string)
			jobCtx := propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": parent})
			jobCtx, span := otel.Tracer("wbd/async").Start(jobCtx, "worker.process")
			err := s.ProcessJob(jobCtx, payload.ID, validate, execute)
			if err != nil {
				span.RecordError(errors.New("job processing failed"))
				span.SetStatus(codes.Error, "job processing failed")
			}
			span.End()
			if errors.Is(err, ErrIntegrationPending) {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(2 * time.Second):
				}
				if err := message.Nack(false, true); err != nil {
					return err
				}
				continue
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := message.Reject(false); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			if err := message.Ack(false); err != nil {
				return err
			}
		}
	}
}

func (s *Service) RunMigrationWorker(ctx context.Context, conn *amqp.Connection, queue string) error {
	validate := func(context.Context, models.MigrationJob) error {
		// TODO: approval
		return ErrIntegrationPending
	}
	execute := func(ctx context.Context, job models.MigrationJob) (JobResult, error) {
		if job.Mode != "STANDARD" {
			// TODO: konek approval dan checkpoint
			return JobResult{}, ErrIntegrationPending
		}
		if job.Action == ActionApply {
			_, err := s.ExecuteApprovedApply(ctx, job.TargetDBID, job.MigrationID, job.Revision, job.Actor)
			if errors.Is(err, ErrDatabaseBusy) {
				return JobResult{}, ErrRetrySafe
			}
			return JobResult{}, err
		}
		_, err := s.ExecuteReversal(ctx, job.TargetDBID, job.MigrationID, job.ExpectedExecutionID, job.Action, job.Actor)
		if errors.Is(err, ErrDatabaseBusy) {
			return JobResult{}, ErrRetrySafe
		}
		return JobResult{}, err
	}
	return s.RunWorker(ctx, conn, queue, validate, execute)
}

func (s *Service) ProcessJob(ctx context.Context, id int, validate JobValidator, execute JobExecutor) error {
	var job models.MigrationJob
	if err := s.internalDB.WithContext(ctx).First(&job, id).Error; err != nil {
		return err
	}
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
	lockCtx, lockSpan := otel.Tracer("wbd/async").Start(ctx, "database.lock")
	err = lock.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock(3111, $1::integer)", job.TargetDBID).Scan(&acquired)
	if err != nil {
		lockSpan.SetStatus(codes.Error, "lock failed")
	}
	lockSpan.End()
	if err != nil {
		return err
	}
	if !acquired {
		return ErrIntegrationPending
	}
	jobEvent(ctx, "job.lock.acquired", job)
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var unlocked bool
		if err := lock.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock(3111, $1::integer)", job.TargetDBID).Scan(&unlocked); err != nil || !unlocked {
			_ = lock.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if err := s.internalDB.WithContext(ctx).First(&job, id).Error; err != nil {
		return err
	}
	if job.Status == "RUNNING" || job.Status == "UNCERTAIN" {
		if s.Reconcile == nil {
			return s.internalDB.WithContext(ctx).Model(&job).Update("status", "UNCERTAIN").Error
		}
		// TODO: commit/checkpoint proof
		proof, err := s.Reconcile(ctx, job)
		if err != nil {
			return err
		}
		if proof.Status != "SUCCEEDED" && proof.Status != "FAILED" && proof.Status != "QUEUED" && proof.Status != "PARTIAL" && proof.Status != "PARTIALLY_REVERTED" {
			return ErrIntegrationPending
		}
		updates := map[string]any{"status": proof.Status, "committed_batches": proof.CommittedBatches, "committed_rows": proof.CommittedRows}
		if proof.Status == "QUEUED" {
			updates["published_at"] = nil
			updates["next_attempt_at"] = time.Now().UTC()
		}
		return s.internalDB.WithContext(ctx).Model(&job).Updates(updates).Error
	}
	if job.Status != "QUEUED" {
		return nil
	}
	if job.NextAttemptAt.After(time.Now().UTC()) {
		return ErrIntegrationPending
	}
	var blocked int64
	if err := s.internalDB.WithContext(ctx).Model(&models.MigrationJob{}).Where("target_db_id = ? AND id <> ? AND status IN ?", job.TargetDBID, id, []string{"RUNNING", "PARTIAL", "PARTIALLY_REVERTED", "UNCERTAIN"}).Count(&blocked).Error; err != nil {
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
		updates := map[string]any{"status": "FAILED", "attempts": job.Attempts + 1}
		if errors.Is(err, ErrRetrySafe) && job.Attempts+1 < 3 {
			updates["status"] = "QUEUED"
			updates["published_at"] = nil
			updates["next_attempt_at"] = time.Now().UTC().Add(time.Duration((job.Attempts+1)*2) * time.Second)
		}
		job.Status = updates["status"].(string)
		jobEvent(ctx, "job.validation.result", job)
		return s.internalDB.WithContext(ctx).Model(&job).Updates(updates).Error
	}
	job.Attempts++
	job.Status = "RUNNING"
	if err := s.internalDB.WithContext(ctx).Model(&job).Updates(map[string]any{"status": job.Status, "attempts": job.Attempts}).Error; err != nil {
		return err
	}
	jobEvent(ctx, "job.running", job)
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
	updates := map[string]any{"status": status, "committed_batches": result.CommittedBatches, "committed_rows": result.CommittedRows}
	if status != "SUCCEEDED" && status != "FAILED" && status != "PARTIAL" && status != "PARTIALLY_REVERTED" && status != "UNCERTAIN" {
		updates["status"] = "UNCERTAIN"
	}
	if errors.Is(execErr, ErrRetrySafe) && result.Status == "" && status != "UNCERTAIN" && job.Attempts < 3 {
		updates["status"] = "QUEUED"
		updates["published_at"] = nil
		updates["next_attempt_at"] = time.Now().UTC().Add(time.Duration(job.Attempts*2) * time.Second)
	}
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if errors.Is(execErr, ErrIntegrationPending) {
		updates["status"] = "QUEUED"
		updates["attempts"] = job.Attempts - 1
		updates["published_at"] = nil
		updates["next_attempt_at"] = time.Now().UTC().Add(30 * time.Second)
	}
	defer cancel()
	if err := s.internalDB.WithContext(saveCtx).Model(&job).Updates(updates).Error; err != nil {
		return err
	}
	job.Status = updates["status"].(string)
	event := "job.finished"
	if job.Status == "QUEUED" {
		event = "job.retry"
	}
	jobEvent(ctx, event, job)
	return nil
}
