package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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
		if rec.Code != http.StatusOK {
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
	request := func(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var bodyReader *bytes.Reader
		if body == nil {
			bodyReader = bytes.NewReader(nil)
		} else {
			payload, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal request body: %v", err)
			}
			bodyReader = bytes.NewReader(payload)
		}
		req := httptest.NewRequest(method, path, bodyReader)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
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
	createBody := map[string]string{"sql_query": sqlQuery}

	if rec := request(http.MethodPost, basePath, createBody, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create: status=%d", rec.Code)
	}
	if rec := request(http.MethodPost, basePath, createBody, viewerCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer create: status=%d", rec.Code)
	}
	created := request(http.MethodPost, basePath, createBody, editorCookie)
	if created.Code != http.StatusCreated {
		t.Fatalf("editor create: status=%d body=%s", created.Code, created.Body.String())
	}
	var createResp struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(bytes.NewReader(created.Body.Bytes())).Decode(&createResp); err != nil || createResp.ID <= 0 {
		t.Fatalf("invalid created draft JSON %q: %v", created.Body.String(), err)
	}
	migrationID := createResp.ID
	if rec := request(http.MethodGet, basePath, nil, editorCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fmt.Sprintf(`"id":%d`, migrationID)) {
		t.Fatalf("editor cannot find the saved draft: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodGet, basePath, nil, viewerCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"drafts":[]`) {
		t.Fatalf("viewer can see an editable draft: status=%d body=%s", rec.Code, rec.Body.String())
	}

	explainPath := fmt.Sprintf("%s/%d/explain", basePath, migrationID)
	if rec := request(http.MethodPost, explainPath, nil, viewerCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer explain: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPost, explainPath, nil, editorCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Function Scan") {
		t.Fatalf("editor explain: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Restore a DDL query so the existing execute flow verifies a real schema change.
	ddlBody := map[string]string{"sql_query": "CREATE TABLE migration_app_example (id integer PRIMARY KEY)"}
	if rec := request(http.MethodPut, fmt.Sprintf("%s/%d", basePath, migrationID), ddlBody, editorCookie); rec.Code != http.StatusOK {
		t.Fatalf("restore DDL draft: status=%d body=%s", rec.Code, rec.Body.String())
	}

	executePath := fmt.Sprintf("%s/%d/execute", basePath, migrationID)
	if rec := request(http.MethodPost, executePath, nil, viewerCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer execute: status=%d", rec.Code)
	}
	if rec := request(http.MethodPost, executePath, nil, editorCookie); rec.Code != http.StatusOK {
		t.Fatalf("editor execute: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !targetDB.Migrator().HasTable("migration_app_example") {
		t.Fatal("migration did not create the target table")
	}

	var editor models.User
	var record models.MigrationRecord
	if err := internalDB.Where("username = ?", "editor").First(&editor).Error; err != nil {
		t.Fatal(err)
	}
	if err := internalDB.First(&record, migrationID).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != migration.StatusApplied || record.Version != 1 || record.ExecutedBy != strconv.Itoa(editor.ID) || record.ExecutedAt.IsZero() {
		t.Fatalf("unexpected migration history: %+v", record)
	}
	if rec := request(http.MethodGet, basePath, nil, editorCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"drafts":[]`) {
		t.Fatalf("executed draft still appears in draft list: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodGet, basePath, nil, viewerCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "APPLIED") {
		t.Fatalf("viewer history: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
