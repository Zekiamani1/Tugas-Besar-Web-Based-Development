package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"strconv"
	"wbd/internal/middleware"
	"wbd/internal/models"
	"wbd/internal/service/interfaces"
)

type App struct {
	interfaces.AccessControlService
	interfaces.DBManagementService
	interfaces.MigrationService
	*http.ServeMux
}

func NewApp(
	acSvc interfaces.AccessControlService,
	dbmSvc interfaces.DBManagementService,
	migSvc interfaces.MigrationService,
) *App {
	app := &App{
		AccessControlService: acSvc,
		DBManagementService:  dbmSvc,
		MigrationService:     migSvc,
		ServeMux:             http.NewServeMux(),
	}
	app.HandleFunc("GET /login", app.handleLoginPage)
	app.Handle("GET /dashboard", middleware.RequireAuth(app, app.handleDashboard))
	app.Handle("GET /admin", middleware.RequireAdminAccess(app, app.handleAdminPage))
	app.HandleFunc("POST /login", app.handleLogin)
	app.Handle("POST /logout", middleware.RequireAuth(app, app.handleLogout))
	app.Handle("POST /admin/users", middleware.RequireAdminAccess(app, app.handleRegisterUser))
	app.Handle("POST /admin/groups", middleware.RequireAdminAccess(app, app.handleCreateGroup))
	app.Handle("POST /admin/groups/members", middleware.RequireAdminAccess(app, app.handleAssignUserToGroup))
	app.Handle("POST /admin/groups/permissions", middleware.RequireAdminAccess(app, app.handleSetGroupDBAccess))
	app.Handle("POST /admin/databases", middleware.RequireAdminAccess(app, app.handleRegisterTargetDB))
	app.Handle("GET /databases/{dbID}/schemas", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetSchemas))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTables))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables/{table}", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTableDetails))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables/{table}/data", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTableData))
	app.Handle("GET /databases/{dbID}/migrations", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationHistory))
	app.Handle("GET /databases/{dbID}/migrations/{migrationID}", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationDetail))
	app.Handle("POST /databases/{dbID}/migrations", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleCreateMigrationDraft))
	app.Handle("PUT /databases/{dbID}/migrations/{migrationID}", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleUpdateMigrationDraft))
	app.Handle("POST /databases/{dbID}/migrations/{migrationID}/edit", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleUpdateMigrationDraft))
	app.Handle("POST /databases/{dbID}/migrations/{migrationID}/execute", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleExecuteMigration))

	return app
}
func (app *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/static/login.html")
}
func (app *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/static/dashboard.html")
}

func (app *App) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("web/templates/admin.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, "")
}

func (app *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	sessionID, err := app.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
	})
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (app *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	var sessionID string
	if c, err := r.Cookie("session_id"); err == nil && c.Value != "" {
		sessionID = c.Value
	} else {
		var req struct {
			SessionID string `json:"session_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		sessionID = req.SessionID
	}
	if err := app.Logout(r.Context(), sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:   "session_id",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (app *App) handleRegisterUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := app.Register(r.Context(), req.Username, req.Password); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/admin.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, fmt.Sprintf("berhasil menambahkan %s", req.Username))
}

func (app *App) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupName string `json:"group_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := app.CreateGroup(r.Context(), req.GroupName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/admin.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, fmt.Sprintf("berhasil menambahkan group %s", req.GroupName))
}

func (app *App) handleAssignUserToGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID  int `json:"user_id"`
		GroupID int `json:"group_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := app.AssignUserToGroup(r.Context(), req.UserID, req.GroupID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/admin.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, fmt.Sprintf("berhasil memasukkan user_id %d ke group_id %d", req.UserID, req.GroupID))
}

func (app *App) handleSetGroupDBAccess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID     int    `json:"group_id"`
		TargetDBID  int    `json:"target_db_id"`
		AccessLevel string `json:"access_level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := app.SetGroupDBAccess(r.Context(), req.GroupID, req.TargetDBID, req.AccessLevel); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/admin.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, fmt.Sprintf("berhasil meng-set hak akses group_id %d pada target_db_id %d menjadi %s", req.GroupID, req.TargetDBID, req.AccessLevel))
}

func (app *App) handleRegisterTargetDB(w http.ResponseWriter, r *http.Request) {
	var info models.TargetDBInfo
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	dbID, err := app.RegisterTargetDB(r.Context(), info)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/admin.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, fmt.Sprintf("DB ditambahkan dengan id %d", dbID))
}

