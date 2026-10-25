package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wbd/internal/models"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	_ "modernc.org/sqlite"
)

func startMySQLServer(t *testing.T, dbName string) (*tcmysql.MySQLContainer, string, int, string, string) {
	t.Helper()
	ctx := context.Background()
	container, err := tcmysql.Run(ctx, "mysql:8.0",
		tcmysql.WithDatabase(dbName),
		tcmysql.WithUsername("testuser"),
		tcmysql.WithPassword("secret"),
	)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("failed to start mysql container for %s: %v", dbName, err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get mysql host: %v", err)
	}
	port, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("failed to get mysql port: %v", err)
	}
	return container, host, int(port.Num()), "testuser", "secret"
}

func startPostgresServer(t *testing.T, dbName string) (*tcpostgres.PostgresContainer, string) {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("secret"),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("failed to start postgres container for %s: %v", dbName, err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get postgres connection string for %s: %v", dbName, err)
	}
	return container, dsn
}

func setupe2e(t *testing.T) (string, *http.Client, *sql.DB, *sql.DB, string, int) {
	t.Helper()
	ctx := context.Background()

	//Build the application binary executable
	tempDir := t.TempDir()
	binPath := filepath.Join(tempDir, "platform-server")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		t.Fatalf("failed to compile application: %v\n%s", err, string(out))
	}

	//Start PostgreSQL containers for internal platform and target database
	_, internalDSN := startPostgresServer(t, "db_platform_internal")
	internalDB, err := sql.Open("postgres", internalDSN)
	if err != nil {
		t.Fatalf("failed to open internal database connection: %v", err)
	}
	t.Cleanup(func() { _ = internalDB.Close() })

	targetContainer, targetDSN := startPostgresServer(t, "db_toko_online")
	targetDB, err := sql.Open("postgres", targetDSN)
	if err != nil {
		t.Fatalf("failed to open target database connection: %v", err)
	}
	t.Cleanup(func() { _ = targetDB.Close() })

	targetHost, _ := targetContainer.Host(ctx)
	targetPort, _ := targetContainer.MappedPort(ctx, "5432/tcp")

	//Run the application on a free local port
	const adminUser = "admin_platform"
	const adminPass = "AdminSecret123!"

	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	serverPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	serverCmd := exec.Command(binPath)
	serverCmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", serverPort),
		fmt.Sprintf("DATABASE_URL=%s", internalDSN),
		"SESSION_ABSOLUTE_TTL=2h",
		"SESSION_IDLE_TIMEOUT=30m",
		"SESSION_ROTATION_INTERVAL=15m",
		"SESSION_ROTATION_GRACE_PERIOD=30s",
		"SESSION_COOKIE_SECURE=false",
		fmt.Sprintf("ADMIN_USERNAME=%s", adminUser),
		fmt.Sprintf("ADMIN_PASSWORD=%s", adminPass),
	)
	if err := serverCmd.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	t.Cleanup(func() {
		if serverCmd.Process != nil {
			_ = serverCmd.Process.Kill()
		}
	})

	serverBaseURL := fmt.Sprintf("http://127.0.0.1:%d", serverPort)

	//Wait until the server is ready
	serverReady := false
	for attempt := 0; attempt < 50; attempt++ {
		time.Sleep(100 * time.Millisecond)
		if resp, err := http.Get(serverBaseURL + "/login"); err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			serverReady = true
			break
		}
	}
	if !serverReady {
		t.Fatal("application server did not become ready within 5 seconds; check its configuration and startup logs")
	}

	//HTTP client
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return serverBaseURL, client, internalDB, targetDB, targetHost, int(targetPort.Num())
}

