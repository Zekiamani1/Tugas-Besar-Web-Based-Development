package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"time"
	"wbd/internal/middleware"
	"wbd/internal/models"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/interfaces"
	"wbd/internal/service/migration"

	"go.opentelemetry.io/otel"
	"gorm.io/gorm"
)

type App struct {
	interfaces.AccessControlService
	interfaces.DBManagementService
	interfaces.MigrationService
	*http.ServeMux
	sessionCookieSecure bool
	migrationApproval   interfaces.MigrationApproval
}

type CookieConfig struct {
	MigrationApproval interfaces.MigrationApproval
	Secure            bool
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
		migrationApproval:    config.MigrationApproval,
	}
	app.HandleFunc("GET /health", app.handleHealth)
	app.Handle("GET /me", middleware.RequireAuth(app, app.handleMe, app.sessionCookieSecure))
	app.Handle("GET /databases", middleware.RequireAuth(app, app.handleListAccessibleDatabases, app.sessionCookieSecure))
	app.Handle("GET /admin/users", middleware.RequireAdminAccess(app, app.handleListUsers, app.sessionCookieSecure))
	app.Handle("GET /admin/groups", middleware.RequireAdminAccess(app, app.handleListGroups, app.sessionCookieSecure))
	app.Handle("GET /admin/databases", middleware.RequireAdminAccess(app, app.handleListDatabases, app.sessionCookieSecure))
	app.HandleFunc("POST /login", app.handleLogin)
	app.Handle("POST /logout", middleware.RequireAuth(app, app.handleLogout, app.sessionCookieSecure))
	app.Handle("POST /admin/users", middleware.RequireAdminAccess(app, app.handleRegisterUser, app.sessionCookieSecure))
	app.Handle("POST /admin/groups", middleware.RequireAdminAccess(app, app.handleCreateGroup, app.sessionCookieSecure))
	app.Handle("POST /admin/groups/members", middleware.RequireAdminAccess(app, app.handleAssignUserToGroup, app.sessionCookieSecure))
	app.Handle("POST /admin/groups/leader", middleware.RequireAdminAccess(app, app.handleSetGroupLeader, app.sessionCookieSecure))
	app.Handle("POST /admin/groups/permissions", middleware.RequireAdminAccess(app, app.handleSetGroupDBAccess, app.sessionCookieSecure))
	app.Handle("DELETE /admin/groups/{groupID}/members/{userID}", middleware.RequireAdminAccess(app, app.handleRemoveGroupMember, app.sessionCookieSecure))
	app.Handle("DELETE /admin/groups/{groupID}/databases/{dbID}/access", middleware.RequireAdminAccess(app, app.handleRevokeGroupDBAccess, app.sessionCookieSecure))
	app.Handle("POST /admin/databases", middleware.RequireAdminAccess(app, app.handleRegisterTargetDB, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetSchemas, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTables, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables/{table}", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTableDetails, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/schemas/{schema}/tables/{table}/data", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetTableData, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationHistory, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations/drafts", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleGetMigrationDraftsJSON, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations/history", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationHistoryJSON, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migration-reviews", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleListPendingMigrationReviews, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations/position", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationPosition, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations/jobs", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleListActiveMigrationJobs, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations/{migrationID}", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationDetail, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations/{migrationID}/definition", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationDefinition, app.sessionCookieSecure))
	app.Handle("POST /databases/{dbID}/migrations", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleCreateMigrationDraft, app.sessionCookieSecure))
	app.Handle("PUT /databases/{dbID}/migrations/{migrationID}", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleUpdateMigrationDraft, app.sessionCookieSecure))
	app.Handle("POST /databases/{dbID}/migrations/{migrationID}/explain", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleExplainMigration, app.sessionCookieSecure))
	app.Handle("POST /databases/{dbID}/migrations/{migrationID}/submit", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleSubmitMigration, app.sessionCookieSecure))
	app.Handle("POST /databases/{dbID}/migrations/{migrationID}/jobs/{jobID}/resume", middleware.RequireDBAccess(app, "VIEW_EDIT", app.handleResumeMigrationJob, app.sessionCookieSecure))
	app.Handle("GET /databases/{dbID}/migrations/{migrationID}/reviews/{reviewID}", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleGetMigrationReview, app.sessionCookieSecure))
	app.Handle("POST /databases/{dbID}/migrations/{migrationID}/reviews/{reviewID}/decision", middleware.RequireDBAccess(app, "VIEW_ONLY", app.handleMigrationReviewDecision, app.sessionCookieSecure))
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

