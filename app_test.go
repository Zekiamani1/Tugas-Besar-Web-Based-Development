package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wbd/internal/middleware"
	"wbd/internal/models"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/interfaces"
)

type mockAccessControlService struct {
	interfaces.AccessControlService
	registerFunc             func(ctx context.Context, username, password string) error
	loginFunc                func(ctx context.Context, username, password string) (string, error)
	logoutFunc               func(ctx context.Context, sessionID string) error
	validateSessionFunc      func(ctx context.Context, sessionID string) (int, error)
	validateWithRotationFunc func(ctx context.Context, sessionID string) (models.SessionValidation, error)
	listUsersFunc            func(ctx context.Context) ([]models.UserSummary, error)
	listGroupsFunc           func(ctx context.Context) ([]models.Group, error)
	isAdminFunc              func(ctx context.Context, userID int) (bool, error)
	evaluateDBAccessFunc     func(ctx context.Context, userID, targetDBID int, requiredLevel string) error
	createGroupFunc          func(ctx context.Context, groupName string) error
	assignUserToGroupFunc    func(ctx context.Context, userID, groupID int) error
	setGroupDBAccessFunc     func(ctx context.Context, groupID, targetDBID int, accessLevel string) error
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

func (m *mockAccessControlService) ValidateSessionWithRotation(ctx context.Context, sessionID string) (models.SessionValidation, error) {
	if m.validateWithRotationFunc != nil {
		return m.validateWithRotationFunc(ctx, sessionID)
	}
	userID, err := m.ValidateSession(ctx, sessionID)
	return models.SessionValidation{UserID: userID, CurrentSessionID: sessionID}, err
}

func (m *mockAccessControlService) ListUsers(ctx context.Context) ([]models.UserSummary, error) {
	if m.listUsersFunc == nil {
		return nil, nil
	}
	return m.listUsersFunc(ctx)
}

func (m *mockAccessControlService) ListGroups(ctx context.Context) ([]models.Group, error) {
	if m.listGroupsFunc == nil {
		return nil, nil
	}
	return m.listGroupsFunc(ctx)
}

func (m *mockAccessControlService) ListGroupMembers(context.Context) ([]models.GroupMemberSummary, error) {
	return nil, nil
}

func (m *mockAccessControlService) ListGroupDBAccess(context.Context) ([]models.GroupDBAccessSummary, error) {
	return nil, nil
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

	t.Run("Health", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("expected 200 JSON, got %d (%q)", rec.Code, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
			t.Errorf("expected ok status JSON, got: %s", rec.Body.String())
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

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") || !strings.Contains(rec.Body.String(), "login successful") {
			t.Errorf("expected JSON login response, got: %s", rec.Body.String())
		}
		cookies := rec.Result().Cookies()
		if len(cookies) == 0 || cookies[0].Name != "session_id" || cookies[0].Value != "sess-123" {
			t.Errorf("expected session_id cookie sess-123, got %+v", cookies)
		} else if !cookies[0].HttpOnly || cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
			t.Errorf("unexpected development session cookie attributes: %+v", cookies[0])
		}
	})

	t.Run("LoginSecureCookieConfiguration", func(t *testing.T) {
		mockAC.loginFunc = func(context.Context, string, string) (string, error) { return "secure-session", nil }
		secureApp := NewAppWithConfig(mockAC, &mockDBManagementService{}, nil, CookieConfig{Secure: true})
		req := newJSONRequest(t, http.MethodPost, "/login", map[string]string{"username": "admin", "password": "secret"})
		rec := httptest.NewRecorder()
		secureApp.handleLogin(rec, req)
		cookies := rec.Result().Cookies()
		if len(cookies) == 0 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
			t.Fatalf("expected secure HttpOnly SameSite=Lax session cookie, got %+v", cookies)
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

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "logged out") {
			t.Fatalf("expected 200 JSON logged out, got %d: %s", rec.Code, rec.Body.String())
		}
		cookies := rec.Result().Cookies()
		if len(cookies) == 0 || cookies[0].Name != middleware.SessionCookieName || cookies[0].MaxAge != -1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
			t.Errorf("unexpected logout cookie attributes: %+v", cookies)
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
			t.Errorf("expected 200 OK JSON with success message, got code=%d body=%s", rec.Code, rec.Body.String())
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
			t.Errorf("expected 200 OK JSON with success message, got code=%d body=%s", rec.Code, rec.Body.String())
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
			t.Errorf("expected 200 OK JSON with success message, got code=%d body=%s", rec.Code, rec.Body.String())
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
			t.Errorf("expected 200 OK JSON with success message, got code=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestHealthAndAuthFeedbackJSON(t *testing.T) {
	ac := &mockAccessControlService{}
	app := NewApp(ac, nil, nil)

	t.Run("health reports ok JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
			t.Fatalf("health json: status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("login error returns JSON without echoing password", func(t *testing.T) {
		ac.loginFunc = func(context.Context, string, string) (string, error) {
			return "", errors.New("invalid credentials")
		}
		req := newJSONRequest(t, http.MethodPost, "/login", map[string]string{
			"username": "alice",
			"password": "very-secret-password",
		})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") || !strings.Contains(body, `"error"`) || strings.Contains(body, "very-secret-password") {
			t.Fatalf("login feedback: status=%d body=%s", rec.Code, body)
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
			t.Errorf("expected 200 OK JSON with success message, got code=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("RegisterTargetDB_MySQL", func(t *testing.T) {
		mockDBM.registerTargetDBFunc = func(ctx context.Context, info models.TargetDBInfo) (int, error) {
			if info.Name != "analytics_mysql" || info.DBType != "mysql" || info.Port != 3306 || info.DBName != "analytics" {
				t.Errorf("unexpected MySQL TargetDBInfo: %+v", info)
			}
			return 2, nil
		}

		req := newJSONRequest(t, http.MethodPost, "/admin/databases", models.TargetDBInfo{
			Name:     "analytics_mysql",
			DBType:   "mysql",
			Host:     "127.0.0.1",
			Port:     3306,
			User:     "root",
			Password: "secret",
			DBName:   "analytics",
		})
		rec := httptest.NewRecorder()
		app.handleRegisterTargetDB(rec, req)

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "DB ditambahkan dengan id 2") {
			t.Errorf("expected 200 OK JSON with success message, got code=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("RegisterTargetDB_SQLite", func(t *testing.T) {
		mockDBM.registerTargetDBFunc = func(ctx context.Context, info models.TargetDBInfo) (int, error) {
			if info.Name != "catalog_sqlite" || info.DBType != "sqlite" || info.DBName != "/data/app.db" {
				t.Errorf("unexpected SQLite TargetDBInfo: %+v", info)
			}
			return 3, nil
		}

		req := newJSONRequest(t, http.MethodPost, "/admin/databases", models.TargetDBInfo{
			Name:   "catalog_sqlite",
			DBType: "sqlite",
			DBName: "/data/app.db",
		})
		rec := httptest.NewRecorder()
		app.handleRegisterTargetDB(rec, req)

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "DB ditambahkan dengan id 3") {
			t.Errorf("expected 200 OK JSON with success message, got code=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GetDatabases", func(t *testing.T) {
		mockAC.validateSessionFunc = func(_ context.Context, sessionID string) (int, error) {
			if sessionID != "viewer-session" {
				t.Errorf("unexpected databases session: %q", sessionID)
			}
			return 7, nil
		}
		mockDBM.listDatabasesFunc = func(ctx context.Context) ([]models.TargetDBInfo, error) {
			return []models.TargetDBInfo{
				{
					ID: 1, Name: "production_pg", DBType: "postgres", Host: "localhost",
					Port: 5432, User: "db-user", Password: "secret-password", DBName: "prod_db",
				},
				{
					ID: 2, Name: "analytics_pg", DBType: "postgres", Host: "localhost",
					Port: 5432, User: "db-user", Password: "secret-password", DBName: "analytics_db",
				},
			}, nil
		}

		req := httptest.NewRequest(http.MethodGet, "/databases", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "viewer-session"})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") || !strings.Contains(body, `"production_pg"`) || !strings.Contains(body, `"analytics_pg"`) {
			t.Errorf("expected production_pg and analytics_pg in JSON, got:\n%s", body)
		}
		if strings.Contains(body, "secret-password") || strings.Contains(body, "db-user") || strings.Contains(body, `"User"`) || strings.Contains(body, `"Password"`) {
			t.Errorf("databases endpoint exposed credentials: %s", body)
		}
	})

	t.Run("GetDatabasesRequiresAuthAndHandlesEmpty", func(t *testing.T) {
		unauthReq := httptest.NewRequest(http.MethodGet, "/databases", nil)
		unauthRec := httptest.NewRecorder()
		app.ServeHTTP(unauthRec, unauthReq)
		if unauthRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 without session, got %d: %s", unauthRec.Code, unauthRec.Body.String())
		}

		mockDBM.listDatabasesFunc = func(context.Context) ([]models.TargetDBInfo, error) {
			return nil, nil
		}
		req := httptest.NewRequest(http.MethodGet, "/databases", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "viewer-session"})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("get empty databases: status=%d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"databases":[]`) {
			t.Errorf("expected empty databases slice: %s", rec.Body.String())
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
		if !strings.Contains(body, `users`) || !strings.Contains(body, `orders`) {
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
		var payload struct {
			Columns []string         `json:"columns"`
			Rows    []map[string]any `json:"rows"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("invalid json response: %v", err)
		}
		if len(payload.Columns) != 2 || payload.Columns[0] != "email" || payload.Columns[1] != "id" || len(payload.Rows) != 1 {
			t.Errorf("wrong table data JSON: %+v", payload)
		}
	})
}

func TestAdminJSONValidationAndErrors(t *testing.T) {
	mockAC := &mockAccessControlService{}
	mockDBM := &mockDBManagementService{}
	app := NewApp(mockAC, mockDBM, nil)

	t.Run("invalid JSON body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/groups/members", strings.NewReader(`{"user_id":"oops","group_id":3}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.handleAssignUserToGroup(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid JSON body, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("expected JSON error, got %s", rec.Body.String())
		}
	})

	t.Run("failed database request does not echo password", func(t *testing.T) {
		mockDBM.registerTargetDBFunc = func(context.Context, models.TargetDBInfo) (int, error) {
			return 0, errors.New("connection refused")
		}
		req := newJSONRequest(t, http.MethodPost, "/admin/databases", models.TargetDBInfo{
			Name:     "production",
			DBType:   "postgres",
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "very-secret-password",
			DBName:   "prod_db",
		})
		rec := httptest.NewRecorder()
		app.handleRegisterTargetDB(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusInternalServerError || !strings.Contains(body, `"error"`) || strings.Contains(body, "very-secret-password") {
			t.Fatalf("database error feedback: status=%d body=%s", rec.Code, body)
		}
	})
}

func TestMigrationErrorJSONResponse(t *testing.T) {
	ac := &mockAccessControlService{}
	mig := &mockMigrationService{}
	app := NewApp(ac, nil, mig)
	mig.createDraftFunc = func(context.Context, int, string) (int, error) { return 0, errors.New("database unavailable") }

	req := newJSONRequest(t, http.MethodPost, "/databases/1/migrations", map[string]string{
		"sql_query": "CREATE TABLE example (id INT);",
	})
	req.SetPathValue("dbID", "1")
	rec := httptest.NewRecorder()
	app.handleCreateMigrationDraft(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "database unavailable") {
		t.Fatalf("migration error feedback: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminEntityListPages(t *testing.T) {
	mockAC := &mockAccessControlService{
		validateSessionFunc: func(context.Context, string) (int, error) { return 7, nil },
		isAdminFunc:         func(context.Context, int) (bool, error) { return true, nil },
		listUsersFunc: func(context.Context) ([]models.UserSummary, error) {
			return []models.UserSummary{{ID: 4, Username: "alice", IsAdmin: false}}, nil
		},
		listGroupsFunc: func(context.Context) ([]models.Group, error) {
			return []models.Group{{ID: 2, Name: "analysts"}}, nil
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
		{path: "/admin/users", wantText: []string{`"users"`, "alice", `"ID":4`}, mustNotHave: []string{"do-not-display-this", "password_hash"}},
		{path: "/admin/groups", wantText: []string{`"groups"`, "analysts", `"id":2`}, mustNotHave: []string{"do-not-display-this"}},
		{path: "/admin/databases", wantText: []string{`"databases"`, "analytics", `"id":9`, "db.internal", "analytics_db"}, mustNotHave: []string{"do-not-display-this", "db-admin"}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "admin-session"})
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("expected 200 JSON, got %d (%q): %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
			}
			for _, text := range tt.wantText {
				if !strings.Contains(rec.Body.String(), text) {
					t.Errorf("expected %q in JSON, got: %s", text, rec.Body.String())
				}
			}
			for _, text := range tt.mustNotHave {
				if strings.Contains(rec.Body.String(), text) {
					t.Errorf("JSON must not include %q", text)
				}
			}
		})
	}

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
				{ID: 2, TargetDBID: 1, Version: 2, SQLQuery: "ALTER TABLE users ADD email TEXT;", Status: "FAILED"},
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
		if !strings.Contains(body, `CREATE TABLE users (id INT);`) || !strings.Contains(body, `APPLIED`) || !strings.Contains(body, `"can_edit":false`) || !strings.Contains(body, `"drafts":[]`) {
			t.Errorf("wrong VIEW_ONLY migration JSON, got:\n %s", body)
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
		if !strings.Contains(body, `ALTER TABLE users ADD email TEXT;`) || !strings.Contains(body, `DRAFT`) || !strings.Contains(body, `alice`) || !strings.Contains(body, `"can_edit":false`) {
			t.Errorf("wrong migration detail JSON, got:\n %s", body)
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
		if historyRec.Code != http.StatusOK || !strings.Contains(historyRec.Body.String(), `"can_edit":true`) || !strings.Contains(historyRec.Body.String(), `ALTER TABLE users ADD email TEXT;`) {
			t.Errorf("editor should receive drafts and can_edit=true: status=%d body=%s", historyRec.Code, historyRec.Body.String())
		}

		detailReq := httptest.NewRequest(http.MethodGet, "/databases/1/migrations/2", nil)
		detailReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "viewer-session"})
		detailReq.SetPathValue("dbID", "1")
		detailReq.SetPathValue("migrationID", "2")
		detailRec := httptest.NewRecorder()
		app.handleGetMigrationDetail(detailRec, detailReq)
		if detailRec.Code != http.StatusOK || !strings.Contains(detailRec.Body.String(), `"can_edit":true`) {
			t.Errorf("editor detail should have can_edit=true: status=%d body=%s", detailRec.Code, detailRec.Body.String())
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

		if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"id":3`) {
			t.Errorf("expected 201 Created JSON with id=3, got code=%d body=%s", rec.Code, rec.Body.String())
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

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"draft updated"`) {
			t.Errorf("expected 200 OK JSON draft updated, got code=%d body=%s", rec.Code, rec.Body.String())
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

		req := newJSONRequest(t, http.MethodPost, "/databases/1/migrations/3/execute", map[string]string{
			"token": "forged-user",
		})
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "valid-session"})
		req.SetPathValue("dbID", "1")
		req.SetPathValue("migrationID", "3")
		rec := httptest.NewRecorder()
		app.handleExecuteMigration(rec, req)

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"APPLIED"`) {
			t.Errorf("expected 200 OK JSON APPLIED, got code=%d body=%s", rec.Code, rec.Body.String())
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