func (app *App) handleGetSchemas(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	schemas, err := app.GetSchemas(r.Context(), dbID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/schemas.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, struct {
		DBID    int
		Schemas []string
	}{
		DBID:    dbID,
		Schemas: schemas,
	})
}

func (app *App) handleGetTables(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	schema := r.PathValue("schema")
	tables, err := app.GetTables(r.Context(), dbID, schema)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/tables.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, struct {
		DBID   int
		Schema string
		Tables []string
	}{
		DBID:   dbID,
		Schema: schema,
		Tables: tables,
	})
}

func (app *App) handleGetTableDetails(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	schema := r.PathValue("schema")
	table := r.PathValue("table")
	details, err := app.GetTableDetails(r.Context(), dbID, schema, table)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/table_details.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, struct {
		DBID     int
		Metadata *models.TableMetadata
	}{
		DBID:     dbID,
		Metadata: details,
	})
}

func (app *App) handleGetTableData(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	schema := r.PathValue("schema")
	table := r.PathValue("table")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	data, err := app.GetTableData(r.Context(), dbID, schema, table, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/table_data.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, struct {
		DBID      int
		Schema    string
		TableName string
		Rows      []map[string]any
	}{
		DBID:      dbID,
		Schema:    schema,
		TableName: table,
		Rows:      data,
	})
}

func (app *App) handleGetMigrationHistory(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	history, err := app.GetLinearHistory(r.Context(), dbID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/migrations.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, struct {
		DBID       int
		Migrations []models.MigrationRecord
	}{
		DBID:       dbID,
		Migrations: history,
	})
}

func (app *App) handleGetMigrationDetail(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	migrationID, err := strconv.Atoi(r.PathValue("migrationID"))
	if err != nil {
		http.Error(w, "invalid migrationID", http.StatusBadRequest)
		return
	}
	detail, err := app.GetMigrationDetail(r.Context(), dbID, migrationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/migration_detail.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, detail)
}

func (app *App) handleCreateMigrationDraft(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	sqlQuery, err := migrationSQLFromRequest(r)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	migrationID, err := app.CreateDraft(r.Context(), dbID, sqlQuery)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/databases/%d/migrations/%d", dbID, migrationID), http.StatusSeeOther)
}

func (app *App) handleUpdateMigrationDraft(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	migrationID, err := strconv.Atoi(r.PathValue("migrationID"))
	if err != nil {
		http.Error(w, "invalid migrationID", http.StatusBadRequest)
		return
	}
	sqlQuery, err := migrationSQLFromRequest(r)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := app.UpdateDraft(r.Context(), dbID, migrationID, sqlQuery); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/databases/%d/migrations/%d", dbID, migrationID), http.StatusSeeOther)
}

func migrationSQLFromRequest(r *http.Request) (string, error) {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return "", err
	}
	switch contentType {
	case "application/json":
		var req struct {
			SQLQuery string `json:"sql_query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return "", err
		}
		return req.SQLQuery, nil
	case "application/x-www-form-urlencoded":
		if err := r.ParseForm(); err != nil {
			return "", err
		}
		return r.PostForm.Get("sql_query"), nil
	default:
		return "", fmt.Errorf("unsupported content type: %s", contentType)
	}
}

func (app *App) handleExecuteMigration(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	migrationID, err := strconv.Atoi(r.PathValue("migrationID"))
	if err != nil {
		http.Error(w, "invalid migrationID", http.StatusBadRequest)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if _, err := app.ExecuteMigration(r.Context(), dbID, migrationID, req.Token); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/databases/%d/migrations/%d", dbID, migrationID), http.StatusSeeOther)
}
