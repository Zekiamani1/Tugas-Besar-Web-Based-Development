package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"wbd/internal/models"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/dbmanagement"
	"wbd/internal/service/migration"
)

func TestMigrationAppIntegration(t *testing.T) {
	ctx := context.Background()
	internalContainer := startTestPostgres(t, "migration_app_internal")
	targetContainer := startTestPostgres(t, "migration_app_target")
	internalDB := openGorm(t, internalContainer)
	targetDB := openGorm(t, targetContainer)

	host, err := targetContainer.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := targetContainer.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}

	ac := accesscontrol.NewService(internalDB, accesscontrol.SessionConfig{
		AbsoluteTTL:      2 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		RotationInterval: 15 * time.Minute,
		GracePeriod:      30 * time.Second,
	})
	dbm := dbmanagement.NewService(internalDB)
	t.Cleanup(dbm.CloseAllTargetConnections)
	mig := migration.NewService(internalDB, dbm)
	if err := mig.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	targetID, err := dbm.RegisterTargetDB(ctx, models.TargetDBInfo{
		Name:     "migration-app-target",
		DBType:   "postgres",
		Host:     host,
		Port:     int(port.Num()),
		User:     testPGUser,
		Password: testPGPassword,
		DBName:   "migration_app_target",
	})
	if err != nil || targetID <= 0 {
		t.Fatalf("register target: id=%d err=%v", targetID, err)
	}

	for _, user := range []struct {
		name  string
		level string
	}{
		{"viewer", accesscontrol.AccessLevelViewOnly},
		{"editor", accesscontrol.AccessLevelViewEdit},
	} {
		if err := ac.Register(ctx, user.name, "test-password"); err != nil {
			t.Fatal(err)
		}
		if err := ac.CreateGroup(ctx, user.name+"-group"); err != nil {
			t.Fatal(err)
		}
		var storedUser models.User
		var group models.Group
		if err := internalDB.Where("username = ?", user.name).First(&storedUser).Error; err != nil {
			t.Fatal(err)
		}
		if err := internalDB.Where("name = ?", user.name+"-group").First(&group).Error; err != nil {
			t.Fatal(err)
		}
		if err := ac.AssignUserToGroup(ctx, storedUser.ID, group.ID); err != nil {
			t.Fatal(err)
		}
		if err := ac.SetGroupDBAccess(ctx, group.ID, targetID, user.level); err != nil {
			t.Fatal(err)
		}
	}

	app := NewApp(ac, dbm, mig)
	login := func(username string) *http.Cookie {
		t.Helper()
		req := newJSONRequest(t, http.MethodPost, "/login", map[string]string{
			"username": username,
			"password": "test-password",
		})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("login %s: status=%d body=%s", username, rec.Code, rec.Body.String())
		}
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Name == "session_id" {
				return cookie
			}
		}
		t.Fatalf("login %s did not set session cookie", username)
		return nil
	}
	request := func(method, path, form string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(form))
		if form != "" {
			addCSRFFormToken(req)
		}
		if form == "" && method == http.MethodPost {
			addCSRFFormToken(req)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	viewerCookie := login("viewer")
	editorCookie := login("editor")
	basePath := fmt.Sprintf("/databases/%d/migrations", targetID)
	sqlQuery := "SELECT count(*) FROM generate_series(1, 100)"
	createForm := url.Values{"sql_query": {sqlQuery}}.Encode()

	if rec := request(http.MethodPost, basePath, createForm, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create: status=%d", rec.Code)
	}
	if rec := request(http.MethodPost, basePath, createForm, viewerCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer create: status=%d", rec.Code)
	}
	created := request(http.MethodPost, basePath, createForm, editorCookie)
	if created.Code != http.StatusSeeOther {
		t.Fatalf("editor create: status=%d body=%s", created.Code, created.Body.String())
	}
	migrationID, err := strconv.Atoi(strings.TrimPrefix(created.Header().Get("Location"), basePath+"/"))
	if err != nil || migrationID <= 0 {
		t.Fatalf("invalid draft redirect: %q", created.Header().Get("Location"))
	}
	if rec := request(http.MethodGet, basePath, "", editorCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fmt.Sprintf("Draft #%d", migrationID)) {
		t.Fatalf("editor cannot find the saved draft: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodGet, basePath, "", viewerCookie); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), fmt.Sprintf("Draft #%d", migrationID)) {
		t.Fatalf("viewer can see an editable draft: status=%d body=%s", rec.Code, rec.Body.String())
	}

	explainPath := fmt.Sprintf("%s/%d/explain", basePath, migrationID)
	if rec := request(http.MethodPost, explainPath, "", viewerCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer explain: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPost, explainPath, "", editorCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Function Scan") {
		t.Fatalf("editor explain: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Restore a DDL query so the existing execute flow verifies a real schema change.
	ddlForm := url.Values{"sql_query": {"CREATE TABLE migration_app_example (id integer PRIMARY KEY)"}}.Encode()
	if rec := request(http.MethodPost, fmt.Sprintf("%s/%d/edit", basePath, migrationID), ddlForm, editorCookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("restore DDL draft: status=%d body=%s", rec.Code, rec.Body.String())
	}

	executePath := fmt.Sprintf("%s/%d/execute", basePath, migrationID)
	if rec := request(http.MethodPost, executePath, "", viewerCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer execute: status=%d", rec.Code)
	}
	var rabbitConn *amqp.Connection
	var queueCancel context.CancelFunc
	var queueDone chan error
	if rabbitURL := os.Getenv("RABBITMQ_TEST_URL"); rabbitURL != "" {
		rabbitConn, err = amqp.Dial(rabbitURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rabbitConn.Close() })
		queueCtx, cancel := context.WithCancel(ctx)
		queueCancel = cancel
		queueDone = make(chan error, 2)
		queueName := fmt.Sprintf("migration_app_%d", time.Now().UnixNano())
		t.Logf("RabbitMQ queue under test: %s", queueName)
		go func() { queueDone <- mig.RunPublisher(queueCtx, rabbitConn, queueName) }()
		go func() { queueDone <- mig.RunMigrationWorker(queueCtx, rabbitConn, queueName) }()
		t.Cleanup(func() {
			queueCancel()
			<-queueDone
			<-queueDone
			ch, e := rabbitConn.Channel()
			if e == nil {
				_, _ = ch.QueueDelete(queueName+".retry", false, false, false)
				_, _ = ch.QueueDelete(queueName, false, false, false)
				_ = ch.Close()
			}
		})
	}
	if rec := request(http.MethodPost, executePath, "", editorCookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("editor execute: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var job models.MigrationJob
	if err := internalDB.Where("migration_id = ? AND target_db_id = ?", migrationID, targetID).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != "QUEUED" && !(rabbitConn != nil && (job.Status == "RUNNING" || job.Status == "SUCCEEDED")) {
		t.Fatalf("expected queued job, got %s", job.Status)
	}
	if rabbitConn == nil {
		validate := func(context.Context, models.MigrationJob) error { return nil }
		execute := func(ctx context.Context, job models.MigrationJob) (migration.JobResult, error) {
			_, err := mig.ExecuteApprovedApply(ctx, job.TargetDBID, job.MigrationID, job.Revision, job.Actor)
			return migration.JobResult{}, err
		}
		if err := mig.ProcessJob(ctx, job.ID, validate, execute); err != nil {
			t.Fatal(err)
		}
	} else {
		statusPath := fmt.Sprintf("%s/%d?job_id=%d", basePath, migrationID, job.ID)
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		for {
			rec := request(http.MethodGet, statusPath, "", editorCookie)
			if rec.Code != http.StatusOK {
				t.Fatalf("job status endpoint: status=%d body=%s", rec.Code, rec.Body.String())
			}
			var current models.MigrationJob
			if err := json.Unmarshal(rec.Body.Bytes(), &current); err != nil {
				t.Fatal(err)
			}
			if current.Status == "SUCCEEDED" {
				job = current
				break
			}
			if current.Status == "FAILED" || current.Status == "UNCERTAIN" {
				t.Fatalf("RabbitMQ job failed: %+v", current)
			}
			select {
			case <-deadline.C:
				t.Fatalf("HTTP job status did not reach success: %+v", current)
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	if err := internalDB.First(&job, job.ID).Error; err != nil || job.Status != "SUCCEEDED" {
		t.Fatalf("job did not succeed: status=%s err=%v", job.Status, err)
	}
	if !targetDB.Migrator().HasTable("migration_app_example") {
		t.Fatal("migration did not create the target table")
	}

	var editor models.User
	var record models.MigrationExecution
	if err := internalDB.Where("username = ?", "editor").First(&editor).Error; err != nil {
		t.Fatal(err)
	}
	if err := internalDB.Where("migration_id = ?", migrationID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != migration.StatusApplied || record.Version != 1 || record.ExecutedBy != strconv.Itoa(editor.ID) || record.StartedAt.IsZero() {
		t.Fatalf("unexpected migration history: %+v", record)
	}
	if rec := request(http.MethodGet, basePath, "", editorCookie); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), fmt.Sprintf("Draft #%d", migrationID)) {
		t.Fatalf("executed draft still appears in draft list: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodGet, basePath, "", viewerCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "APPLIED") {
		t.Fatalf("viewer history: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
