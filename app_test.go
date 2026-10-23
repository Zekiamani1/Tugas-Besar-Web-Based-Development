package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"wbd/internal/models"
	"wbd/internal/service/interfaces"
)

type mockAccessControlService struct {
	interfaces.AccessControlService
	registerFunc          func(ctx context.Context, username, password string) error
	loginFunc             func(ctx context.Context, username, password string) (string, error)
	logoutFunc            func(ctx context.Context, sessionID string) error
	validateSessionFunc   func(ctx context.Context, sessionID string) error
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

func (m *mockAccessControlService) ValidateSession(ctx context.Context, sessionID string) error {
	if m.validateSessionFunc != nil {
		return m.validateSessionFunc(ctx, sessionID)
	}
	return nil
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
	getSchemasFunc       func(ctx context.Context, targetDBID int) ([]string, error)
	getTablesFunc        func(ctx context.Context, targetDBID int, schema string) ([]string, error)
	getTableDetailsFunc  func(ctx context.Context, targetDBID int, schema, tableName string) (*models.TableMetadata, error)
	getTableDataFunc     func(ctx context.Context, targetDBID int, schema, tableName string, limit, offset int) ([]map[string]any, error)
}

func (m *mockDBManagementService) RegisterTargetDB(ctx context.Context, info models.TargetDBInfo) (int, error) {
	return m.registerTargetDBFunc(ctx, info)
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
	app := NewApp(mockAC, nil, nil)

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

func TestDBManagementHandlers(t *testing.T) {
	mockDBM := &mockDBManagementService{}
	app := NewApp(nil, mockDBM, nil)

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
		if !strings.Contains(body, `id`) || !strings.Contains(body, `1`) || !strings.Contains(body, `email`) || !strings.Contains(body, `alice@example.com`) {
			t.Errorf("wrong table data, got:\n %s", body)
		}
	})
}

func TestMigrationHandlers(t *testing.T) {
	mockMig := &mockMigrationService{}
	app := NewApp(nil, nil, mockMig)

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
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
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
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/1/migrations/4" {
			t.Errorf("form draft update: code=%d loc=%s", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("ExecuteMigration", func(t *testing.T) {
		mockMig.executeMigrationFunc = func(ctx context.Context, targetDBID, migrationID int, token string) (*models.MigrationRecord, error) {
			if targetDBID != 1 || migrationID != 3 || token != "exec-token-xyz" {
				t.Errorf("unexpected execute migration args: (%d, %d, %s)", targetDBID, migrationID, token)
			}
			return &models.MigrationRecord{
				ID:         3,
				TargetDBID: 1,
				Version:    3,
				SQLQuery:   "CREATE TABLE orders (id SERIAL PRIMARY KEY);",
				Status:     "APPLIED",
				ExecutedBy: "alice",
			}, nil
		}

		req := newJSONRequest(t, http.MethodPost, "/databases/1/migrations/3/execute", map[string]any{
			"token": "exec-token-xyz",
		})
		req.SetPathValue("dbID", "1")
		req.SetPathValue("migrationID", "3")
		rec := httptest.NewRecorder()
		app.handleExecuteMigration(rec, req)

		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/1/migrations/3" {
			t.Errorf("expected 303 redirect to /databases/1/migrations/3, got code=%d loc=%s", rec.Code, rec.Header().Get("Location"))
		}
	})
}
