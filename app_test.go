package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"wbd/internal/middleware"
	"wbd/internal/models"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/interfaces"
)

type mockAccessControlService struct {
	interfaces.AccessControlService
	registerFunc          func(ctx context.Context, username, password string) error
	loginFunc             func(ctx context.Context, username, password string) (string, error)
	logoutFunc            func(ctx context.Context, sessionID string) error
	validateSessionFunc   func(ctx context.Context, sessionID string) (int, error)
	listUsersFunc         func(ctx context.Context) ([]models.UserSummary, error)
	listGroupsFunc        func(ctx context.Context) ([]models.GroupSummary, error)
	isAdminFunc           func(ctx context.Context, userID int) (bool, error)
	evaluateDBAccessFunc  func(ctx context.Context, userID, targetDBID int, requiredLevel string) error
	createGroupFunc       func(ctx context.Context, groupName string) error
	assignUserToGroupFunc func(ctx context.Context, userID, groupID int) error
	setGroupDBAccessFunc  func(ctx context.Context, groupID, targetDBID int, accessLevel string) error
}

func (m *mockAccessControlService) Register(ctx context.Context, username, password string) error {
	return m.registerFunc(ctx, username, password)
}

func (m *mockAccessControlService) Login(ctx context.Context, username, password string) (string, error) {
	return m.loginFunc(ctx, username, password)
}

func (m *mockAccessControlService) Logout(ctx context.Context, sessionID string) error {
	return m.logoutFunc(ctx, sessionID)
}

func (m *mockAccessControlService) ValidateSession(ctx context.Context, sessionID string) (int, error) {
	if m.validateSessionFunc != nil {
		return m.validateSessionFunc(ctx, sessionID)
	}
	return 0, nil
}

func (m *mockAccessControlService) ListUsers(ctx context.Context) ([]models.UserSummary, error) {
	if m.listUsersFunc == nil {
		return nil, nil
	}
	return m.listUsersFunc(ctx)
}

func (m *mockAccessControlService) ListGroups(ctx context.Context) ([]models.GroupSummary, error) {
	if m.listGroupsFunc == nil {
		return nil, nil
	}
	return m.listGroupsFunc(ctx)
}

func (m *mockAccessControlService) IsAdmin(ctx context.Context, userID int) (bool, error) {
	return m.isAdminFunc(ctx, userID)
}

func (m *mockAccessControlService) EvaluateDBAccess(ctx context.Context, userID, targetDBID int, requiredLevel string) error {
	return m.evaluateDBAccessFunc(ctx, userID, targetDBID, requiredLevel)
}

func (m *mockAccessControlService) CreateGroup(ctx context.Context, groupName string) error {
	return m.createGroupFunc(ctx, groupName)
}

func (m *mockAccessControlService) AssignUserToGroup(ctx context.Context, userID, groupID int) error {
	return m.assignUserToGroupFunc(ctx, userID, groupID)
}

func (m *mockAccessControlService) SetGroupDBAccess(ctx context.Context, groupID, targetDBID int, accessLevel string) error {
	return m.setGroupDBAccessFunc(ctx, groupID, targetDBID, accessLevel)
}

type mockDBManagementService struct {
	interfaces.DBManagementService
	registerTargetDBFunc func(ctx context.Context, info models.TargetDBInfo) (int, error)
	listDatabasesFunc    func(ctx context.Context) ([]models.TargetDBInfo, error)
	getSchemasFunc       func(ctx context.Context, targetDBID int) ([]string, error)
	getTablesFunc        func(ctx context.Context, targetDBID int, schema string) ([]string, error)
	getTableDetailsFunc  func(ctx context.Context, targetDBID int, schema, tableName string) (*models.TableMetadata, error)
	getTableDataFunc     func(ctx context.Context, targetDBID int, schema, tableName string, limit, offset int) ([]map[string]any, error)
}

func (m *mockDBManagementService) RegisterTargetDB(ctx context.Context, info models.TargetDBInfo) (int, error) {
	return m.registerTargetDBFunc(ctx, info)
}

func (m *mockDBManagementService) ListDatabases(ctx context.Context) ([]models.TargetDBInfo, error) {
	if m.listDatabasesFunc != nil {
		return m.listDatabasesFunc(ctx)
	}
	return nil, nil
}

func (m *mockDBManagementService) GetTargetConnection(ctx context.Context, targetDBID int) (*sql.DB, error) {
	return nil, nil
}

func (m *mockDBManagementService) GetSchemas(ctx context.Context, targetDBID int) ([]string, error) {
	return m.getSchemasFunc(ctx, targetDBID)
}

func (m *mockDBManagementService) GetTables(ctx context.Context, targetDBID int, schema string) ([]string, error) {
	return m.getTablesFunc(ctx, targetDBID, schema)
}

func (m *mockDBManagementService) GetTableDetails(ctx context.Context, targetDBID int, schema, tableName string) (*models.TableMetadata, error) {
	return m.getTableDetailsFunc(ctx, targetDBID, schema, tableName)
}

func (m *mockDBManagementService) GetTableData(ctx context.Context, targetDBID int, schema, tableName string, limit, offset int) ([]map[string]any, error) {
	return m.getTableDataFunc(ctx, targetDBID, schema, tableName, limit, offset)
}

func (m *mockDBManagementService) CloseAllTargetConnections() {}

