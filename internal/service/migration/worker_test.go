package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"wbd/internal/models"
)

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
	defer func() { _, _ = ch.QueueDelete(queue, false, false, false) }()
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 2)
	validate := func(context.Context, models.MigrationJob) error { return nil }
	execute := func(ctx context.Context, job models.MigrationJob) (JobResult, error) {
		_, err := svc.ExecuteApprovedApply(ctx, job.TargetDBID, job.MigrationID, job.Revision, job.Actor)
		return JobResult{}, err
	}
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
	stop()
	<-done
	<-done
	joined = true
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
			if err := svc.ProcessJob(ctx, j.ID, validate, retry); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.First(&j, j.ID).Error; err != nil {
			t.Fatal(err)
		}
		if j.Status != "FAILED" || j.Attempts != 3 {
			t.Fatalf("unbounded retry: %+v", j)
		}
		if err := db.Model(&j).Update("status", "RUNNING").Error; err != nil {
			t.Fatal(err)
		}
		noReplay := func(context.Context, models.MigrationJob) (JobResult, error) {
			t.Error("interrupted job must not replay SQL")
			return JobResult{}, nil
		}
		if err := svc.ProcessJob(ctx, j.ID, validate, noReplay); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&j, j.ID).Error; err != nil {
			t.Fatal(err)
		}
		if j.Status != "UNCERTAIN" {
			t.Fatalf("expected uncertain, got %s", j.Status)
		}
		svc.Reconcile = func(context.Context, models.MigrationJob) (JobResult, error) {
			return JobResult{Status: "SUCCEEDED"}, nil
		}
		defer func() { svc.Reconcile = nil }()
		if err := svc.ProcessJob(ctx, j.ID, validate, noReplay); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&j, j.ID).Error; err != nil {
			t.Fatal(err)
		}
		if j.Status != "SUCCEEDED" {
			t.Fatalf("reconciliation not saved: %s", j.Status)
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
		go func() { finished <- svc.ProcessJob(ctx, first, validate, hold) }()
		defer func() {
			close(release)
			if err := <-finished; err != nil {
				t.Error(err)
			}
		}()
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("first job did not start")
		}
		instant := func(context.Context, models.MigrationJob) (JobResult, error) { return JobResult{}, nil }
		if err := svc.ProcessJob(ctx, second, validate, instant); !errors.Is(err, ErrIntegrationPending) {
			t.Fatalf("same database was not locked: %v", err)
		}
		if err := svc.ProcessJob(ctx, other, validate, instant); err != nil {
			t.Fatalf("different database blocked: %v", err)
		}
	})
}
