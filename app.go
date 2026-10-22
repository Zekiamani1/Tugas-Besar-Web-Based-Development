package main

import (
	"html/template"
	"net/http"

	"wbd/internal/middleware"
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
	//handle login using access control's login
}

func (app *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	//handle logout using access control's logout
}

func (app *App) handleRegisterUser(w http.ResponseWriter, r *http.Request) {
	//handle regis using access control's register
}

func (app *App) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	//handle group creating using access control's create group
}

func (app *App) handleAssignUserToGroup(w http.ResponseWriter, r *http.Request) {
	//handle group assigning using access control's assign group
}

func (app *App) handleSetGroupDBAccess(w http.ResponseWriter, r *http.Request) {
	//handle group access right using access control thingy
}

func (app *App) handleRegisterTargetDB(w http.ResponseWriter, r *http.Request) {
	//handle registering new DB using dbmanagement's registertarget
}

func (app *App) handleGetSchemas(w http.ResponseWriter, r *http.Request) {
	//handle getting schemas using dbmanagement's getschemas
}

func (app *App) handleGetTables(w http.ResponseWriter, r *http.Request) {
	//handle getting tables using dbmanagement's gettables
}

func (app *App) handleGetTableDetails(w http.ResponseWriter, r *http.Request) {
	//handle getting table details using dbmanagement's gettabledetails
}

func (app *App) handleGetTableData(w http.ResponseWriter, r *http.Request) {
	//handle getting table data using dbmanagement's gettabledata
}

func (app *App) handleGetMigrationHistory(w http.ResponseWriter, r *http.Request) {
	//handle getting migration history using migration's getlinearhistory
}

func (app *App) handleGetMigrationDetail(w http.ResponseWriter, r *http.Request) {
	//handle getting migration detail using migration's getmigrationdetail
}

func (app *App) handleCreateMigrationDraft(w http.ResponseWriter, r *http.Request) {
	//handle creating migration draft using migration's createdraft
}

func (app *App) handleUpdateMigrationDraft(w http.ResponseWriter, r *http.Request) {
	//handle updating migration draft using migration's updatedraft
}

func (app *App) handleExecuteMigration(w http.ResponseWriter, r *http.Request) {
	//handle executing migration using migration's executemigration
}