type mockMigrationService struct {
	interfaces.MigrationService
	createDraftFunc        func(ctx context.Context, targetDBID int, sqlQuery string) (int, error)
	updateDraftFunc        func(ctx context.Context, targetDBID, migrationID int, sqlQuery string) error
	getMigrationDetailFunc func(ctx context.Context, targetDBID, migrationID int) (*models.MigrationRecord, error)
	getDraftsFunc          func(ctx context.Context, targetDBID int) ([]models.MigrationRecord, error)
	getLinearHistoryFunc   func(ctx context.Context, targetDBID int) ([]models.MigrationRecord, error)
	executeMigrationFunc   func(ctx context.Context, targetDBID, migrationID int, token string) (*models.MigrationRecord, error)
}

func (m *mockMigrationService) CreateDraft(ctx context.Context, targetDBID int, sqlQuery string) (int, error) {
	return m.createDraftFunc(ctx, targetDBID, sqlQuery)
}

func (m *mockMigrationService) UpdateDraft(ctx context.Context, targetDBID, migrationID int, sqlQuery string) error {
	return m.updateDraftFunc(ctx, targetDBID, migrationID, sqlQuery)
}

func (m *mockMigrationService) GetMigrationDetail(ctx context.Context, targetDBID, migrationID int) (*models.MigrationRecord, error) {
	return m.getMigrationDetailFunc(ctx, targetDBID, migrationID)
}

func (m *mockMigrationService) GetDrafts(ctx context.Context, targetDBID int) ([]models.MigrationRecord, error) {
	return m.getDraftsFunc(ctx, targetDBID)
}

func (m *mockMigrationService) GetLinearHistory(ctx context.Context, targetDBID int) ([]models.MigrationRecord, error) {
	return m.getLinearHistoryFunc(ctx, targetDBID)
}

func (m *mockMigrationService) ExecuteMigration(ctx context.Context, targetDBID, migrationID int, token string) (*models.MigrationRecord, error) {
	return m.executeMigrationFunc(ctx, targetDBID, migrationID, token)
}

