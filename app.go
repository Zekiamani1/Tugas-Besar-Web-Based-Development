package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"wbd/internal/middleware"
	"wbd/internal/models"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/interfaces"
)

type App struct {
	interfaces.AccessControlService
	interfaces.DBManagementService
	interfaces.MigrationService
	*http.ServeMux
	sessionCookieSecure bool
}

type CookieConfig struct {
	Secure bool
}

func (app *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	middleware.CSRFProtection(app.ServeMux).ServeHTTP(w, r)
}

func csrfTokenFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(middleware.CSRFTokenCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func NewApp(
	acSvc interfaces.AccessControlService,
	dbmSvc interfaces.DBManagementService,
	migSvc interfaces.MigrationService,
) *App {
	return NewAppWithConfig(acSvc, dbmSvc, migSvc, CookieConfig{})
}

func NewAppWithConfig(
	acSvc interfaces.AccessControlService,
	dbmSvc interfaces.DBManagementService,
	migSvc interfaces.MigrationService,
	config CookieConfig,
) *App {
	app := &App{
		AccessControlService: acSvc,
		DBManagementService:  dbmSvc,
		MigrationService:     migSvc,
		ServeMux:             http.NewServeMux(),
		sessionCookieSecure:  config.Secure,
	}
	app.HandleFunc("GET /{$}", app.handleHome)
	app.HandleFunc("GET /static/app.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/static/app.css")
	})
	app.HandleFunc("GET /login", app.handleLoginPage)
	app.Handle("GET /dashboard", middleware.RequireAuth(app, app.handleDashboard, app.sessionCookieSecure))
	app.Handle("GET /admin", middleware.RequireAdminAccess(app, app.handleAdminPage, app.sessionCookieSecure))
	app.Handle("GET /admin/users", middleware.RequireAdminAccess(app, app.handleAdminUsersPage, app.sessionCookieSecure))
	app.Handle("GET /admin/groups", middleware.RequireAdminAccess(app, app.handleAdminGroupsPage, app.sessionCookieSecure))
	app.Handle("GET /admin/databases", middleware.RequireAdminAccess(app, app.handleAdminDatabasesPage, app.sessionCookieSecure))
	app.HandleFunc("POST /login", app.handleLogin)
	app.Handle("POST /logout", middleware.RequireAuth(app, app.handleLogout, app.sessionCookieSecure))
	app.Handle("POST /admin/users", middleware.RequireAdminAccess(app, app.handleRegisterUser, app.sessionCookieSecure))
	app.Handle("POST /admin/groups", middleware.RequireAdminAccess(app, app.handleCreateGroup, app.sessionCookieSecure))
	app.Handle("POST /admin/groups/members", middleware.RequireAdminAccess(app, app.handleAssignUserToGroup, app.sessionCookieSecure))
	app.Handle("POST /admin/groups/permissions", middleware.RequireAdminAccess(app, app.handleSetGroupDBAccess, app.sessionCookieSecure))
	app.Handle("POST /admin/databases", middleware.RequireAdminAccess(app, app.handleRegisterTargetDB, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleDatabaseRedirect, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetSchemas, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTables, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables/{table}", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTableDetails, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables/{table}/data", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTableData, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationHistory, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations/{migrationID}", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationDetail, app.sessionCookieSecure))
	app.Handle("POST /databases/{dbID}/migrations", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleCreateMigrationDraft, app.sessionCookieSecure))
	app.Handle("PUT /databases/{dbID}/migrations/{migrationID}", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleUpdateMigrationDraft, app.sessionCookieSecure))
	app.Handle("POST /databases/{dbID}/migrations/{migrationID}/edit", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleUpdateMigrationDraft, app.sessionCookieSecure))
	app.Handle("POST /databases/{dbID}/migrations/{migrationID}/explain", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleExplainMigration, app.sessionCookieSecure))
	app.Handle("POST /databases/{dbID}/migrations/{migrationID}/execute", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleExecuteMigration, app.sessionCookieSecure))

	return app
}

