package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wbd/internal/models"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/dbmanagement"
	"wbd/internal/service/migration"
)

func sendAccessControlRequest(
	t *testing.T,
	app http.Handler,
	method string,
	path string,
	body any,
	cookie *http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()

	var requestBody *bytes.Reader
	if body == nil {
		requestBody = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		requestBody = bytes.NewReader(data)
	}

	req := httptest.NewRequest(method, path, requestBody)
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

func loginThroughApp(
	t *testing.T,
	app http.Handler,
	username string,
	password string,
) *http.Cookie {
	t.Helper()

	rec := sendAccessControlRequest(
		t,
		app,
		http.MethodPost,
		"/login",
		map[string]string{
			"username": username,
			"password": password,
		},
		nil,
	)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d; body: %s",
			rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "session_id" && cookie.Value != "" {
			return cookie
		}
	}

	t.Fatal("login response did not set a session cookie")
	return nil
}

func TestAccessControlAppMiddlewareServicePostgresIntegration(t *testing.T) {
	ctx := context.Background()

	// Satu database internal menyimpan user, session, group, keanggotaan, dan izin group.
	internalContainer := startTestPostgres(t, "auth_internal_test")
	internalDB := openGorm(t, internalContainer)

	// Database kedua adalah database target yang akan dibaca
	targetContainer := startTestPostgres(t, "auth_target_test")
	targetDB := openGorm(t, targetContainer)

	const targetSchema = "access_control_integration"
	if err := targetDB.Exec("CREATE SCHEMA " + targetSchema).Error; err != nil {
		t.Fatalf("create target schema: %v", err)
	}
	if err := targetDB.Exec(
		"CREATE TABLE " + targetSchema + ".example (id integer PRIMARY KEY)",
	).Error; err != nil {
		t.Fatalf("create target table: %v", err)
	}

	targetHost, err := targetContainer.Host(ctx)
	if err != nil {
		t.Fatalf("get target database host: %v", err)
	}
	targetPort, err := targetContainer.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("get target database port: %v", err)
	}

	// menyiapkan tabel user, session, group, dan izin.
	dbmService := dbmanagement.NewService(internalDB)
	t.Cleanup(dbmService.CloseAllTargetConnections)

	acService := accesscontrol.NewService(
		internalDB,
		accesscontrol.SessionConfig{
			AbsoluteTTL: 2 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	)
	migrationService := migration.NewService(internalDB, dbmService)

	app := NewApp(acService, dbmService, migrationService)

	// Buat fixture admin melalui service, lalu  flag di database.
	if err := acService.Register(ctx, "platform-admin", "admin-password"); err != nil {
		t.Fatalf("register initial admin fixture: %v", err)
	}

	var admin models.User
	if err := internalDB.
		Where("username = ?", "platform-admin").
		First(&admin).Error; err != nil {
		t.Fatalf("find initial admin fixture: %v", err)
	}
	if err := internalDB.Model(&models.User{}).
		Where("id = ?", admin.ID).
		Update("is_admin", true).Error; err != nil {
		t.Fatalf("promote initial admin fixture: %v", err)
	}

	t.Run("unauthenticated admin request is rejected", func(t *testing.T) {
		rec := sendAccessControlRequest(
			t,
			app,
			http.MethodGet,
			"/admin",
			nil,
			nil,
		)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d; body: %s",
				rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	})

	adminCookie := loginThroughApp(t, app, "platform-admin", "admin-password")

	t.Run("authenticated platform admin can open admin page", func(t *testing.T) {
		rec := sendAccessControlRequest(
			t,
			app,
			http.MethodGet,
			"/admin",
			nil,
			adminCookie,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body: %s",
				rec.Code, http.StatusOK, rec.Body.String())
		}
	})

	t.Run("admin provisions target, user, group, and database permission", func(t *testing.T) {
		// Register database target through App -> RequireAdminAccess -> handler -> DBManagementService -> internal PostgreSQL.
		targetInfo := models.TargetDBInfo{
			Name:     "auth-integration-target",
			DBType:   "postgres",
			Host:     targetHost,
			Port:     int(targetPort.Num()),
			User:     testPGUser,
			Password: testPGPassword,
			DBName:   "auth_target_test",
		}
		rec := sendAccessControlRequest(
			t,
			app,
			http.MethodPost,
			"/admin/databases",
			targetInfo,
			adminCookie,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("register target status = %d, want %d; body: %s",
				rec.Code, http.StatusOK, rec.Body.String())
		}

		var storedTarget models.TargetDBInfo
		if err := internalDB.
			Where("name = ?", targetInfo.Name).
			First(&storedTarget).Error; err != nil {
			t.Fatalf("find registered target: %v", err)
		}
		rec = sendAccessControlRequest(
			t,
			app,
			http.MethodPost,
			"/admin/users",
			map[string]string{
				"username": "viewer",
				"password": "viewer-password",
			},
			adminCookie,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("register viewer status = %d, want %d; body: %s",
				rec.Code, http.StatusOK, rec.Body.String())
		}

		var viewer models.User
		if err := internalDB.
			Where("username = ?", "viewer").
			First(&viewer).Error; err != nil {
			t.Fatalf("find viewer user: %v", err)
		}
		if viewer.IsAdmin {
			t.Fatal("new ordinary user unexpectedly has Platform Admin role")
		}

		// Create group, assign user, then grant VIEW_ONLY through
		rec = sendAccessControlRequest(
			t,
			app,
			http.MethodPost,
			"/admin/groups",
			map[string]string{"group_name": "readers"},
			adminCookie,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("create group status = %d, want %d; body: %s",
				rec.Code, http.StatusOK, rec.Body.String())
		}

		var readers models.Group
		if err := internalDB.
			Where("name = ?", "readers").
			First(&readers).Error; err != nil {
			t.Fatalf("find readers group: %v", err)
		}

		rec = sendAccessControlRequest(
			t,
			app,
			http.MethodPost,
			"/admin/groups/members",
			map[string]int{
				"user_id":  viewer.ID,
				"group_id": readers.ID,
			},
			adminCookie,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("assign group member status = %d, want %d; body: %s",
				rec.Code, http.StatusOK, rec.Body.String())
		}

		rec = sendAccessControlRequest(
			t,
			app,
			http.MethodPost,
			"/admin/groups/permissions",
			map[string]any{
				"group_id":     readers.ID,
				"target_db_id": storedTarget.ID,
				"access_level": accesscontrol.AccessLevelViewOnly,
			},
			adminCookie,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("set group permission status = %d, want %d; body: %s",
				rec.Code, http.StatusOK, rec.Body.String())
		}
	})

	viewerCookie := loginThroughApp(t, app, "viewer", "viewer-password")

	t.Run("ordinary user cannot access Platform Admin page", func(t *testing.T) {
		rec := sendAccessControlRequest(
			t,
			app,
			http.MethodGet,
			"/admin",
			nil,
			viewerCookie,
		)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d; body: %s",
				rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	var storedTarget models.TargetDBInfo
	if err := internalDB.
		Where("name = ?", "auth-integration-target").
		First(&storedTarget).Error; err != nil {
		t.Fatalf("find target for access requests: %v", err)
	}
	schemasPath := fmt.Sprintf("/databases/%d/schemas", storedTarget.ID)

	t.Run("missing session is rejected before database access", func(t *testing.T) {
		rec := sendAccessControlRequest(
			t,
			app,
			http.MethodGet,
			schemasPath,
			nil,
			nil,
		)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d; body: %s",
				rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	})

	t.Run("VIEW_ONLY user reaches target PostgreSQL through middleware", func(t *testing.T) {
		rec := sendAccessControlRequest(
			t,
			app,
			http.MethodGet,
			schemasPath,
			nil,
			viewerCookie,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body: %s",
				rec.Code, http.StatusOK, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), targetSchema) {
			t.Fatalf("response does not contain target schema %q: %s",
				targetSchema, rec.Body.String())
		}
	})

	t.Run("VIEW_ONLY user is denied migration write access", func(t *testing.T) {
		path := fmt.Sprintf("/databases/%d/migrations", storedTarget.ID)
		rec := sendAccessControlRequest(
			t,
			app,
			http.MethodPost,
			path,
			map[string]string{"sql_query": "CREATE TABLE denied (id integer)"},
			viewerCookie,
		)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d; body: %s",
				rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("logout revokes session used by middleware", func(t *testing.T) {
		rec := sendAccessControlRequest(
			t,
			app,
			http.MethodPost,
			"/logout",
			map[string]string{},
			viewerCookie,
		)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("logout status = %d, want %d; body: %s",
				rec.Code, http.StatusSeeOther, rec.Body.String())
		}

		rec = sendAccessControlRequest(
			t,
			app,
			http.MethodGet,
			schemasPath,
			nil,
			viewerCookie,
		)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("request after logout status = %d, want %d; body: %s",
				rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	})
}