func (app *App) handleMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	users, err := app.ListUsers(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "user unavailable")
		return
	}
	var currentUser *models.UserSummary
	for i := range users {
		if users[i].ID == userID {
			currentUser = &users[i]
			break
		}
	}
	if currentUser == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user": currentUser,
	})
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
		TargetDBID   int    `json:"targetDbId"`
		DatabaseName string `json:"databaseName"`
		AccessLevel  string `json:"accessLevel"`
	}
	type groupDetail struct {
		ID             int                   `json:"id"`
		Name           string                `json:"name"`
		Members        []groupMember         `json:"members"`
		DatabaseAccess []groupDatabaseAccess `json:"databaseAccess"`
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

func (app *App) writeDatabaseSummaries(w http.ResponseWriter, databases []models.TargetDBInfo) {
	type databaseSummary struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		DBType string `json:"dbType"`
		Host   string `json:"host"`
		Port   int    `json:"port"`
		DBName string `json:"dbName"`
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

func (app *App) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	databases, err := app.ListDatabases(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "database list unavailable")
		return
	}
	app.writeDatabaseSummaries(w, databases)
}

func (app *App) handleListAccessibleDatabases(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	databases, err := app.ListDatabases(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "database list unavailable")
		return
	}
	accessible := make([]models.TargetDBInfo, 0, len(databases))
	for _, database := range databases {
		err := app.EvaluateDBAccess(r.Context(), userID, database.ID, accesscontrol.AccessLevelViewOnly)
		if errors.Is(err, accesscontrol.ErrDBAccessDenied) {
			continue
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "database access unavailable")
			return
		}
		accessible = append(accessible, database)
	}
	app.writeDatabaseSummaries(w, accessible)
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

