package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
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
	app.HandleFunc("GET /health", app.handleHealth)
	app.Handle("GET /databases", middleware.RequireAuth(app, app.handleListDatabases, app.sessionCookieSecure))
	app.Handle("GET /admin/users", middleware.RequireAdminAccess(app, app.handleListUsers, app.sessionCookieSecure))
	app.Handle("GET /admin/groups", middleware.RequireAdminAccess(app, app.handleListGroups, app.sessionCookieSecure))
	app.Handle("GET /admin/databases", middleware.RequireAdminAccess(app, app.handleListDatabases, app.sessionCookieSecure))
	app.HandleFunc("POST /login", app.handleLogin)
	app.Handle("POST /logout", middleware.RequireAuth(app, app.handleLogout, app.sessionCookieSecure))
	app.Handle("POST /admin/users", middleware.RequireAdminAccess(app, app.handleRegisterUser, app.sessionCookieSecure))
	app.Handle("POST /admin/groups", middleware.RequireAdminAccess(app, app.handleCreateGroup, app.sessionCookieSecure))
	app.Handle("POST /admin/groups/members", middleware.RequireAdminAccess(app, app.handleAssignUserToGroup, app.sessionCookieSecure))
	app.Handle("POST /admin/groups/permissions", middleware.RequireAdminAccess(app, app.handleSetGroupDBAccess, app.sessionCookieSecure))
	app.Handle("POST /admin/databases", middleware.RequireAdminAccess(app, app.handleRegisterTargetDB, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetSchemas, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTables, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables/{table}", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTableDetails, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables/{table}/data", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTableData, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationHistory, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations/{migrationID}", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationDetail, app.sessionCookieSecure))
	app.Handle("POST /databases/{dbID}/migrations", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleCreateMigrationDraft, app.sessionCookieSecure))
	app.Handle("PUT /databases/{dbID}/migrations/{migrationID}", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleUpdateMigrationDraft, app.sessionCookieSecure))
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

func (app *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (app *App) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := app.ListUsers(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "user list unavailable")
		return
	}
	if users == nil {
		users = make([]models.UserSummary, 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (app *App) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := app.ListGroups(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "group list unavailable")
		return
	}
	members, err := app.ListGroupMembers(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "group member list unavailable")
		return
	}
	accesses, err := app.ListGroupDBAccess(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "group database access list unavailable")
		return
	}

	type groupMember struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
	}
	type groupDatabaseAccess struct {
		TargetDBID   int    `json:"target_db_id"`
		DatabaseName string `json:"database_name"`
		AccessLevel  string `json:"access_level"`
	}
	type groupDetail struct {
		ID             int                   `json:"id"`
		Name           string                `json:"name"`
		Members        []groupMember         `json:"members"`
		DatabaseAccess []groupDatabaseAccess `json:"database_access"`
	}

	details := make([]groupDetail, 0, len(groups))
	groupIndex := make(map[int]int, len(groups))
	for _, group := range groups {
		groupIndex[group.ID] = len(details)
		details = append(details, groupDetail{
			ID:             group.ID,
			Name:           group.Name,
			Members:        make([]groupMember, 0),
			DatabaseAccess: make([]groupDatabaseAccess, 0),
		})
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

	writeJSON(w, http.StatusOK, map[string]any{"groups": details})
}

func (app *App) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	databases, err := app.ListDatabases(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "database list unavailable")
		return
	}
	type databaseSummary struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		DBType string `json:"db_type"`
		Host   string `json:"host"`
		Port   int    `json:"port"`
		DBName string `json:"db_name"`
	}
	summaries := make([]databaseSummary, 0, len(databases))
	for _, database := range databases {
		summaries = append(summaries, databaseSummary{
			ID:     database.ID,
			Name:   database.Name,
			DBType: database.DBType,
			Host:   database.Host,
			Port:   database.Port,
			DBName: database.DBName,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": summaries})
}

func (app *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sessionID, err := app.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	app.setSessionCookie(w, sessionID, 0)
	writeJSON(w, http.StatusOK, map[string]string{"message": "login successful"})
}

func (app *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	var sessionID string
	if c, err := r.Cookie(middleware.SessionCookieName); err == nil && c.Value != "" {
		sessionID = c.Value
	} else {
		var req struct {
			SessionID string `json:"session_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		sessionID = req.SessionID
	}
	if err := app.Logout(r.Context(), sessionID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	app.setSessionCookie(w, "", -1)
	writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

func (app *App) handleRegisterUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := app.Register(r.Context(), req.Username, req.Password); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("berhasil menambahkan %s", req.Username),
	})
}

func (app *App) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupName string `json:"group_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := app.CreateGroup(r.Context(), req.GroupName); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("berhasil menambahkan group %s", req.GroupName),
	})
}

func (app *App) handleAssignUserToGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID  int `json:"user_id"`
		GroupID int `json:"group_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := app.AssignUserToGroup(r.Context(), req.UserID, req.GroupID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("berhasil memasukkan user_id %d ke group_id %d", req.UserID, req.GroupID),
	})
}

func (app *App) handleSetGroupDBAccess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID     int    `json:"group_id"`
		TargetDBID  int    `json:"target_db_id"`
		AccessLevel string `json:"access_level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := app.SetGroupDBAccess(r.Context(), req.GroupID, req.TargetDBID, req.AccessLevel); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("berhasil meng-set hak akses group_id %d pada target_db_id %d menjadi %s", req.GroupID, req.TargetDBID, req.AccessLevel),
	})
}