func (app *App) setSessionCookie(w http.ResponseWriter, sessionID string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   app.sessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (app *App) handleHome(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(middleware.SessionCookieName); err == nil {
		userID, err := app.ValidateSession(r.Context(), cookie.Value)
		if err == nil && userID > 0 {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		if err != nil && !errors.Is(err, accesscontrol.ErrInvalidSession) {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

type loginPageData struct {
	Username  string
	Error     string
	CSRFToken string
}

func renderLoginPage(w http.ResponseWriter, status int, data loginPageData) {
	tmpl, err := template.ParseFiles("web/static/login.html")
	if err != nil {
		http.Error(w, "login page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = tmpl.Execute(w, data)
}

func (app *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	data := loginPageData{CSRFToken: csrfTokenFromRequest(r)}
	if r.URL.Query().Get("reason") == "session" {
		data.Error = "Sesi berakhir atau Anda belum login. Silakan masuk kembali."
	}
	renderLoginPage(w, http.StatusOK, data)
}
func (app *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(middleware.SessionCookieName)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	userID, err := app.ValidateSession(r.Context(), cookie.Value)
	if errors.Is(err, accesscontrol.ErrInvalidSession) || (err == nil && userID <= 0) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	isAdmin, err := app.IsAdmin(r.Context(), userID)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	dbs, err := app.ListDatabases(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	accessibleDBs := make([]models.TargetDBInfo, 0, len(dbs))
	for _, db := range dbs {
		err := app.EvaluateDBAccess(r.Context(), userID, db.ID, accesscontrol.AccessLevelViewOnly)
		if errors.Is(err, accesscontrol.ErrDBAccessDenied) {
			continue
		}
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		accessibleDBs = append(accessibleDBs, db)
	}
	tmpl, err := template.ParseFiles("web/templates/dashboard.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, struct {
		Databases []models.TargetDBInfo
		IsAdmin   bool
		CSRFToken string
	}{
		Databases: accessibleDBs,
		IsAdmin:   isAdmin,
		CSRFToken: csrfTokenFromRequest(r),
	})
}

func (app *App) handleDatabaseRedirect(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/databases/%d/schemas", dbID), http.StatusSeeOther)
}

type adminPageData struct {
	Message   string
	IsError   bool
	ErrorForm string
	Values    url.Values
	Users     []models.UserSummary
	Groups    []models.Group
	Databases []adminDatabaseSummary
	CSRFToken string
}

func (app *App) renderAdminPage(w http.ResponseWriter, r *http.Request, status int, data adminPageData) {
	users, err := app.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "admin form options unavailable", http.StatusInternalServerError)
		return
	}
	groups, err := app.ListGroups(r.Context())
	if err != nil {
		http.Error(w, "admin form options unavailable", http.StatusInternalServerError)
		return
	}
	databases, err := app.ListDatabases(r.Context())
	if err != nil {
		http.Error(w, "admin form options unavailable", http.StatusInternalServerError)
		return
	}
	data.Users = users
	data.Groups = groups
	data.Databases = summarizeAdminDatabases(databases)
	data.CSRFToken = csrfTokenFromRequest(r)

	tmpl, err := template.ParseFiles("web/templates/admin.html")
	if err != nil {
		http.Error(w, "admin page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = tmpl.Execute(w, data)
}

func (app *App) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	app.renderAdminPage(w, r, http.StatusOK, adminPageData{})
}

func (app *App) handleAdminUsersPage(w http.ResponseWriter, r *http.Request) {
	users, err := app.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "user list unavailable", http.StatusInternalServerError)
		return
	}
	renderAdminListPage(w, "admin_users.html", struct {
		Users []models.UserSummary
	}{Users: users})
}

func (app *App) handleAdminGroupsPage(w http.ResponseWriter, r *http.Request) {
	groups, err := app.ListGroups(r.Context())
	if err != nil {
		http.Error(w, "group list unavailable", http.StatusInternalServerError)
		return
	}
	members, err := app.ListGroupMembers(r.Context())
	if err != nil {
		http.Error(w, "group member list unavailable", http.StatusInternalServerError)
		return
	}
	accesses, err := app.ListGroupDBAccess(r.Context())
	if err != nil {
		http.Error(w, "group database access list unavailable", http.StatusInternalServerError)
		return
	}

	type groupMember struct {
		ID       int
		Username string
	}
	type groupDatabaseAccess struct {
		TargetDBID   int
		DatabaseName string
		AccessLevel  string
	}
	type groupDetail struct {
		ID             int
		Name           string
		Members        []groupMember
		DatabaseAccess []groupDatabaseAccess
	}

	details := make([]groupDetail, 0, len(groups))
	groupIndex := make(map[int]int, len(groups))
	for _, group := range groups {
		groupIndex[group.ID] = len(details)
		details = append(details, groupDetail{ID: group.ID, Name: group.Name})
	}
	for _, member := range members {
		if index, exists := groupIndex[member.GroupID]; exists {
			details[index].Members = append(details[index].Members, groupMember{ID: member.UserID, Username: member.Username})
		}
	}
	for _, access := range accesses {
		if index, exists := groupIndex[access.GroupID]; exists {
			details[index].DatabaseAccess = append(details[index].DatabaseAccess, groupDatabaseAccess{
				TargetDBID:   access.TargetDBID,
				DatabaseName: access.DatabaseName,
				AccessLevel:  access.AccessLevel,
			})
		}
	}

	renderAdminListPage(w, "admin_groups.html", struct {
		Groups []groupDetail
	}{Groups: details})
}

type adminDatabaseSummary struct {
	ID     int
	Name   string
	DBType string
	Host   string
	Port   int
	DBName string
}

func (app *App) handleAdminDatabasesPage(w http.ResponseWriter, r *http.Request) {
	databases, err := app.ListDatabases(r.Context())
	if err != nil {
		http.Error(w, "database list unavailable", http.StatusInternalServerError)
		return
	}
	summaries := summarizeAdminDatabases(databases)
	renderAdminListPage(w, "admin_databases.html", struct {
		Databases []adminDatabaseSummary
	}{Databases: summaries})
}

func summarizeAdminDatabases(databases []models.TargetDBInfo) []adminDatabaseSummary {
	summaries := make([]adminDatabaseSummary, 0, len(databases))
	for _, database := range databases {
		summaries = append(summaries, adminDatabaseSummary{
			ID:     database.ID,
			Name:   database.Name,
			DBType: database.DBType,
			Host:   database.Host,
			Port:   database.Port,
			DBName: database.DBName,
		})
	}
	return summaries
}

func renderAdminListPage(w http.ResponseWriter, page string, data any) {
	tmpl, err := template.ParseFiles("web/templates/" + page)
	if err != nil {
		http.Error(w, "admin list page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		return
	}
}

func isFormRequest(r *http.Request) bool {
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return contentType == "application/x-www-form-urlencoded"
}

func (app *App) adminRequestError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if isFormRequest(r) {
		app.renderAdminPage(w, r, status, adminPageData{
			Message:   message,
			IsError:   true,
			ErrorForm: r.URL.Path,
			Values:    r.PostForm,
		})
		return
	}
	http.Error(w, message, status)
}

func decodeJSONOrForm(r *http.Request, dst any, fromForm func(url.Values) error) error {
	if !isFormRequest(r) {
		return json.NewDecoder(r.Body).Decode(dst)
	}
	if err := r.ParseForm(); err != nil {
		return err
	}
	return fromForm(r.PostForm)
}

func (app *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSONOrForm(r, &req, func(form url.Values) error {
		req.Username = form.Get("username")
		req.Password = form.Get("password")
		return nil
	}); err != nil {
		if isFormRequest(r) {
			renderLoginPage(w, http.StatusBadRequest, loginPageData{Error: "Periksa kembali isian login.", CSRFToken: csrfTokenFromRequest(r)})
		} else {
			http.Error(w, "invalid request body", http.StatusBadRequest)
		}
		return
	}
	sessionID, err := app.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		if isFormRequest(r) {
			renderLoginPage(w, http.StatusUnauthorized, loginPageData{Username: req.Username, Error: "Username atau password salah.", CSRFToken: csrfTokenFromRequest(r)})
		} else {
			http.Error(w, err.Error(), http.StatusUnauthorized)
		}
		return
	}
	app.setSessionCookie(w, sessionID, 0)
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
	app.setSessionCookie(w, "", -1)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (app *App) handleRegisterUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSONOrForm(r, &req, func(form url.Values) error {
		req.Username = form.Get("username")
		req.Password = form.Get("password")
		return nil
	}); err != nil {
		app.adminRequestError(w, r, http.StatusBadRequest, "Periksa kembali data pengguna.")
		return
	}
	if err := app.Register(r.Context(), req.Username, req.Password); err != nil {
		app.adminRequestError(w, r, http.StatusInternalServerError, "Pengguna tidak dapat ditambahkan. Periksa data dan coba lagi.")
		return
	}
	app.renderAdminPage(w, r, http.StatusOK, adminPageData{Message: fmt.Sprintf("berhasil menambahkan %s", req.Username)})
}

func (app *App) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupName string `json:"group_name"`
	}
	if err := decodeJSONOrForm(r, &req, func(form url.Values) error {
		req.GroupName = form.Get("group_name")
		return nil
	}); err != nil {
		app.adminRequestError(w, r, http.StatusBadRequest, "Periksa kembali nama grup.")
		return
	}
	if err := app.CreateGroup(r.Context(), req.GroupName); err != nil {
		app.adminRequestError(w, r, http.StatusInternalServerError, "Grup tidak dapat dibuat. Periksa nama dan coba lagi.")
		return
	}
	app.renderAdminPage(w, r, http.StatusOK, adminPageData{Message: fmt.Sprintf("berhasil menambahkan group %s", req.GroupName)})
}

func (app *App) handleAssignUserToGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID  int `json:"user_id"`
		GroupID int `json:"group_id"`
	}
	if err := decodeJSONOrForm(r, &req, func(form url.Values) (err error) {
		if req.UserID, err = strconv.Atoi(form.Get("user_id")); err != nil {
			return err
		}
		req.GroupID, err = strconv.Atoi(form.Get("group_id"))
		return err
	}); err != nil {
		app.adminRequestError(w, r, http.StatusBadRequest, "Pilih user dan group yang valid.")
		return
	}
	if err := app.AssignUserToGroup(r.Context(), req.UserID, req.GroupID); err != nil {
		app.adminRequestError(w, r, http.StatusInternalServerError, "Pengguna tidak dapat dimasukkan ke grup. Periksa pilihan dan coba lagi.")
		return
	}
	app.renderAdminPage(w, r, http.StatusOK, adminPageData{Message: fmt.Sprintf("berhasil memasukkan user_id %d ke group_id %d", req.UserID, req.GroupID)})
}