func (app *App) handleSetGroupLeader(w http.ResponseWriter, r *http.Request) {
	var input struct {
		GroupID int `json:"group_id"`
		UserID  int `json:"user_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || input.GroupID <= 0 || input.UserID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid group or user ID")
		return
	}
	service, ok := app.AccessControlService.(interface {
		SetGroupLeader(context.Context, int, int) error
	})
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, "group leader service unavailable")
		return
	}
	if err := service.SetGroupLeader(r.Context(), input.GroupID, input.UserID); err != nil {
		writeJSONError(w, http.StatusBadRequest, "unable to assign group leader")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func accessRemovalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, accesscontrol.ErrAccessRelationNotFound):
		writeJSONError(w, http.StatusNotFound, "Group membership or database access not found.")
	case errors.Is(err, accesscontrol.ErrGroupLeaderRemoval):
		writeJSONError(w, http.StatusConflict, "Replace the group leader before removing this member.")
	case errors.Is(err, accesscontrol.ErrAccessChangeBusy):
		writeJSONError(w, http.StatusConflict, "Access cannot be removed while a migration review or job is active.")
	default:
		writeJSONError(w, http.StatusInternalServerError, "Failed to update access.")
	}
}

func (app *App) handleRemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	groupID, err := strconv.Atoi(r.PathValue("groupID"))
	if err != nil || groupID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Invalid group ID.")
		return
	}
	userID, err := strconv.Atoi(r.PathValue("userID"))
	if err != nil || userID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Invalid user ID.")
		return
	}
	if err := app.RemoveUserFromGroup(r.Context(), userID, groupID); err != nil {
		accessRemovalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (app *App) handleRevokeGroupDBAccess(w http.ResponseWriter, r *http.Request) {
	groupID, err := strconv.Atoi(r.PathValue("groupID"))
	if err != nil || groupID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Invalid group ID.")
		return
	}
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil || dbID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Invalid database ID.")
		return
	}
	if err := app.RevokeGroupDBAccess(r.Context(), groupID, dbID); err != nil {
		accessRemovalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if details != nil {
		if details.Columns == nil {
			details.Columns = make([]models.ColumnInfo, 0)
		}
		if details.Constraints == nil {
			details.Constraints = make([]models.ConstraintInfo, 0)
		}
		if details.Indexes == nil {
			details.Indexes = make([]models.IndexInfo, 0)
		}
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
	if r.URL.Query().Get("hide_rollback") == "true" {
		history = migration.HideRollbackPairs(history)
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

func (app *App) handleGetMigrationDraftsJSON(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil || dbID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	drafts, err := app.GetDrafts(r.Context(), dbID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "unable to read migration drafts")
		return
	}
	type draftSummary struct {
		MigrationID int    `json:"migration_id"`
		Revision    int    `json:"revision"`
		SQLUp       string `json:"sql_up"`
	}
	result := make([]draftSummary, 0, len(drafts))
	for _, draft := range drafts {
		result = append(result, draftSummary{MigrationID: draft.ID, Revision: draft.Revision, SQLUp: draft.SQLQuery})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (app *App) handleGetMigrationHistoryJSON(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil || dbID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	history, err := app.GetLinearHistory(r.Context(), dbID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "unable to read migration history")
		return
	}
	if r.URL.Query().Get("hide_rollback") == "true" {
		history = migration.HideRollbackPairs(history)
	}
	type historyEntry struct {
		ID                 int        `json:"migration_id"`
		ExecutionID        int        `json:"execution_id"`
		Action             string     `json:"action"`
		Revision           int        `json:"revision"`
		RevertsExecutionID *int       `json:"reverts_execution_id"`
		TargetDBID         int        `json:"target_db_id"`
		Version            int        `json:"version"`
		SQLQuery           string     `json:"sql_snapshot"`
		ExecutedBy         string     `json:"executed_by"`
		Status             string     `json:"status"`
		ExecutedAt         time.Time  `json:"started_at"`
		FinishedAt         *time.Time `json:"finished_at"`
	}
	entries := make([]historyEntry, 0, len(history))
	for _, item := range history {
		entries = append(entries, historyEntry{
			ID:                 item.ID,
			ExecutionID:        item.ExecutionID,
			Action:             item.Action,
			Revision:           item.Revision,
			RevertsExecutionID: item.RevertsExecutionID,
			TargetDBID:         item.TargetDBID,
			Version:            item.Version,
			SQLQuery:           item.SQLQuery,
			ExecutedBy:         item.ExecutedBy,
			Status:             item.Status,
			ExecutedAt:         item.ExecutedAt,
			FinishedAt:         item.FinishedAt,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entries)
}

func (app *App) handleGetMigrationPosition(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil || dbID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	reader, ok := app.MigrationService.(interface {
		GetPosition(context.Context, int) (migration.Position, error)
	})
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "migration position unavailable")
		return
	}
	position, err := reader.GetPosition(r.Context(), dbID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "unable to read migration position")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(position)
}

func (app *App) handleListActiveMigrationJobs(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil || dbID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	lister, ok := app.MigrationService.(interface {
		ListActiveJobs(context.Context, int) ([]models.MigrationJob, error)
	})
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, "job service unavailable")
		return
	}
	jobs, err := lister.ListActiveJobs(r.Context(), dbID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "unable to list active migration jobs")
		return
	}
	if jobs == nil {
		jobs = make([]models.MigrationJob, 0)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jobs)
}

func (app *App) handleGetMigrationDefinition(w http.ResponseWriter, r *http.Request) {
	dbID, dbErr := strconv.Atoi(r.PathValue("dbID"))
	migrationID, migrationErr := strconv.Atoi(r.PathValue("migrationID"))
	if dbErr != nil || migrationErr != nil || dbID <= 0 || migrationID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid database or migration ID")
		return
	}
	reader, ok := app.MigrationService.(interface {
		GetDefinition(context.Context, int, int) (models.Migration, error)
	})
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "migration definition unavailable")
		return
	}
	definition, err := reader.GetDefinition(r.Context(), dbID, migrationID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeJSONError(w, http.StatusNotFound, "migration not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "unable to read migration definition")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(definition)
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
	if value := r.URL.Query().Get("job_id"); value != "" {
		jobID, err := strconv.Atoi(value)
		if err != nil || jobID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid job ID")
			return
		}
		jobs, ok := app.MigrationService.(interface {
			FindJob(context.Context, int, int, int) (*models.MigrationJob, error)
		})
		if !ok {
			writeJSONError(w, http.StatusServiceUnavailable, "job service unavailable")
			return
		}
		job, err := jobs.FindJob(r.Context(), dbID, migrationID, jobID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeJSONError(w, http.StatusNotFound, "job not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "unable to read job")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(job)
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

type draftDefinitionService interface {
	CreateDraftDefinition(context.Context, int, models.MigrationDraft) (int, error)
	UpdateDraftDefinition(context.Context, int, int, models.MigrationDraft) error
}

type draftDefinitionResult int

const (
	draftDefinitionLegacy draftDefinitionResult = iota
	draftDefinitionRejected
	draftDefinitionSaved
)

func (app *App) handleDraftDefinitionJSON(w http.ResponseWriter, r *http.Request, legacySQL **string, save func(draftDefinitionService, models.MigrationDraft) error) draftDefinitionResult {
	if rawContentType := r.Header.Get("Content-Type"); rawContentType != "" {
		contentType, _, _ := mime.ParseMediaType(rawContentType)
		if contentType != "application/json" {
			return draftDefinitionLegacy
		}
	}
	var req struct {
		SQLQuery           *string `json:"sql_query"`
		SQLUp              *string `json:"sql_up"`
		SQLDown            *string `json:"sql_down"`
		Irreversible       *bool   `json:"irreversible"`
		IrreversibleReason *string `json:"irreversible_reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return draftDefinitionRejected
	}
	if req.SQLQuery != nil && req.SQLUp == nil && req.SQLDown == nil && req.Irreversible == nil && req.IrreversibleReason == nil {
		*legacySQL = req.SQLQuery
		return draftDefinitionLegacy
	}
	if req.SQLQuery != nil || req.SQLUp == nil {
		writeJSONError(w, http.StatusBadRequest, "send sql_up with sql_down or an irreversible reason")
		return draftDefinitionRejected
	}
	draft := models.MigrationDraft{SQLUp: *req.SQLUp}
	if req.SQLDown != nil {
		draft.SQLDown = *req.SQLDown
	}
	if req.Irreversible != nil {
		draft.Irreversible = *req.Irreversible
	}
	if req.IrreversibleReason != nil {
		draft.IrreversibleReason = *req.IrreversibleReason
	}
	svc, ok := app.MigrationService.(draftDefinitionService)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "migration definition service unavailable")
		return draftDefinitionRejected
	}
	switch err := save(svc, draft); {
	case err == nil:
		return draftDefinitionSaved
	case errors.Is(err, migration.ErrInvalidMigration):
		writeJSONError(w, http.StatusBadRequest, "provide SQL up and down, or mark irreversible and provide a reason")
	case errors.Is(err, migration.ErrDraftNotFound):
		writeJSONError(w, http.StatusConflict, "editable draft not found")
	default:
		writeJSONError(w, http.StatusInternalServerError, "unable to save migration draft")
	}
	return draftDefinitionRejected
}

