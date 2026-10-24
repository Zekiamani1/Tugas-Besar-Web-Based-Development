package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wbd/internal/middleware"
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
			AbsoluteTTL:      2 * time.Hour,
			IdleTimeout:      30 * time.Minute,
			RotationInterval: 15 * time.Minute,
			GracePeriod:      30 * time.Second,
		},
	)
	migrationService := migration.NewService(internalDB, dbmService)

	app := NewAppWithConfig(acService, dbmService, migrationService, CookieConfig{Secure: true})

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

	var readers models.Group
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
	if !viewerCookie.Secure || !viewerCookie.HttpOnly || viewerCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("login cookie has unexpected security attributes: %+v", viewerCookie)
	}

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
	var earlierRotatedCookie *http.Cookie

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

	t.Run("periodic rotation preserves absolute expiration and refreshes cookie", func(t *testing.T) {
		oldSessionID := viewerCookie.Value
		earlierRotatedCookie = &http.Cookie{Name: middleware.SessionCookieName, Value: oldSessionID}
		var before models.Session
		if err := internalDB.Where("session_id = ?", oldSessionID).First(&before).Error; err != nil {
			t.Fatalf("find viewer session before rotation: %v", err)
		}
		if err := internalDB.Model(&models.Session{}).
			Where("id = ?", before.ID).
			Update("last_rotated_at", time.Now().UTC().Add(-16*time.Minute)).Error; err != nil {
			t.Fatalf("age session rotation timestamp: %v", err)
		}

		type requestResult struct {
			status  int
			cookies []*http.Cookie
		}
		results := make(chan requestResult, 2)
		var requests sync.WaitGroup
		for i := 0; i < 2; i++ {
			requests.Add(1)
			go func() {
				defer requests.Done()
				req := httptest.NewRequest(http.MethodGet, "/admin", nil)
				req.AddCookie(viewerCookie)
				rec := httptest.NewRecorder()
				app.ServeHTTP(rec, req)
				results <- requestResult{status: rec.Code, cookies: rec.Result().Cookies()}
			}()
		}
		requests.Wait()
		close(results)
		var rotatedCookie *http.Cookie
		for result := range results {
			if result.status != http.StatusForbidden {
				t.Fatalf("concurrent viewer request status = %d, want %d", result.status, http.StatusForbidden)
			}
			for _, cookie := range result.cookies {
				if cookie.Name == middleware.SessionCookieName {
					if rotatedCookie != nil && rotatedCookie.Value != cookie.Value {
						t.Fatalf("concurrent responses returned different session IDs: %q and %q", rotatedCookie.Value, cookie.Value)
					}
					rotatedCookie = cookie
				}
			}
		}
		if rotatedCookie == nil || rotatedCookie.Value == oldSessionID {
			t.Fatalf("concurrent rotation did not issue a new session cookie: %+v", rotatedCookie)
		}
		var after models.Session
		if err := internalDB.Where("session_id = ?", rotatedCookie.Value).First(&after).Error; err != nil {
			t.Fatalf("find periodically rotated session: %v", err)
		}
		if !after.ExpiresAt.Equal(before.ExpiresAt) {
			t.Fatalf("absolute expiration changed from %v to %v", before.ExpiresAt, after.ExpiresAt)
		}
		viewerCookie = rotatedCookie

		// A concurrent request that still carries the old ID receives the current cookie during grace.
		rec := sendAccessControlRequest(t, app, http.MethodGet, schemasPath, nil, &http.Cookie{Name: middleware.SessionCookieName, Value: oldSessionID})
		if rec.Code != http.StatusOK {
			t.Fatalf("old ID during grace status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
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

	t.Run("group permission increase rotates the member session", func(t *testing.T) {
		oldSessionID := viewerCookie.Value
		if earlierRotatedCookie != nil {
			if err := internalDB.Model(&models.SessionAlias{}).
				Where("session_id = ?", earlierRotatedCookie.Value).
				Update("expires_at", time.Now().UTC().Add(time.Minute)).Error; err != nil {
				t.Fatalf("extend earlier grace alias for overlapping-rotation scenario: %v", err)
			}
		}
		var before models.Session
		if err := internalDB.Where("session_id = ?", oldSessionID).First(&before).Error; err != nil {
			t.Fatalf("find viewer session before privilege increase: %v", err)
		}

		rec := sendAccessControlRequest(
			t,
			app,
			http.MethodPost,
			"/admin/groups/permissions",
			map[string]any{
				"group_id":     readers.ID,
				"target_db_id": storedTarget.ID,
				"access_level": accesscontrol.AccessLevelViewEdit,
			},
			adminCookie,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("increase group permission status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var after models.Session
		var currentAlias models.SessionAlias
		if err := internalDB.Where("session_id = ?", oldSessionID).First(&currentAlias).Error; err != nil {
			t.Fatalf("find grace-period alias: %v", err)
		}
		if err := internalDB.First(&after, currentAlias.SessionRecordID).Error; err != nil {
			t.Fatalf("find rotated viewer session: %v", err)
		}
		if after.SessionID == oldSessionID {
			t.Fatal("session ID did not change after privilege increase")
		}
		if !after.ExpiresAt.Equal(before.ExpiresAt) {
			t.Fatalf("absolute expiration changed from %v to %v", before.ExpiresAt, after.ExpiresAt)
		}

		// Browser still presents the old ID: it remains accepted during grace and receives the current ID.
		rec = sendAccessControlRequest(t, app, http.MethodGet, schemasPath, nil, viewerCookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("old session ID during grace status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Name == middleware.SessionCookieName && cookie.Value == after.SessionID {
				viewerCookie = cookie
				break
			}
		}
		if viewerCookie.Value != after.SessionID {
			t.Fatal("response did not refresh the rotated session cookie")
		}
		if earlierRotatedCookie != nil {
			rec = sendAccessControlRequest(t, app, http.MethodGet, schemasPath, nil, earlierRotatedCookie)
			if rec.Code != http.StatusOK {
				t.Fatalf("earlier ID still within grace was rejected: status=%d body=%s", rec.Code, rec.Body.String())
			}
			foundCurrentCookie := false
			for _, cookie := range rec.Result().Cookies() {
				if cookie.Name == middleware.SessionCookieName && cookie.Value == after.SessionID {
					foundCurrentCookie = true
				}
			}
			if !foundCurrentCookie {
				t.Fatal("response using an earlier grace ID did not refresh to the current session ID")
			}
		}
	})

	t.Run("permission downgrade revokes member sessions", func(t *testing.T) {
		viewerCookie = loginThroughApp(t, app, "viewer", "viewer-password")
		rec := sendAccessControlRequest(
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
			t.Fatalf("downgrade group permission status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		rec = sendAccessControlRequest(t, app, http.MethodGet, schemasPath, nil, viewerCookie)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("request after permission downgrade status = %d, want %d; body: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	})

	t.Run("logout revokes session used by middleware", func(t *testing.T) {
		viewerCookie = loginThroughApp(t, app, "viewer", "viewer-password")
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