func (app *App) handleSetGroupDBAccess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID     int    `json:"group_id"`
		TargetDBID  int    `json:"target_db_id"`
		AccessLevel string `json:"access_level"`
	}
	if err := decodeJSONOrForm(r, &req, func(form url.Values) (err error) {
		if req.GroupID, err = strconv.Atoi(form.Get("group_id")); err != nil {
			return err
		}
		if req.TargetDBID, err = strconv.Atoi(form.Get("target_db_id")); err != nil {
			return err
		}
		req.AccessLevel = form.Get("access_level")
		return nil
	}); err != nil {
		app.adminRequestError(w, r, http.StatusBadRequest, "Pilih group dan database yang valid.")
		return
	}
	if err := app.SetGroupDBAccess(r.Context(), req.GroupID, req.TargetDBID, req.AccessLevel); err != nil {
		app.adminRequestError(w, r, http.StatusInternalServerError, "Izin database tidak dapat disimpan. Periksa pilihan dan coba lagi.")
		return
	}
	app.renderAdminPage(w, r, http.StatusOK, adminPageData{Message: fmt.Sprintf("berhasil meng-set hak akses group_id %d pada target_db_id %d menjadi %s", req.GroupID, req.TargetDBID, req.AccessLevel)})
}

func (app *App) handleRegisterTargetDB(w http.ResponseWriter, r *http.Request) {
	var info models.TargetDBInfo
	if err := decodeJSONOrForm(r, &info, func(form url.Values) (err error) {
		info.Name = form.Get("name")
		info.DBType = form.Get("db_type")
		info.Host = form.Get("host")
		if portStr := form.Get("port"); portStr != "" {
			if info.Port, err = strconv.Atoi(portStr); err != nil {
				return err
			}
		}
		info.User = form.Get("user")
		info.Password = form.Get("password")
		info.DBName = form.Get("db_name")
		return nil
	}); err != nil {
		app.adminRequestError(w, r, http.StatusBadRequest, "Port harus berupa angka dan semua data koneksi perlu diisi.")
		return
	}
	dbID, err := app.RegisterTargetDB(r.Context(), info)
	if err != nil {
		app.adminRequestError(w, r, http.StatusInternalServerError, "Database tidak dapat didaftarkan. Periksa koneksi dan coba lagi.")
		return
	}
	app.renderAdminPage(w, r, http.StatusOK, adminPageData{Message: fmt.Sprintf("DB ditambahkan dengan id %d", dbID)})
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
	var columns []string
	if len(data) > 0 {
		for column := range data[0] {
			columns = append(columns, column)
		}
		sort.Strings(columns)
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
		Columns   []string
		Rows      []map[string]any
	}{
		DBID:      dbID,
		Schema:    schema,
		TableName: table,
		Columns:   columns,
		Rows:      data,
	})
}