func (app *App) handleCreateMigrationDraft(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid dbID")
		return
	}
	var migrationID int
	var legacySQL *string
	if handled := app.handleDraftDefinitionJSON(w, r, &legacySQL, func(svc draftDefinitionService, draft models.MigrationDraft) error {
		id, err := svc.CreateDraftDefinition(r.Context(), dbID, draft)
		migrationID = id
		return err
	}); handled != draftDefinitionLegacy {
		if handled == draftDefinitionSaved {
			writeJSON(w, http.StatusCreated, map[string]any{
				"id":    migrationID,
				"db_id": dbID,
			})
		}
		return
	}
	if legacySQL == nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	migrationID, err = app.CreateDraft(r.Context(), dbID, *legacySQL)
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
	var legacySQL *string
	if handled := app.handleDraftDefinitionJSON(w, r, &legacySQL, func(svc draftDefinitionService, draft models.MigrationDraft) error {
		return svc.UpdateDraftDefinition(r.Context(), dbID, migrationID, draft)
	}); handled != draftDefinitionLegacy {
		if handled == draftDefinitionSaved {
			writeJSON(w, http.StatusOK, map[string]any{
				"id":      migrationID,
				"db_id":   dbID,
				"message": "draft updated",
			})
		}
		return
	}
	if legacySQL == nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := app.UpdateDraft(r.Context(), dbID, migrationID, *legacySQL); err != nil {
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
	if app.migrationApproval != nil {
		writeJSONError(w, http.StatusConflict, "submit this migration for approval before execution")
		return
	}
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
	jobs, ok := app.MigrationService.(interface {
		SubmitJob(context.Context, models.MigrationJob) (int, error)
	})
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, "job service unavailable")
		return
	}
	var input struct {
		Action              string `json:"action"`
		Mode                string `json:"mode"`
		BatchSize           int    `json:"batch_size"`
		BatchTable          string `json:"batch_table"`
		BatchAttribute      string `json:"batch_attribute"`
		ResumeJobID         int    `json:"resume_job_id"`
		Revision            int    `json:"revision"`
		ExpectedExecutionID int    `json:"expected_execution_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if input.Action == "" {
		input.Action = migration.ActionApply
	}
	if input.Mode == "" {
		input.Mode = "STANDARD"
	}
	ctx, span := otel.Tracer("wbd/async").Start(r.Context(), "api.submit")
	defer span.End()
	actor := strconv.Itoa(userID)
	if input.ResumeJobID > 0 {
		resumer, ok := app.MigrationService.(interface {
			ResumeJob(context.Context, int, int, int, string) error
		})
		if !ok {
			writeJSONError(w, http.StatusServiceUnavailable, "job resume unavailable")
			return
		}
		if err := resumer.ResumeJob(ctx, dbID, migrationID, input.ResumeJobID, actor); err != nil {
			writeJSONError(w, http.StatusConflict, "job cannot be resumed")
			return
		}
		location := fmt.Sprintf("/databases/%d/migrations/%d?job_id=%d", dbID, migrationID, input.ResumeJobID)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"job_id":     input.ResumeJobID,
			"status":     "QUEUED",
			"status_url": location,
		})
		return
	}
	id, err := jobs.SubmitJob(ctx, models.MigrationJob{
		TargetDBID:          dbID,
		MigrationID:         migrationID,
		Actor:               actor,
		Action:              input.Action,
		Mode:                input.Mode,
		BatchSize:           input.BatchSize,
		BatchTable:          input.BatchTable,
		BatchAttribute:      input.BatchAttribute,
		Revision:            input.Revision,
		ExpectedExecutionID: input.ExpectedExecutionID,
	})
	if errors.Is(err, migration.ErrInvalidJob) {
		writeJSONError(w, http.StatusBadRequest, "invalid job")
		return
	}
	if errors.Is(err, migration.ErrJobConflict) {
		writeJSONError(w, http.StatusConflict, "job already pending")
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeJSONError(w, http.StatusNotFound, "migration not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "unable to queue job")
		return
	}
	location := fmt.Sprintf("/databases/%d/migrations/%d?job_id=%d", dbID, migrationID, id)
	w.Header().Set("Location", location)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id":     id,
		"status":     "QUEUED",
		"status_url": location,
	})
}

func (app *App) handleResumeMigrationJob(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.AuthenticatedUserID(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	dbID, dbErr := strconv.Atoi(r.PathValue("dbID"))
	migrationID, migErr := strconv.Atoi(r.PathValue("migrationID"))
	jobID, jobErr := strconv.Atoi(r.PathValue("jobID"))
	if dbErr != nil || migErr != nil || jobErr != nil || dbID <= 0 || migrationID <= 0 || jobID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid database, migration, or job ID")
		return
	}
	service, ok := app.MigrationService.(interface {
		FindJob(context.Context, int, int, int) (*models.MigrationJob, error)
		ResumeJob(context.Context, int, int, int, string) error
	})
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, "job resume unavailable")
		return
	}
	job, err := service.FindJob(r.Context(), dbID, migrationID, jobID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeJSONError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "unable to read job")
		return
	}
	if job.Mode != "BATCH" || (job.Status != migration.StatusPartial && job.Status != migration.StatusPartialReverted) {
		writeJSONError(w, http.StatusConflict, "job cannot be resumed")
		return
	}
	if app.migrationApproval != nil {
		reader, ok := app.migrationApproval.(interfaces.ApprovedMigrationReader)
		if !ok {
			writeJSONError(w, http.StatusServiceUnavailable, "migration approval unavailable")
			return
		}
		if job.ApprovalID == "" {
			writeJSONError(w, http.StatusConflict, "migration approval required before resume")
			return
		}
		approved, err := reader.GetApprovedMigration(r.Context(), job.ApprovalID)
		if err != nil {
			writeJSONError(w, http.StatusConflict, "migration approval is no longer valid")
			return
		}
		if approved.TargetDBID != job.TargetDBID ||
			approved.MigrationID != job.MigrationID ||
			approved.Action != job.Action ||
			approved.Mode != job.Mode ||
			approved.BatchSize != job.BatchSize ||
			approved.BatchTable != job.BatchTable ||
			approved.BatchAttribute != job.BatchAttribute ||
			approved.Revision != job.Revision ||
			approved.ExpectedExecutionID != job.ExpectedExecutionID {
			writeJSONError(w, http.StatusConflict, "migration approval does not match job")
			return
		}
	}
	ctx, span := otel.Tracer("wbd/async").Start(r.Context(), "api.resume")
	defer span.End()
	if err := service.ResumeJob(ctx, dbID, migrationID, jobID, strconv.Itoa(actor)); err != nil {
		if errors.Is(err, migration.ErrJobConflict) {
			writeJSONError(w, http.StatusConflict, "job cannot be resumed")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "unable to resume job")
		return
	}
	location := fmt.Sprintf("/databases/%d/migrations/%d?job_id=%d", dbID, migrationID, jobID)
	w.Header().Set("Location", location)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id":     jobID,
		"status":     "QUEUED",
		"status_url": location,
	})
}