func TestE2E(t *testing.T) {
	rootT := t
	serverURL, client, internalDB, targetDB, targetHost, targetPort := setupe2e(t)

	var targetDatabaseID int
	var sqliteDatabaseID int
	var mysqlDatabaseID int
	var devUserID, analystUserID, devGroupID, analystGroupID int
	var draftID string
	t.Run("Admin Login", func(t *testing.T) {
		loginPayload, _ := json.Marshal(map[string]string{
			"username": "admin_platform",
			"password": "AdminSecret123!",
		})
		resp, err := client.Post(serverURL+"/login", "application/json", bytes.NewReader(loginPayload))
		if err != nil {
			t.Fatalf("admin login request failed: %v", err)
		}
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("admin login failed: status %v", resp.StatusCode)
		}
		if len(resp.Cookies()) == 0 {
			t.Fatal("session cookie not found in login response")
		}
		resp.Body.Close()
	})

	t.Run("Registers Target Database", func(t *testing.T) {
		dbPayload, _ := json.Marshal(models.TargetDBInfo{
			Name:     "Toko Online Production",
			DBType:   "postgres",
			Host:     targetHost,
			Port:     targetPort,
			User:     "postgres",
			Password: "secret",
			DBName:   "db_toko_online",
		})
		resp, err := client.Post(serverURL+"/admin/databases", "application/json", bytes.NewReader(dbPayload))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("failed to register target database: %v", err)
		}
		resp.Body.Close()

		if err := internalDB.QueryRow("SELECT id FROM target_db_infos WHERE name = 'Toko Online Production'").Scan(&targetDatabaseID); err != nil {
			t.Fatalf("failed to query target db id: %v", err)
		}
	})

	t.Run("Registers SQLite Target Database", func(t *testing.T) {
		sqliteFile := filepath.Join(rootT.TempDir(), "e2e_sqlite.db")
		rawSQLite, err := sql.Open("sqlite", sqliteFile)
		if err != nil {
			t.Fatalf("failed to open raw sqlite: %v", err)
		}
		for _, stmt := range []string{
			`CREATE TABLE e2e_articles (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				title TEXT NOT NULL,
				content TEXT
			)`,
			`INSERT INTO e2e_articles (title, content) VALUES ('Hello World', 'First content')`,
		} {
			if _, err := rawSQLite.Exec(stmt); err != nil {
				t.Fatalf("failed to seed sqlite: %v", err)
			}
		}
		rawSQLite.Close()

		dbPayload, _ := json.Marshal(models.TargetDBInfo{
			Name:   "SQLite E2E Database",
			DBType: "sqlite",
			DBName: sqliteFile,
		})
		resp, err := client.Post(serverURL+"/admin/databases", "application/json", bytes.NewReader(dbPayload))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("failed to register sqlite target database: %v", err)
		}
		resp.Body.Close()

		if err := internalDB.QueryRow("SELECT id FROM target_db_infos WHERE name = 'SQLite E2E Database'").Scan(&sqliteDatabaseID); err != nil {
			t.Fatalf("failed to query sqlite target db id: %v", err)
		}
	})

	t.Run("Registers MySQL Target Database", func(t *testing.T) {
		const mysqlDBName = "e2e_mysql"
		_, mHost, mPort, mUser, mPass := startMySQLServer(rootT, mysqlDBName)

		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true", mUser, mPass, mHost, mPort, mysqlDBName)
		rawDB, err := sql.Open("mysql", dsn)
		if err != nil {
			t.Fatalf("failed to open mysql: %v", err)
		}
		for _, stmt := range []string{
			`CREATE TABLE e2e_orders (
				id INT AUTO_INCREMENT PRIMARY KEY,
				order_no VARCHAR(100) NOT NULL UNIQUE,
				amount DECIMAL(10, 2)
			)`,
			`INSERT INTO e2e_orders (order_no, amount) VALUES ('ORD-001', 123.45)`,
		} {
			if _, err := rawDB.Exec(stmt); err != nil {
				t.Fatalf("failed to seed mysql: %v", err)
			}
		}
		rawDB.Close()

		dbPayload, _ := json.Marshal(models.TargetDBInfo{
			Name:     "MySQL E2E Database",
			DBType:   "mysql",
			Host:     mHost,
			Port:     mPort,
			User:     mUser,
			Password: mPass,
			DBName:   mysqlDBName,
		})
		resp, err := client.Post(serverURL+"/admin/databases", "application/json", bytes.NewReader(dbPayload))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("failed to register mysql target database: %v", err)
		}
		resp.Body.Close()

		if err := internalDB.QueryRow("SELECT id FROM target_db_infos WHERE name = 'MySQL E2E Database'").Scan(&mysqlDatabaseID); err != nil {
			t.Fatalf("failed to query mysql target db id: %v", err)
		}
	})

	t.Run("Registers User Accounts", func(t *testing.T) {
		for _, u := range []struct{ username, password string }{
			{"budi_developer", "BudiPassword123!"},
			{"siti_analyst", "SitiPassword123!"},
		} {
			userPayload, _ := json.Marshal(map[string]string{"username": u.username, "password": u.password})
			resp, err := client.Post(serverURL+"/admin/users", "application/json", bytes.NewReader(userPayload))
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("failed to register user %s: status %v", u.username, resp.StatusCode)
			}
			resp.Body.Close()
		}

		if err := internalDB.QueryRow("SELECT id FROM users WHERE username = 'budi_developer'").Scan(&devUserID); err != nil {
			t.Fatalf("failed to query dev user id: %v", err)
		}
		if err := internalDB.QueryRow("SELECT id FROM users WHERE username = 'siti_analyst'").Scan(&analystUserID); err != nil {
			t.Fatalf("failed to query analyst user id: %v", err)
		}
	})

	t.Run("Creates Groups", func(t *testing.T) {
		for _, g := range []string{"tim_pengembang", "tim_analis"} {
			groupPayload, _ := json.Marshal(map[string]string{"group_name": g})
			resp, err := client.Post(serverURL+"/admin/groups", "application/json", bytes.NewReader(groupPayload))
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("failed to create group %s: status %v", g, resp.StatusCode)
			}
			resp.Body.Close()
		}

		if err := internalDB.QueryRow("SELECT id FROM groups WHERE name = 'tim_pengembang'").Scan(&devGroupID); err != nil {
			t.Fatalf("failed to query dev group id: %v", err)
		}
		if err := internalDB.QueryRow("SELECT id FROM groups WHERE name = 'tim_analis'").Scan(&analystGroupID); err != nil {
			t.Fatalf("failed to query analyst group id: %v", err)
		}
	})

	t.Run("Assigns Members to Groups", func(t *testing.T) {
		for _, m := range []struct{ userID, groupID int }{
			{devUserID, devGroupID},
			{analystUserID, analystGroupID},
		} {
			memberPayload, _ := json.Marshal(map[string]int{"user_id": m.userID, "group_id": m.groupID})
			resp, err := client.Post(serverURL+"/admin/groups/members", "application/json", bytes.NewReader(memberPayload))
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("failed to assign user_id %d to group: status %v", m.userID, resp.StatusCode)
			}
			resp.Body.Close()
		}
	})

	t.Run("Sets Group Database Access Permissions", func(t *testing.T) {
		for _, dbID := range []int{targetDatabaseID, sqliteDatabaseID, mysqlDatabaseID} {
			for _, p := range []struct {
				groupID int
				level   string
			}{
				{devGroupID, "VIEW_EDIT"},
				{analystGroupID, "VIEW_ONLY"},
			} {
				permPayload, _ := json.Marshal(map[string]any{"group_id": p.groupID, "target_db_id": dbID, "access_level": p.level})
				resp, err := client.Post(serverURL+"/admin/groups/permissions", "application/json", bytes.NewReader(permPayload))
				if err != nil || resp.StatusCode != http.StatusOK {
					t.Fatalf("failed to set permissions for group %d on db %d: status %v", p.groupID, dbID, resp.StatusCode)
				}
				resp.Body.Close()
			}
		}
	})

	t.Run("Admin Logout", func(t *testing.T) {
		resp, err := client.Post(serverURL+"/logout", "application/json", nil)
		if err != nil || resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("admin logout failed: status %v", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Dashboard Access Denied Without Authentication", func(t *testing.T) {
		resp, _ := client.Get(serverURL + "/dashboard")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected status 401 without login, but got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Admin Panel Access Denied Without Authentication", func(t *testing.T) {
		resp, _ := client.Get(serverURL + "/admin")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected status 401 without login, but got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Database Access Denied Without Authentication", func(t *testing.T) {
		resp, _ := client.Get(fmt.Sprintf("%s/databases/%d/schemas", serverURL, targetDatabaseID))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected status 401 without login, but got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Analyst Login (View Only)", func(t *testing.T) {
		loginPayload, _ := json.Marshal(map[string]string{"username": "siti_analyst", "password": "SitiPassword123!"})
		resp, err := client.Post(serverURL+"/login", "application/json", bytes.NewReader(loginPayload))
		if err != nil || resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("analyst login failed: status %v", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Analyst Accesses Database and Redirects to Schemas", func(t *testing.T) {
		resp, _ := client.Get(fmt.Sprintf("%s/databases/%d", serverURL, targetDatabaseID))
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != fmt.Sprintf("/databases/%d/schemas", targetDatabaseID) {
			t.Errorf("database page redirection failed, status: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Analyst Views Schema List", func(t *testing.T) {
		resp, _ := client.Get(fmt.Sprintf("%s/databases/%d/schemas", serverURL, targetDatabaseID))
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(bodyBytes), "public") {
			t.Errorf("failed to open schema page: status %d", resp.StatusCode)
		}
	})

	t.Run("Analyst Views SQLite Table Info", func(t *testing.T) {
		resp, err := client.Get(fmt.Sprintf("%s/databases/%d/schemas", serverURL, sqliteDatabaseID))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("failed to open sqlite schemas: status %v", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), "main") {
			t.Errorf("expected 'main' in sqlite schema page: %s", string(body))
		}

		resp, err = client.Get(fmt.Sprintf("%s/databases/%d/schemas/main/tables", serverURL, sqliteDatabaseID))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("failed to open sqlite tables: status %v", resp.StatusCode)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), "e2e_articles") {
			t.Errorf("expected 'e2e_articles' in sqlite tables page: %s", string(body))
		}

		resp, err = client.Get(fmt.Sprintf("%s/databases/%d/schemas/main/tables/e2e_articles", serverURL, sqliteDatabaseID))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("failed to open sqlite table details: status %v", resp.StatusCode)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), "title") || !strings.Contains(string(body), "PRIMARY KEY") {
			t.Errorf("expected table info in sqlite details: %s", string(body))
		}
	})

	t.Run("Analyst Views MySQL Table Info", func(t *testing.T) {
		const mysqlDBName = "e2e_mysql"
		resp, err := client.Get(fmt.Sprintf("%s/databases/%d/schemas", serverURL, mysqlDatabaseID))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("failed to open mysql schemas: status %v", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), mysqlDBName) {
			t.Errorf("expected '%s' in mysql schema page: %s", mysqlDBName, string(body))
		}

		resp, err = client.Get(fmt.Sprintf("%s/databases/%d/schemas/%s/tables", serverURL, mysqlDatabaseID, mysqlDBName))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("failed to open mysql tables: status %v", resp.StatusCode)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), "e2e_orders") {
			t.Errorf("expected 'e2e_orders' in mysql tables page: %s", string(body))
		}

		resp, err = client.Get(fmt.Sprintf("%s/databases/%d/schemas/%s/tables/e2e_orders", serverURL, mysqlDatabaseID, mysqlDBName))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("failed to open mysql table details: status %v", resp.StatusCode)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), "order_no") || !strings.Contains(string(body), "PRIMARY KEY") {
			t.Errorf("expected table info in mysql details: %s", string(body))
		}
	})

	t.Run("Migration Creation Denied for View Only", func(t *testing.T) {
		draftPayload, _ := json.Marshal(map[string]string{"sql_query": "CREATE TABLE forbidden_test (id INT);"})
		resp, _ := client.Post(fmt.Sprintf("%s/databases/%d/migrations", serverURL, targetDatabaseID), "application/json", bytes.NewReader(draftPayload))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected status 403 when VIEW_ONLY creates migration, got: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Migration Edit Denied for View Only", func(t *testing.T) {
		editPayload, _ := json.Marshal(map[string]string{"sql_query": "ALTER TABLE forbidden_test ADD COLUMN x INT;"})
		resp, _ := client.Post(fmt.Sprintf("%s/databases/%d/migrations/1/edit", serverURL, targetDatabaseID), "application/json", bytes.NewReader(editPayload))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected status 403 when VIEW_ONLY edits migration, got: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Migration Execution Denied for View Only", func(t *testing.T) {
		execPayload, _ := json.Marshal(map[string]string{"token": "siti_analyst"})
		resp, _ := client.Post(fmt.Sprintf("%s/databases/%d/migrations/1/execute", serverURL, targetDatabaseID), "application/json", bytes.NewReader(execPayload))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected status 403 when VIEW_ONLY executes migration, got: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Database Access Denied for Unassigned Database", func(t *testing.T) {
		resp, _ := client.Get(fmt.Sprintf("%s/databases/9999/schemas", serverURL))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected status 403 when accessing unassigned database, got: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Admin Panel Access Denied for Non-Admin", func(t *testing.T) {
		resp, _ := client.Get(serverURL + "/admin")
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected status 403 for non-admin on /admin, got: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("User Registration Denied for Non-Admin", func(t *testing.T) {
		userPayload, _ := json.Marshal(map[string]string{"username": "hacker", "password": "password"})
		resp, _ := client.Post(serverURL+"/admin/users", "application/json", bytes.NewReader(userPayload))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected status 403 for non-admin on /admin/users, got: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Group Creation Denied for Non-Admin", func(t *testing.T) {
		groupPayload, _ := json.Marshal(map[string]string{"group_name": "hacker_group"})
		resp, _ := client.Post(serverURL+"/admin/groups", "application/json", bytes.NewReader(groupPayload))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected status 403 for non-admin on /admin/groups, got: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Database Registration Denied for Non-Admin", func(t *testing.T) {
		dbPayload, _ := json.Marshal(map[string]string{"name": "hacker_db"})
		resp, _ := client.Post(serverURL+"/admin/databases", "application/json", bytes.NewReader(dbPayload))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected status 403 for non-admin on /admin/databases, got: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Permission Modification Denied for Non-Admin", func(t *testing.T) {
		permPayload, _ := json.Marshal(map[string]any{"group_id": analystGroupID, "target_db_id": targetDatabaseID, "access_level": "ADMIN"})
		resp, _ := client.Post(serverURL+"/admin/groups/permissions", "application/json", bytes.NewReader(permPayload))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected status 403 for non-admin on /admin/groups/permissions, got: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Analyst Logout", func(t *testing.T) {
		resp, _ := client.Post(serverURL+"/logout", "application/json", nil)
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("analyst logout failed: status %v", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Developer Login", func(t *testing.T) {
		loginPayload, _ := json.Marshal(map[string]string{"username": "budi_developer", "password": "BudiPassword123!"})
		resp, err := client.Post(serverURL+"/login", "application/json", bytes.NewReader(loginPayload))
		if err != nil || resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("developer login failed: status %v", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Developer Database Access Denied for Unassigned Database", func(t *testing.T) {
		resp, _ := client.Get(fmt.Sprintf("%s/databases/9999/schemas", serverURL))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected status 403 for unassigned database, got: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Developer Migration Creation Denied for Unassigned Database", func(t *testing.T) {
		draftPayload, _ := json.Marshal(map[string]string{"sql_query": "CREATE TABLE forbidden_db (id INT);"})
		resp, _ := client.Post(fmt.Sprintf("%s/databases/9999/migrations", serverURL), "application/json", bytes.NewReader(draftPayload))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected status 403 when creating migration on unassigned database, got: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Developer Creates Table Migration Draft", func(t *testing.T) {
		draft1Payload, _ := json.Marshal(map[string]string{
			"sql_query": "CREATE TABLE public.produk (id SERIAL PRIMARY KEY, nama TEXT NOT NULL, harga INT NOT NULL);",
		})
		resp, err := client.Post(fmt.Sprintf("%s/databases/%d/migrations", serverURL, targetDatabaseID), "application/json", bytes.NewReader(draft1Payload))
		if err != nil || resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("failed to create migration draft: %d", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		draftID = loc[strings.LastIndex(loc, "/")+1:]
		resp.Body.Close()
	})

	t.Run("Developer Edits Table Migration Draft", func(t *testing.T) {
		draftEditPayload, _ := json.Marshal(map[string]string{
			"sql_query": "CREATE TABLE public.produk (id SERIAL PRIMARY KEY, nama TEXT NOT NULL, harga INT NOT NULL, stok INT DEFAULT 0);",
		})
		resp, err := client.Post(fmt.Sprintf("%s/databases/%d/migrations/%s/edit", serverURL, targetDatabaseID, draftID), "application/json", bytes.NewReader(draftEditPayload))
		if err != nil || resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("failed to update migration draft: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Developer Successfully Executes Table Migration", func(t *testing.T) {
		execPayload, _ := json.Marshal(map[string]string{"token": "budi_developer"})
		resp, err := client.Post(fmt.Sprintf("%s/databases/%d/migrations/%s/execute", serverURL, targetDatabaseID, draftID), "application/json", bytes.NewReader(execPayload))
		if err != nil || resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("failed to execute migration: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Developer Executes Seed Data Migration", func(t *testing.T) {
		draft2Payload, _ := json.Marshal(map[string]string{
			"sql_query": "INSERT INTO public.produk (nama, harga, stok) VALUES ('Laptop ThinkPad', 18500000, 10), ('Keyboard Mechanical', 1200000, 30);",
		})
		resp, _ := client.Post(fmt.Sprintf("%s/databases/%d/migrations", serverURL, targetDatabaseID), "application/json", bytes.NewReader(draft2Payload))
		resp.Body.Close()

		execPayload, _ := json.Marshal(map[string]string{"token": "budi_developer"})
		resp, _ = client.Post(fmt.Sprintf("%s/databases/%d/migrations/2/execute", serverURL, targetDatabaseID), "application/json", bytes.NewReader(execPayload))
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("failed to execute data migration: %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Display Newly Inserted Data Rows", func(t *testing.T) {
		resp, _ := client.Get(fmt.Sprintf("%s/databases/%d/schemas/public/tables/produk/data", serverURL, targetDatabaseID))
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(bodyBytes), "Laptop ThinkPad") {
			t.Fatalf("product data row does not appear in database viewer: %s", string(bodyBytes))
		}
	})

	t.Run("Failed Migration Execution Returns 500", func(t *testing.T) {
		badPayload, _ := json.Marshal(map[string]string{
			"sql_query": "CREATE TABLE public.riwayat_transaksi_temp (id INT); INSERT INTO public.riwayat_transaksi_temp VALUES ('teks_bukan_angka');",
		})
		resp, _ := client.Post(fmt.Sprintf("%s/databases/%d/migrations", serverURL, targetDatabaseID), "application/json", bytes.NewReader(badPayload))
		resp.Body.Close()

		execPayload, _ := json.Marshal(map[string]string{"token": "budi_developer"})
		resp, _ = client.Post(fmt.Sprintf("%s/databases/%d/migrations/3/execute", serverURL, targetDatabaseID), "application/json", bytes.NewReader(execPayload))
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected status 500 on failing migration, but got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Verify Rollback on Physical Database", func(t *testing.T) {
		var tabelTersisa bool
		err := targetDB.QueryRow("SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'riwayat_transaksi_temp')").Scan(&tabelTersisa)
		if err != nil {
			t.Fatalf("failed to query rollback status: %v", err)
		}
		if tabelTersisa {
			t.Fatal("ROLLBACK FAILED: table 'riwayat_transaksi_temp' still exists in target database!")
		}
	})

	t.Run("Final Logout", func(t *testing.T) {
		resp, _ := client.Post(serverURL+"/logout", "application/json", nil)
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("final logout failed: status %v", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Dashboard Access Denied After Logout", func(t *testing.T) {
		resp, _ := client.Get(serverURL + "/dashboard")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected status 401 after logout, but got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("Database Access Denied After Logout", func(t *testing.T) {
		resp, _ := client.Get(fmt.Sprintf("%s/databases/%d/schemas", serverURL, targetDatabaseID))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected status 401 after logout, but got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})
}