func (app *App) migrationCanEdit(r *http.Request, dbID int) (bool, error) {
	cookie, err := r.Cookie(middleware.SessionCookieName)
	if err != nil {
		return false, accesscontrol.ErrInvalidSession
	}
	userID, err := app.ValidateSession(r.Context(), cookie.Value)
	if err != nil {
		return false, err
	}
	if userID <= 0 {
		return false, accesscontrol.ErrInvalidSession
	}
	err = app.EvaluateDBAccess(r.Context(), userID, dbID, accesscontrol.AccessLevelViewEdit)
	if errors.Is(err, accesscontrol.ErrDBAccessDenied) {
		return false, nil
	}
	return err == nil, err
}

func (app *App) handleGetMigrationHistory(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	app.renderMigrationHistory(w, r, dbID, http.StatusOK, "", "")
}

func (app *App) renderMigrationHistory(w http.ResponseWriter, r *http.Request, dbID, status int, pageError, draftSQL string) {
	canEdit, err := app.migrationCanEdit(r, dbID)
	if err != nil {
		if errors.Is(err, accesscontrol.ErrInvalidSession) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		} else {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}
	history, err := app.GetLinearHistory(r.Context(), dbID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var drafts []models.MigrationRecord
	if canEdit {
		drafts, err = app.GetDrafts(r.Context(), dbID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	tmpl, err := template.ParseFiles("web/templates/migrations.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = tmpl.Execute(w, struct {
		DBID       int
		Migrations []models.MigrationRecord
		Drafts     []models.MigrationRecord
		CanEdit    bool
		Error      string
		DraftSQL   string
		CSRFToken  string
	}{
		DBID:       dbID,
		Migrations: history,
		Drafts:     drafts,
		CanEdit:    canEdit,
		Error:      pageError,
		DraftSQL:   draftSQL,
		CSRFToken:  csrfTokenFromRequest(r),
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
	app.renderMigrationDetail(w, r, dbID, migrationID, http.StatusOK, "", "")
}

func (app *App) renderMigrationDetail(w http.ResponseWriter, r *http.Request, dbID, migrationID, status int, pageError, editedSQL string) {
	app.renderMigrationDetailWithPlan(w, r, dbID, migrationID, status, pageError, editedSQL, "")
}

func (app *App) renderMigrationDetailWithPlan(w http.ResponseWriter, r *http.Request, dbID, migrationID, status int, pageError, editedSQL, explainPlan string) {
	canEdit, err := app.migrationCanEdit(r, dbID)
	if err != nil {
		if errors.Is(err, accesscontrol.ErrInvalidSession) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		} else {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = tmpl.Execute(w, struct {
		*models.MigrationRecord
		CanEdit     bool
		Error       string
		EditedSQL   string
		CSRFToken   string
		ExplainPlan string
	}{
		MigrationRecord: detail,
		CanEdit:         canEdit,
		Error:           pageError,
		EditedSQL:       editedSQL,
		CSRFToken:       csrfTokenFromRequest(r),
		ExplainPlan:     explainPlan,
	})
}

func (app *App) handleExplainMigration(w http.ResponseWriter, r *http.Request) {
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

	plan, err := app.ExplainMigration(r.Context(), dbID, migrationID)
	if err != nil {
		app.renderMigrationDetailWithPlan(
			w, r, dbID, migrationID, http.StatusOK,
			"Query plan tidak dapat dibuat. Pastikan draft berisi query yang didukung PostgreSQL.",
			"", "",
		)
		return
	}
	app.renderMigrationDetailWithPlan(w, r, dbID, migrationID, http.StatusOK, "", "", plan)
}

func (app *App) handleCreateMigrationDraft(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		http.Error(w, "invalid dbID", http.StatusBadRequest)
		return
	}
	sqlQuery, err := migrationSQLFromRequest(r)
	if err != nil {
		if isFormRequest(r) {
			app.renderMigrationHistory(w, r, dbID, http.StatusBadRequest, "SQL tidak dapat dibaca. Coba lagi.", "")
		} else {
			http.Error(w, "invalid request body", http.StatusBadRequest)
		}
		return
	}
	migrationID, err := app.CreateDraft(r.Context(), dbID, sqlQuery)
	if err != nil {
		if isFormRequest(r) {
			app.renderMigrationHistory(w, r, dbID, http.StatusInternalServerError, "Draft tidak dapat dibuat. Periksa SQL dan coba lagi.", sqlQuery)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
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
		if isFormRequest(r) {
			app.renderMigrationDetail(w, r, dbID, migrationID, http.StatusBadRequest, "SQL tidak dapat dibaca. Coba lagi.", "")
		} else {
			http.Error(w, "invalid request body", http.StatusBadRequest)
		}
		return
	}
	if err := app.UpdateDraft(r.Context(), dbID, migrationID, sqlQuery); err != nil {
		if isFormRequest(r) {
			app.renderMigrationDetail(w, r, dbID, migrationID, http.StatusInternalServerError, "Draft tidak dapat disimpan. Periksa SQL dan coba lagi.", sqlQuery)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
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
	cookie, err := r.Cookie(middleware.SessionCookieName)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	userID, err := app.ValidateSession(r.Context(), cookie.Value)
	if errors.Is(err, accesscontrol.ErrInvalidSession) || (err == nil && userID <= 0) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if _, err := app.ExecuteMigration(r.Context(), dbID, migrationID, strconv.Itoa(userID)); err != nil {
		if isFormRequest(r) {
			app.renderMigrationDetail(w, r, dbID, migrationID, http.StatusInternalServerError, "Migration gagal dijalankan. Periksa SQL dan status database.", "")
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/databases/%d/migrations/%d", dbID, migrationID), http.StatusSeeOther)
}