func newJSONRequest(t *testing.T, method, target string, payload any) *http.Request {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestAccessControlHandlers(t *testing.T) {
	mockAC := &mockAccessControlService{}
	app := NewApp(mockAC, &mockDBManagementService{}, nil)

	t.Run("LoginPage", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		rec := httptest.NewRecorder()
		app.handleLoginPage(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `<form method="POST" action="/login">`) {
			t.Errorf("expected login form in HTML, got: %s", rec.Body.String())
		}
	})

	t.Run("Login", func(t *testing.T) {
		mockAC.loginFunc = func(ctx context.Context, username, password string) (string, error) {
			if username != "admin" || password != "secret" {
				t.Errorf("unexpected credentials: (%s, %s)", username, password)
			}
			return "sess-123", nil
		}

		req := newJSONRequest(t, http.MethodPost, "/login", map[string]any{
			"username": "admin",
			"password": "secret",
		})
		rec := httptest.NewRecorder()
		app.handleLogin(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected status 303 redirect, got %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/dashboard" {
			t.Errorf("expected redirect to /dashboard, got %s", loc)
		}
		cookies := rec.Result().Cookies()
		if len(cookies) == 0 || cookies[0].Name != "session_id" || cookies[0].Value != "sess-123" {
			t.Errorf("expected session_id cookie sess-123, got %+v", cookies)
		}
	})

	t.Run("LoginFromBrowserForm", func(t *testing.T) {
		mockAC.loginFunc = func(ctx context.Context, username, password string) (string, error) {
			if username != "admin" || password != "secret" {
				t.Errorf("unexpected form credentials: (%s, %s)", username, password)
			}
			return "form-session", nil
		}
		body := url.Values{"username": {"admin"}, "password": {"secret"}}.Encode()
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard" {
			t.Errorf("expected login redirect to dashboard, got code=%d location=%s", rec.Code, rec.Header().Get("Location"))
		}
		cookies := rec.Result().Cookies()
		if len(cookies) == 0 || cookies[0].Name != "session_id" || cookies[0].Value != "form-session" {
			t.Errorf("expected session cookie from browser form, got %+v", cookies)
		}
	})

	t.Run("Logout", func(t *testing.T) {
		mockAC.logoutFunc = func(ctx context.Context, sessionID string) error {
			if sessionID != "sess-123" {
				t.Errorf("expected sessionID sess-123, got %s", sessionID)
			}
			return nil
		}

		req := httptest.NewRequest(http.MethodPost, "/logout", nil)
		req.AddCookie(&http.Cookie{Name: "session_id", Value: "sess-123"})
		rec := httptest.NewRecorder()
		app.handleLogout(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected status 303 redirect, got %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/login" {
			t.Errorf("expected redirect to /login, got %s", loc)
		}
	})

	t.Run("RegisterUser", func(t *testing.T) {
		mockAC.registerFunc = func(ctx context.Context, username, password string) error {
			if username != "alice" || password != "pass123" {
				t.Errorf("unexpected register args: (%s, %s)", username, password)
			}
			return nil
		}

		req := newJSONRequest(t, http.MethodPost, "/admin/users", map[string]any{
			"username": "alice",
			"password": "pass123",
		})
		rec := httptest.NewRecorder()
		app.handleRegisterUser(rec, req)

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "berhasil menambahkan alice") {
			t.Errorf("expected 200 OK with success message on admin page, got code=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("CreateGroup", func(t *testing.T) {
		mockAC.createGroupFunc = func(ctx context.Context, groupName string) error {
			if groupName != "analysts" {
				t.Errorf("expected groupName analysts, got %s", groupName)
			}
			return nil
		}

		req := newJSONRequest(t, http.MethodPost, "/admin/groups", map[string]any{
			"group_name": "analysts",
		})
		rec := httptest.NewRecorder()
		app.handleCreateGroup(rec, req)

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "berhasil menambahkan group analysts") {
			t.Errorf("expected 200 OK with success message on admin page, got code=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("AssignUserToGroup", func(t *testing.T) {
		mockAC.assignUserToGroupFunc = func(ctx context.Context, userID, groupID int) error {
			if userID != 2 || groupID != 3 {
				t.Errorf("expected (2, 3), got (%d, %d)", userID, groupID)
			}
			return nil
		}

		req := newJSONRequest(t, http.MethodPost, "/admin/groups/members", map[string]any{
			"user_id":  2,
			"group_id": 3,
		})
		rec := httptest.NewRecorder()
		app.handleAssignUserToGroup(rec, req)

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "berhasil memasukkan user_id 2 ke group_id 3") {
			t.Errorf("expected 200 OK with success message on admin page, got code=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("SetGroupDBAccess", func(t *testing.T) {
		mockAC.setGroupDBAccessFunc = func(ctx context.Context, groupID, targetDBID int, accessLevel string) error {
			if groupID != 3 || targetDBID != 1 || accessLevel != "VIEW_EDIT" {
				t.Errorf("expected (3, 1, VIEW_EDIT), got (%d, %d, %s)", groupID, targetDBID, accessLevel)
			}
			return nil
		}

		req := newJSONRequest(t, http.MethodPost, "/admin/groups/permissions", map[string]any{
			"group_id":     3,
			"target_db_id": 1,
			"access_level": "VIEW_EDIT",
		})
		rec := httptest.NewRecorder()
		app.handleSetGroupDBAccess(rec, req)

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "berhasil meng-set hak akses group_id 3 pada target_db_id 1 menjadi VIEW_EDIT") {
			t.Errorf("expected 200 OK with success message on admin page, got code=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestBrowserEntryAndFeedback(t *testing.T) {
	ac := &mockAccessControlService{}
	app := NewApp(ac, nil, nil)

	t.Run("root redirects to login without session", func(t *testing.T) {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Fatalf("root redirect: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("root redirects to dashboard with session", func(t *testing.T) {
		ac.validateSessionFunc = func(_ context.Context, token string) (int, error) {
			if token != "valid-session" {
				t.Errorf("unexpected token %q", token)
			}
			return 7, nil
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "valid-session"})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard" {
			t.Fatalf("root redirect: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("login error stays on form without showing password", func(t *testing.T) {
		ac.loginFunc = func(context.Context, string, string) (string, error) {
			return "", errors.New("invalid credentials")
		}
		form := url.Values{"username": {"alice"}, "password": {"very-secret-password"}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusUnauthorized || !strings.Contains(body, `role="alert"`) || !strings.Contains(body, `value="alice"`) || strings.Contains(body, "very-secret-password") {
			t.Fatalf("login feedback: status=%d body=%s", rec.Code, body)
		}
	})

	t.Run("stylesheet is served", func(t *testing.T) {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/css") || !strings.Contains(rec.Body.String(), ".card") {
			t.Fatalf("stylesheet: status=%d type=%q", rec.Code, rec.Header().Get("Content-Type"))
		}
	})
}

func TestDBManagementHandlers(t *testing.T) {
	mockAC := &mockAccessControlService{}
	mockDBM := &mockDBManagementService{}
	app := NewApp(mockAC, mockDBM, nil)

	t.Run("RegisterTargetDB", func(t *testing.T) {
		mockDBM.registerTargetDBFunc = func(ctx context.Context, info models.TargetDBInfo) (int, error) {
			if info.Name != "production_pg" || info.DBType != "postgres" || info.Port != 5432 || info.DBName != "prod_db" {
				t.Errorf("unexpected TargetDBInfo: %+v", info)
			}
			return 1, nil
		}

		req := newJSONRequest(t, http.MethodPost, "/admin/databases", models.TargetDBInfo{
			Name:     "production_pg",
			DBType:   "postgres",
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "secret",
			DBName:   "prod_db",
		})
		rec := httptest.NewRecorder()
		app.handleRegisterTargetDB(rec, req)

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "DB ditambahkan dengan id 1") {
			t.Errorf("expected 200 OK with success message on admin page, got code=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("Dashboard", func(t *testing.T) {
		mockAC.validateSessionFunc = func(_ context.Context, sessionID string) (int, error) {
			if sessionID != "viewer-session" {
				t.Errorf("unexpected dashboard session: %q", sessionID)
			}
			return 7, nil
		}
		mockAC.isAdminFunc = func(_ context.Context, userID int) (bool, error) {
			if userID != 7 {
				t.Errorf("unexpected dashboard user: %d", userID)
			}
			return false, nil
		}
		mockAC.evaluateDBAccessFunc = func(_ context.Context, userID, dbID int, level string) error {
			if userID != 7 || level != accesscontrol.AccessLevelViewOnly {
				t.Errorf("unexpected access check: user=%d level=%s", userID, level)
			}
			if dbID == 1 {
				return nil
			}
			return accesscontrol.ErrDBAccessDenied
		}
		mockDBM.listDatabasesFunc = func(ctx context.Context) ([]models.TargetDBInfo, error) {
			return []models.TargetDBInfo{
				{
					ID:     1,
					Name:   "production_pg",
					DBType: "postgres",
					Host:   "localhost",
					Port:   5432,
					User:   "postgres",
					DBName: "prod_db",
				},
				{
					ID:     2,
					Name:   "analytics_pg",
					DBType: "postgres",
					Host:   "localhost",
					Port:   5432,
					User:   "postgres",
					DBName: "analytics_db",
				},
			}, nil
		}

		req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "viewer-session"})
		rec := httptest.NewRecorder()
		app.handleDashboard(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "production_pg") || !strings.Contains(body, "/databases/1/schemas") || !strings.Contains(body, "/databases/1/migrations") {
			t.Errorf("expected production_pg, /databases/1/schemas, and /databases/1/migrations, got:\n%s", body)
		}
		if strings.Contains(body, "analytics_pg") || strings.Contains(body, "/databases/2/") || strings.Contains(body, "/admin") {
			t.Errorf("dashboard exposed inaccessible database or admin link: %s", body)
		}
	})

	t.Run("AdminDashboardWithoutDatabaseGrant", func(t *testing.T) {
		mockAC.isAdminFunc = func(_ context.Context, userID int) (bool, error) {
			return userID == 7, nil
		}
		mockAC.evaluateDBAccessFunc = func(context.Context, int, int, string) error {
			return accesscontrol.ErrDBAccessDenied
		}
		req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "viewer-session"})
		rec := httptest.NewRecorder()
		app.handleDashboard(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("admin dashboard: status=%d body=%s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `href="/admin"`) || !strings.Contains(body, "Belum ada database") {
			t.Errorf("admin link or empty state missing: %s", body)
		}
		if strings.Contains(body, "production_pg") || strings.Contains(body, "analytics_pg") {
			t.Errorf("platform admin was given database access without a group grant: %s", body)
		}
	})

	t.Run("DatabaseRedirect", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/databases/1", nil)
		req.SetPathValue("dbID", "1")
		rec := httptest.NewRecorder()
		app.handleDatabaseRedirect(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected 303 redirect, got %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/databases/1/schemas" {
			t.Errorf("expected redirect to /databases/1/schemas, got %s", loc)
		}
	})

	t.Run("GetSchemas", func(t *testing.T) {
		mockDBM.getSchemasFunc = func(ctx context.Context, targetDBID int) ([]string, error) {
			if targetDBID != 1 {
				t.Errorf("expected targetDBID 1, got %d", targetDBID)
			}
			return []string{"public", "analytics"}, nil
		}

		req := httptest.NewRequest(http.MethodGet, "/databases/1/schemas", nil)
		req.SetPathValue("dbID", "1")
		rec := httptest.NewRecorder()
		app.handleGetSchemas(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `public`) || !strings.Contains(body, `analytics`) {
			t.Errorf("wrong schemas, got:\n %s", body)
		}
	})

	t.Run("GetTables", func(t *testing.T) {
		mockDBM.getTablesFunc = func(ctx context.Context, targetDBID int, schema string) ([]string, error) {
			if targetDBID != 1 || schema != "public" {
				t.Errorf("expected (1, public), got (%d, %s)", targetDBID, schema)
			}
			return []string{"users", "orders"}, nil
		}

		req := httptest.NewRequest(http.MethodGet, "/databases/1/schemas/public/tables", nil)
		req.SetPathValue("dbID", "1")
		req.SetPathValue("schema", "public")
		rec := httptest.NewRecorder()
		app.handleGetTables(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `users`) || !strings.Contains(body, `order`) {
			t.Errorf("wrong tables, got\n %s", body)
		}
	})

	t.Run("GetTableDetails", func(t *testing.T) {
		mockDBM.getTableDetailsFunc = func(ctx context.Context, targetDBID int, schema, tableName string) (*models.TableMetadata, error) {
			if targetDBID != 1 || schema != "public" || tableName != "users" {
				t.Errorf("expected (1, public, users), got (%d, %s, %s)", targetDBID, schema, tableName)
			}
			return &models.TableMetadata{
				Schema:    "public",
				TableName: "users",
				Columns: []models.ColumnInfo{
					{Name: "id", DataType: "integer", IsNullable: false},
					{Name: "email", DataType: "character varying", IsNullable: true},
				},
				Constraints: []models.ConstraintInfo{
					{Name: "users_pkey", Type: "PRIMARY KEY"},
				},
				Indexes: []models.IndexInfo{
					{Name: "users_pkey", IsUnique: true},
				},
			}, nil
		}

		req := httptest.NewRequest(http.MethodGet, "/databases/1/schemas/public/tables/users", nil)
		req.SetPathValue("dbID", "1")
		req.SetPathValue("schema", "public")
		req.SetPathValue("table", "users")
		rec := httptest.NewRecorder()
		app.handleGetTableDetails(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `public`) || !strings.Contains(body, `users`) || !strings.Contains(body, `id`) || !strings.Contains(body, `integer`) || !strings.Contains(body, `email`) || !strings.Contains(body, `character varying`) || !strings.Contains(body, `users_pkey`) || !strings.Contains(body, `PRIMARY KEY`) {
			t.Errorf("wrong table details, got:\n %s", body)
		}
	})

	t.Run("GetTableData", func(t *testing.T) {
		mockDBM.getTableDataFunc = func(ctx context.Context, targetDBID int, schema, tableName string, limit, offset int) ([]map[string]any, error) {
			if targetDBID != 1 || schema != "public" || tableName != "users" || limit != 10 || offset != 0 {
				t.Errorf("unexpected args: (%d, %s, %s, %d, %d)", targetDBID, schema, tableName, limit, offset)
			}
			return []map[string]any{
				{"id": 1, "email": "alice@example.com"},
			}, nil
		}

		req := httptest.NewRequest(http.MethodGet, "/databases/1/schemas/public/tables/users/data?limit=10&offset=0", nil)
		req.SetPathValue("dbID", "1")
		req.SetPathValue("schema", "public")
		req.SetPathValue("table", "users")
		rec := httptest.NewRecorder()
		app.handleGetTableData(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `<th scope="col">email</th>`) || !strings.Contains(body, `<th scope="col">id</th>`) || !strings.Contains(body, `<td>alice@example.com</td>`) {
			t.Errorf("wrong table data, got:\n %s", body)
		}
	})
}

func TestAdminBrowserForms(t *testing.T) {
	mockAC := &mockAccessControlService{}
	mockDBM := &mockDBManagementService{
		listDatabasesFunc: func(context.Context) ([]models.TargetDBInfo, error) {
			return []models.TargetDBInfo{{ID: 1, Name: "production", DBType: "postgres", Host: "localhost", Port: 5432, DBName: "prod_db"}}, nil
		},
	}
	app := NewApp(mockAC, mockDBM, nil)
	mockAC.listUsersFunc = func(context.Context) ([]models.UserSummary, error) {
		return []models.UserSummary{{ID: 2, Username: "alice"}}, nil
	}
	mockAC.listGroupsFunc = func(context.Context) ([]models.GroupSummary, error) {
		return []models.GroupSummary{{ID: 3, Name: "analysts"}}, nil
	}

	mockAC.registerFunc = func(_ context.Context, username, password string) error {
		if username != "alice" || password != "pass123" {
			t.Errorf("unexpected registered user: %q / %q", username, password)
		}
		return nil
	}
	mockAC.createGroupFunc = func(_ context.Context, name string) error {
		if name != "analysts" {
			t.Errorf("unexpected group name: %q", name)
		}
		return nil
	}
	mockAC.assignUserToGroupFunc = func(_ context.Context, userID, groupID int) error {
		if userID != 2 || groupID != 3 {
			t.Errorf("unexpected group member: user=%d group=%d", userID, groupID)
		}
		return nil
	}
	mockAC.setGroupDBAccessFunc = func(_ context.Context, groupID, dbID int, level string) error {
		if groupID != 3 || dbID != 1 || level != "VIEW_EDIT" {
			t.Errorf("unexpected database access: group=%d db=%d level=%q", groupID, dbID, level)
		}
		return nil
	}
	mockDBM.registerTargetDBFunc = func(_ context.Context, info models.TargetDBInfo) (int, error) {
		if info.Name != "production" || info.DBType != "postgres" || info.Host != "localhost" || info.Port != 5432 || info.User != "postgres" || info.Password != "secret" || info.DBName != "prod_db" {
			t.Errorf("unexpected database registration: %+v", info)
		}
		return 1, nil
	}

	tests := []struct {
		name    string
		path    string
		form    url.Values
		handler http.HandlerFunc
		message string
	}{
		{"register user", "/admin/users", url.Values{"username": {"alice"}, "password": {"pass123"}}, app.handleRegisterUser, "berhasil menambahkan alice"},
		{"create group", "/admin/groups", url.Values{"group_name": {"analysts"}}, app.handleCreateGroup, "berhasil menambahkan group analysts"},
		{"assign member", "/admin/groups/members", url.Values{"user_id": {"2"}, "group_id": {"3"}}, app.handleAssignUserToGroup, "berhasil memasukkan user_id 2"},
		{"set access", "/admin/groups/permissions", url.Values{"group_id": {"3"}, "target_db_id": {"1"}, "access_level": {"VIEW_EDIT"}}, app.handleSetGroupDBAccess, "berhasil meng-set hak akses"},
		{"register database", "/admin/databases", url.Values{"name": {"production"}, "db_type": {"postgres"}, "host": {"localhost"}, "port": {"5432"}, "user": {"postgres"}, "password": {"secret"}, "db_name": {"prod_db"}}, app.handleRegisterTargetDB, "DB ditambahkan dengan id 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			tt.handler(rec, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tt.message) {
				t.Fatalf("form response: status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("invalid numeric field", func(t *testing.T) {
		form := url.Values{"user_id": {"oops"}, "group_id": {"3"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/groups/members", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		app.handleAssignUserToGroup(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid numeric field, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, `value="3" selected`) || !strings.Contains(body, "Pilih user dan group") {
			t.Errorf("expected inline error and preserved group choice, got %s", body)
		}
	})

	t.Run("failed database form does not echo password", func(t *testing.T) {
		mockDBM.registerTargetDBFunc = func(context.Context, models.TargetDBInfo) (int, error) {
			return 0, errors.New("connection refused")
		}
		form := url.Values{"name": {"production"}, "db_type": {"postgres"}, "host": {"localhost"}, "port": {"5432"}, "user": {"postgres"}, "password": {"very-secret-password"}, "db_name": {"prod_db"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/databases", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		app.handleRegisterTargetDB(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusInternalServerError || !strings.Contains(body, `role="alert"`) || !strings.Contains(body, `value="production"`) || strings.Contains(body, "very-secret-password") {
			t.Fatalf("database form feedback: status=%d body=%s", rec.Code, body)
		}
	})
}

func TestMigrationFormErrorKeepsSQL(t *testing.T) {
	ac := &mockAccessControlService{}
	mig := &mockMigrationService{}
	app := NewApp(ac, nil, mig)
	ac.validateSessionFunc = func(context.Context, string) (int, error) { return 7, nil }
	ac.evaluateDBAccessFunc = func(context.Context, int, int, string) error { return nil }
	mig.createDraftFunc = func(context.Context, int, string) (int, error) { return 0, errors.New("database unavailable") }
	mig.getLinearHistoryFunc = func(context.Context, int) ([]models.MigrationRecord, error) { return nil, nil }
	mig.getDraftsFunc = func(context.Context, int) ([]models.MigrationRecord, error) { return nil, nil }

	sql := "CREATE TABLE example (id INT);"
	req := httptest.NewRequest(http.MethodPost, "/databases/1/migrations", strings.NewReader(url.Values{"sql_query": {sql}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "editor-session"})
	req.SetPathValue("dbID", "1")
	rec := httptest.NewRecorder()
	app.handleCreateMigrationDraft(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), sql) || !strings.Contains(rec.Body.String(), `role="alert"`) {
		t.Fatalf("migration form feedback: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminEntityListPages(t *testing.T) {
	mockAC := &mockAccessControlService{
		validateSessionFunc: func(context.Context, string) (int, error) { return 7, nil },
		isAdminFunc:         func(context.Context, int) (bool, error) { return true, nil },
		listUsersFunc: func(context.Context) ([]models.UserSummary, error) {
			return []models.UserSummary{{ID: 4, Username: "alice", IsAdmin: false}}, nil
		},
		listGroupsFunc: func(context.Context) ([]models.GroupSummary, error) {
			return []models.GroupSummary{{ID: 2, Name: "analysts"}}, nil
		},
	}
	mockDBM := &mockDBManagementService{
		listDatabasesFunc: func(context.Context) ([]models.TargetDBInfo, error) {
			return []models.TargetDBInfo{{
				ID: 9, Name: "analytics", DBType: "postgres", Host: "db.internal", Port: 5432,
				User: "db-admin", Password: "do-not-display-this", DBName: "analytics_db",
			}}, nil
		},
	}
	app := NewApp(mockAC, mockDBM, nil)

	tests := []struct {
		path        string
		wantText    []string
		mustNotHave []string
	}{
		{path: "/admin/users", wantText: []string{"alice", "<td>4</td>", "Platform role"}, mustNotHave: []string{"do-not-display-this", "password_hash"}},
		{path: "/admin/groups", wantText: []string{"analysts", "<td>2</td>", "Group list"}, mustNotHave: []string{"do-not-display-this"}},
		{path: "/admin/databases", wantText: []string{"analytics", "<td>9</td>", "db.internal", "analytics_db"}, mustNotHave: []string{"do-not-display-this", "db-admin"}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "admin-session"})
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			for _, text := range tt.wantText {
				if !strings.Contains(rec.Body.String(), text) {
					t.Errorf("expected %q in page, got: %s", text, rec.Body.String())
				}
			}
			for _, text := range tt.mustNotHave {
				if strings.Contains(rec.Body.String(), text) {
					t.Errorf("page must not include %q", text)
				}
			}
		})
	}

	t.Run("admin page links to each list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "admin-session"})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		for _, href := range []string{`href="/admin/users"`, `href="/admin/groups"`, `href="/admin/databases"`} {
			if !strings.Contains(rec.Body.String(), href) {
				t.Errorf("expected admin link %s", href)
			}
		}
		for _, option := range []string{`value="4"`, `alice (User #4)`, `analysts (Group #2)`, `analytics (Database #9)`} {
			if !strings.Contains(rec.Body.String(), option) {
				t.Errorf("expected named assignment option %q", option)
			}
		}
	})

	t.Run("non-admin cannot open lists", func(t *testing.T) {
		mockAC.isAdminFunc = func(context.Context, int) (bool, error) { return false, nil }
		for _, path := range []string{"/admin/users", "/admin/groups", "/admin/databases"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "admin-session"})
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("expected 403 for %s, got %d: %s", path, rec.Code, rec.Body.String())
			}
		}
	})
}

