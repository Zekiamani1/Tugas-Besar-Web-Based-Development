package migration

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"wbd/internal/models"
	"wbd/internal/observability"
)

func TestObservabilityQueueLockAndTrace(t *testing.T) {
	dsn, rabbitURL := os.Getenv("MIGRATION_TEST_DSN"), os.Getenv("RABBITMQ_TEST_URL")
	otlp, metricsURL, tempoURL := os.Getenv("WBD_TEST_OTLP_ENDPOINT"), os.Getenv("WBD_TEST_METRICS_URL"), os.Getenv("WBD_TEST_TEMPO_URL")
	if dsn == "" || rabbitURL == "" || otlp == "" || metricsURL == "" || tempoURL == "" {
		t.Skip("requires disposable PostgreSQL, RabbitMQ and LGTM endpoints")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", otlp)
	runtime, err := observability.Setup(context.Background(), "wbd-e2e-test")
	if err != nil {
		t.Fatal(err)
	}
	shutdown := false
	defer func() {
		if !shutdown {
			_ = runtime.Shutdown(context.Background())
		}
	}()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("observability_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	target, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	svc := NewService(db, targetProvider{conn: target})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	migrationID, err := svc.CreateDraft(ctx, 1, "CREATE TABLE observed_result (id integer)")
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
	queue := "observe_" + schema
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := declareRetryQueue(ch, queue); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = ch.QueueDelete(queue+".retry", false, false, false)
		_, _ = ch.QueueDelete(queue, false, false, false)
	}()
	lock, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(ctx, "SELECT pg_advisory_lock(3111, 1)"); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(3111, 1)")
		}
	}()
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 2)
	go func() { done <- svc.RunPublisher(workerCtx, conn, queue) }()
	go func() { done <- svc.RunWorker(workerCtx, conn, queue, svc.validateAsyncJob, svc.executeAsyncJob) }()
	joined := false
	defer func() {
		stop()
		if !joined {
			<-done
			<-done
		}
	}()
	var jobID int
	var traceID string
	handler, err := observability.HTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID = trace.SpanContextFromContext(r.Context()).TraceID().String()
		jobID, err = svc.SubmitJob(r.Context(), models.MigrationJob{MigrationID: migrationID, TargetDBID: 1, Actor: "test", Action: ActionApply, Mode: "STANDARD", Revision: 1})
		if err != nil {
			w.WriteHeader(500)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/submit", nil))
	if err != nil || response.Code != 200 || jobID == 0 {
		t.Fatalf("submission failed: %v status %d", err, response.Code)
	}
	for {
		info, e := ch.QueueInspect(queue + ".retry")
		if e == nil && info.Messages > 0 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("job did not enter retry queue")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := lock.ExecContext(ctx, "SELECT pg_advisory_unlock(3111, 1)"); err != nil {
		t.Fatal(err)
	}
	locked = false
	for {
		job, e := svc.FindJob(ctx, 1, migrationID, jobID)
		if e != nil {
			t.Fatal(e)
		}
		if job.Status == "SUCCEEDED" {
			break
		}
		if job.Status == "FAILED" || ctx.Err() != nil {
			t.Fatalf("job did not succeed: %+v", job)
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	<-done
	<-done
	joined = true
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	shutdown = true
	deadline := time.Now().Add(15 * time.Second)
	var metricBody, traceBody string
	for time.Now().Before(deadline) {
		metricBody = getTestURL(metricsURL)
		traceBody = getTestURL(strings.TrimRight(tempoURL, "/") + "/api/traces/" + traceID)
		if strings.Contains(metricBody, "wbd_migration_lock_wait_duration_seconds_sum") && strings.Contains(traceBody, "worker.process") && strings.Contains(traceBody, "database.query") {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	var waitSum float64
	for _, line := range strings.Split(metricBody, "\n") {
		if strings.HasPrefix(line, "wbd_migration_lock_wait_duration_seconds_sum") {
			fields := strings.Fields(line)
			if len(fields) > 1 {
				waitSum, _ = strconv.ParseFloat(fields[1], 64)
			}
		}
	}
	if waitSum < 1 {
		t.Errorf("lock wait metric too small or missing: %v", waitSum)
	}
	for _, name := range []string{"http.request", "queue.publish", "worker.process", "database.lock", "migration.execute", "database.query"} {
		if !strings.Contains(traceBody, name) {
			t.Errorf("trace %s missing %s: %s", traceID, name, traceBody)
		}
	}
	t.Logf("trace=%s lock wait sum=%.3f seconds", traceID, waitSum)
}

func getTestURL(address string) string {
	resp, err := http.Get(address)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}