func (app *App) handleRegisterTargetDB(w http.ResponseWriter, r *http.Request) {
	var info models.TargetDBInfo
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dbID, err := app.RegisterTargetDB(r.Context(), info)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to register database")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      dbID,
		"message": fmt.Sprintf("DB ditambahkan dengan id %d", dbID),
	})
}

func (app *App) handleGetSchemas(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	schemas, err := app.GetSchemas(r.Context(), dbID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if schemas == nil {
		schemas = make([]string, 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"db_id":   dbID,
		"schemas": schemas,
	})
}

func (app *App) handleGetTables(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	schema := r.PathValue("schema")
	tables, err := app.GetTables(r.Context(), dbID, schema)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tables == nil {
		tables = make([]string, 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"db_id":  dbID,
		"schema": schema,
		"tables": tables,
	})
}

func (app *App) handleGetTableDetails(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	schema := r.PathValue("schema")
	table := r.PathValue("table")
	details, err := app.GetTableDetails(r.Context(), dbID, schema, table)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"db_id":    dbID,
		"metadata": details,
	})
}

func (app *App) handleGetTableData(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
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
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	columns := make([]string, 0)
	if len(data) > 0 {
		for column := range data[0] {
			columns = append(columns, column)
		}
		sort.Strings(columns)
	} else {
		data = make([]map[string]any, 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"db_id":      dbID,
		"schema":     schema,
		"table_name": table,
		"columns":    columns,
		"rows":       data,
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
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	canEdit, err := app.migrationCanEdit(r, dbID)
	if err != nil {
		if errors.Is(err, accesscontrol.ErrInvalidSession) {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		} else {
			writeJSONError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}
	history, err := app.GetLinearHistory(r.Context(), dbID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if history == nil {
		history = make([]models.MigrationRecord, 0)
	}
	drafts := make([]models.MigrationRecord, 0)
	if canEdit {
		loadedDrafts, err := app.GetDrafts(r.Context(), dbID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if loadedDrafts != nil {
			drafts = loadedDrafts
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"db_id":      dbID,
		"migrations": history,
		"drafts":     drafts,
		"can_edit":   canEdit,
	})
}

func (app *App) handleGetMigrationDetail(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	migrationID, err := strconv.Atoi(r.PathValue("migrationID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid migrationID")
		return
	}
	canEdit, err := app.migrationCanEdit(r, dbID)
	if err != nil {
		if errors.Is(err, accesscontrol.ErrInvalidSession) {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		} else {
			writeJSONError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}
	detail, err := app.GetMigrationDetail(r.Context(), dbID, migrationID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"migration": detail,
		"can_edit":  canEdit,
	})
}

func (app *App) handleExplainMigration(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	migrationID, err := strconv.Atoi(r.PathValue("migrationID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid migrationID")
		return
	}

	plan, err := app.ExplainMigration(r.Context(), dbID, migrationID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"db_id":        dbID,
		"migration_id": migrationID,
		"explain_plan": plan,
	})
}

func (app *App) handleCreateMigrationDraft(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	var req struct {
		SQLQuery string `json:"sql_query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	migrationID, err := app.CreateDraft(r.Context(), dbID, req.SQLQuery)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":    migrationID,
		"db_id": dbID,
	})
}

func (app *App) handleUpdateMigrationDraft(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	migrationID, err := strconv.Atoi(r.PathValue("migrationID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid migrationID")
		return
	}
	var req struct {
		SQLQuery string `json:"sql_query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := app.UpdateDraft(r.Context(), dbID, migrationID, req.SQLQuery); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      migrationID,
		"db_id":   dbID,
		"message": "draft updated",
	})
}

func (app *App) handleExecuteMigration(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	migrationID, err := strconv.Atoi(r.PathValue("migrationID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid migrationID")
		return
	}
	cookie, err := r.Cookie(middleware.SessionCookieName)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	userID, err := app.ValidateSession(r.Context(), cookie.Value)
	if errors.Is(err, accesscontrol.ErrInvalidSession) || (err == nil && userID <= 0) {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	record, err := app.ExecuteMigration(r.Context(), dbID, migrationID, strconv.Itoa(userID))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"migration": record,
	})
}