func TestMigrationHandlers(t *testing.T) {
	mockAC := &mockAccessControlService{}
	mockMig := &mockMigrationService{}
	app := NewApp(mockAC, nil, mockMig)
	mockAC.validateSessionFunc = func(_ context.Context, sessionID string) (int, error) {
		if sessionID != "viewer-session" {
			t.Errorf("unexpected migration session: %q", sessionID)
		}
		return 7, nil
	}
	mockAC.evaluateDBAccessFunc = func(_ context.Context, userID, dbID int, level string) error {
		if userID != 7 || dbID != 1 || level != accesscontrol.AccessLevelViewEdit {
			t.Errorf("unexpected migration edit check: user=%d db=%d level=%s", userID, dbID, level)
		}
		return accesscontrol.ErrDBAccessDenied
	}

	t.Run("GetMigrationHistory", func(t *testing.T) {
		mockMig.getLinearHistoryFunc = func(ctx context.Context, targetDBID int) ([]models.MigrationRecord, error) {
			if targetDBID != 1 {
				t.Errorf("expected targetDBID 1, got %d", targetDBID)
			}
			return []models.MigrationRecord{
				{ID: 1, TargetDBID: 1, Version: 1, SQLQuery: "CREATE TABLE users (id INT);", Status: "APPLIED"},
				{ID: 2, TargetDBID: 1, Version: 2, SQLQuery: "ALTER TABLE users ADD email TEXT;", Status: "DRAFT"},
			}, nil
		}

		req := httptest.NewRequest(http.MethodGet, "/databases/1/migrations", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "viewer-session"})
		req.SetPathValue("dbID", "1")
		rec := httptest.NewRecorder()
		app.handleGetMigrationHistory(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `CREATE TABLE users (id INT);`) || !strings.Contains(body, `APPLIED`) || !strings.Contains(body, `ALTER TABLE users ADD email TEXT;`) || !strings.Contains(body, `DRAFT`) {
			t.Errorf("wrong migration history, got:\n %s", body)
		}
		if strings.Contains(body, "Create Draft") {
			t.Errorf("view-only user should not see draft creation: %s", body)
		}
		if strings.Contains(body, "<h2>Drafts</h2>") {
			t.Errorf("view-only user should not see draft list: %s", body)
		}
	})

	t.Run("GetMigrationDetail", func(t *testing.T) {
		mockMig.getMigrationDetailFunc = func(ctx context.Context, targetDBID, migrationID int) (*models.MigrationRecord, error) {
			if targetDBID != 1 || migrationID != 2 {
				t.Errorf("expected (1, 2), got (%d, %d)", targetDBID, migrationID)
			}
			return &models.MigrationRecord{
				ID:         2,
				TargetDBID: 1,
				Version:    2,
				SQLQuery:   "ALTER TABLE users ADD email TEXT;",
				Status:     "DRAFT",
				ExecutedBy: "alice",
			}, nil
		}

		req := httptest.NewRequest(http.MethodGet, "/databases/1/migrations/2", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "viewer-session"})
		req.SetPathValue("dbID", "1")
		req.SetPathValue("migrationID", "2")
		rec := httptest.NewRecorder()
		app.handleGetMigrationDetail(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `2`) || !strings.Contains(body, `ALTER TABLE users ADD email TEXT;`) || !strings.Contains(body, `DRAFT`) || !strings.Contains(body, `alice`) {
			t.Errorf("wrong migration detail, got:\n %s", body)
		}
		if strings.Contains(body, `name="token"`) {
			t.Errorf("execute form must not ask for an execution token, got:\n%s", body)
		}
		if strings.Contains(body, "Edit Draft") || strings.Contains(body, "Execute Migration") {
			t.Errorf("view-only user should not see migration actions: %s", body)
		}
	})

	t.Run("MigrationEditorControls", func(t *testing.T) {
		mockAC.evaluateDBAccessFunc = func(_ context.Context, userID, dbID int, level string) error {
			if userID != 7 || dbID != 1 || level != accesscontrol.AccessLevelViewEdit {
				t.Errorf("unexpected editor access check: user=%d db=%d level=%s", userID, dbID, level)
			}
			return nil
		}
		mockMig.getDraftsFunc = func(_ context.Context, targetDBID int) ([]models.MigrationRecord, error) {
			if targetDBID != 1 {
				t.Errorf("unexpected draft target: %d", targetDBID)
			}
			return []models.MigrationRecord{{ID: 2, TargetDBID: 1, SQLQuery: "ALTER TABLE users ADD email TEXT;", Status: "DRAFT"}}, nil
		}
		historyReq := httptest.NewRequest(http.MethodGet, "/databases/1/migrations", nil)
		historyReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "viewer-session"})
		historyReq.SetPathValue("dbID", "1")
		historyRec := httptest.NewRecorder()
		app.handleGetMigrationHistory(historyRec, historyReq)
		if historyRec.Code != http.StatusOK || !strings.Contains(historyRec.Body.String(), "Create Draft") || !strings.Contains(historyRec.Body.String(), "Draft #2") {
			t.Errorf("editor should see draft creation and existing drafts: status=%d body=%s", historyRec.Code, historyRec.Body.String())
		}

		detailReq := httptest.NewRequest(http.MethodGet, "/databases/1/migrations/2", nil)
		detailReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "viewer-session"})
		detailReq.SetPathValue("dbID", "1")
		detailReq.SetPathValue("migrationID", "2")
		detailRec := httptest.NewRecorder()
		app.handleGetMigrationDetail(detailRec, detailReq)
		if detailRec.Code != http.StatusOK || !strings.Contains(detailRec.Body.String(), "Edit Draft") || !strings.Contains(detailRec.Body.String(), "Execute Migration") {
			t.Errorf("editor should see draft actions: status=%d body=%s", detailRec.Code, detailRec.Body.String())
		}
	})

	t.Run("CreateMigrationDraft", func(t *testing.T) {
		mockMig.createDraftFunc = func(ctx context.Context, targetDBID int, sqlQuery string) (int, error) {
			if targetDBID != 1 || sqlQuery != "CREATE TABLE orders (id INT);" {
				t.Errorf("unexpected create draft args: (%d, %s)", targetDBID, sqlQuery)
			}
			return 3, nil
		}

		req := newJSONRequest(t, http.MethodPost, "/databases/1/migrations", map[string]any{
			"sql_query": "CREATE TABLE orders (id INT);",
		})
		req.SetPathValue("dbID", "1")
		rec := httptest.NewRecorder()
		app.handleCreateMigrationDraft(rec, req)

		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/1/migrations/3" {
			t.Errorf("expected 303 redirect to /databases/1/migrations/3, got code=%d loc=%s", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("CreateMigrationDraftFromForm", func(t *testing.T) {
		mockMig.createDraftFunc = func(ctx context.Context, targetDBID int, sqlQuery string) (int, error) {
			if targetDBID != 1 || sqlQuery != "CREATE TABLE form_example (id INT);" {
				t.Errorf("unexpected create draft args: (%d, %s)", targetDBID, sqlQuery)
			}
			return 4, nil
		}
		body := url.Values{"sql_query": {"CREATE TABLE form_example (id INT);"}}.Encode()
		req := httptest.NewRequest(http.MethodPost, "/databases/1/migrations", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("dbID", "1")
		rec := httptest.NewRecorder()
		app.handleCreateMigrationDraft(rec, req)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/1/migrations/4" {
			t.Errorf("form draft create: code=%d loc=%s", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("UpdateMigrationDraft", func(t *testing.T) {
		mockMig.updateDraftFunc = func(ctx context.Context, targetDBID, migrationID int, sqlQuery string) error {
			if targetDBID != 1 || migrationID != 3 || sqlQuery != "CREATE TABLE orders (id SERIAL PRIMARY KEY);" {
				t.Errorf("unexpected update draft args: (%d, %d, %s)", targetDBID, migrationID, sqlQuery)
			}
			return nil
		}

		req := newJSONRequest(t, http.MethodPut, "/databases/1/migrations/3", map[string]any{
			"sql_query": "CREATE TABLE orders (id SERIAL PRIMARY KEY);",
		})
		req.SetPathValue("dbID", "1")
		req.SetPathValue("migrationID", "3")
		rec := httptest.NewRecorder()
		app.handleUpdateMigrationDraft(rec, req)

		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/1/migrations/3" {
			t.Errorf("expected 303 redirect to /databases/1/migrations/3, got code=%d loc=%s", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("UpdateMigrationDraftFromForm", func(t *testing.T) {
		mockMig.updateDraftFunc = func(ctx context.Context, targetDBID, migrationID int, sqlQuery string) error {
			if targetDBID != 1 || migrationID != 4 || sqlQuery != "ALTER TABLE form_example ADD name text;" {
				t.Errorf("unexpected update draft args: (%d, %d, %s)", targetDBID, migrationID, sqlQuery)
			}
			return nil
		}
		body := url.Values{"sql_query": {"ALTER TABLE form_example ADD name text;"}}.Encode()
		req := httptest.NewRequest(http.MethodPost, "/databases/1/migrations/4/edit", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("dbID", "1")
		req.SetPathValue("migrationID", "4")
		rec := httptest.NewRecorder()
		app.handleUpdateMigrationDraft(rec, req)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/1/migrations/4" {
			t.Errorf("form draft update: code=%d loc=%s", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("ExecuteMigrationFromSession", func(t *testing.T) {
		mockAC.validateSessionFunc = func(ctx context.Context, sessionID string) (int, error) {
			if sessionID != "valid-session" {
				t.Errorf("unexpected session ID: %s", sessionID)
			}
			return 7, nil
		}
		mockMig.executeMigrationFunc = func(ctx context.Context, targetDBID, migrationID int, executedBy string) (*models.MigrationRecord, error) {
			if targetDBID != 1 || migrationID != 3 || executedBy != "7" {
				t.Errorf("unexpected execute migration args: (%d, %d, %s)", targetDBID, migrationID, executedBy)
			}
			return &models.MigrationRecord{
				ID:         3,
				TargetDBID: 1,
				Version:    3,
				SQLQuery:   "CREATE TABLE orders (id SERIAL PRIMARY KEY);",
				Status:     "APPLIED",
				ExecutedBy: executedBy,
			}, nil
		}

		body := url.Values{"token": {"forged-user"}}.Encode()
		req := httptest.NewRequest(http.MethodPost, "/databases/1/migrations/3/execute", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "valid-session"})
		req.SetPathValue("dbID", "1")
		req.SetPathValue("migrationID", "3")
		rec := httptest.NewRecorder()
		app.handleExecuteMigration(rec, req)

		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/1/migrations/3" {
			t.Errorf("expected 303 redirect to /databases/1/migrations/3, got code=%d loc=%s", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("ExecuteMigrationWithoutSession", func(t *testing.T) {
		mockMig.executeMigrationFunc = func(context.Context, int, int, string) (*models.MigrationRecord, error) {
			t.Fatal("migration must not execute without a session")
			return nil, nil
		}
		req := httptest.NewRequest(http.MethodPost, "/databases/1/migrations/3/execute", nil)
		req.SetPathValue("dbID", "1")
		req.SetPathValue("migrationID", "3")
		rec := httptest.NewRecorder()
		app.handleExecuteMigration(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 without session, got %d", rec.Code)
		}
	})

	t.Run("ExecuteMigrationWithInvalidSession", func(t *testing.T) {
		mockAC.validateSessionFunc = func(context.Context, string) (int, error) {
			return 0, accesscontrol.ErrInvalidSession
		}
		mockMig.executeMigrationFunc = func(context.Context, int, int, string) (*models.MigrationRecord, error) {
			t.Fatal("migration must not execute with an invalid session")
			return nil, nil
		}
		req := httptest.NewRequest(http.MethodPost, "/databases/1/migrations/3/execute", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "invalid-session"})
		req.SetPathValue("dbID", "1")
		req.SetPathValue("migrationID", "3")
		rec := httptest.NewRecorder()
		app.handleExecuteMigration(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for invalid session, got %d", rec.Code)
		}
	})
}
