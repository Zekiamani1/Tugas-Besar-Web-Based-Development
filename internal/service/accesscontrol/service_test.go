package accesscontrol

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"wbd/internal/models"
	"wbd/internal/service/interfaces"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type passwordHashMatcher struct {
	password string
}

// Compile-time check bahwa *Service mengimplementasikan kontrak DBManagementService.
var _ interfaces.AccessControlService = (*Service)(nil)

func newMockGormDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open gorm with sqlmock: %v", err)
	}
	return gormDB, mock, sqlDB
}

func newMockService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, sqlDB := newMockGormDB(t)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &Service{
		db: db,
		sessionConfig: SessionConfig{
			AbsoluteTTL: time.Hour,
			IdleTimeout: 15 * time.Minute,
		},
	}, mock
}

func assertExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("not all database expectations were met: %v", err)
	}
}

func (m passwordHashMatcher) Match(value driver.Value) bool {
	var hash []byte

	switch value := value.(type) {
	case string:
		hash = []byte(value)
	case []byte:
		hash = value
	default:
		return false
	}

	return bcrypt.CompareHashAndPassword(hash, []byte(m.password)) == nil
}

func TestRegisterRejectsEmptyUsername(t *testing.T) {
	service := &Service{}

	err := service.Register(context.Background(), "", "password123")

	if err == nil {
		t.Fatal("expected an error for empty username")
	}
}

func TestRegisterRejectsEmptyPassword(t *testing.T) {
	service := &Service{}

	err := service.Register(context.Background(), "abcd", "")

	if err == nil {
		t.Fatal("expected an error for empty Password")
	}
}

func TestRegisterSucceeds(t *testing.T) {
	db, mock, rawDB := newMockGormDB(t)
	defer rawDB.Close()
	service := &Service{db: db}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "users"`).
		WithArgs("abcd", passwordHashMatcher{password: "password123"}, false).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	err := service.Register(context.Background(), "abcd", "password123")
	if err != nil {
		t.Fatalf("expected registration to succeed, got error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("not all database expectations were met: %v", err)
	}
}

func TestLoginRejectsMissingCredentials(t *testing.T) {
	service := &Service{}
	for _, input := range []struct{ username, password string }{{"", "password"}, {"alice", ""}} {
		if _, err := service.Login(context.Background(), input.username, input.password); err == nil {
			t.Fatalf("expected credentials %q/%q to be rejected", input.username, input.password)
		}
	}
}

func TestLoginRejectsUnknownUsername(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectQuery(`SELECT .* FROM "users" WHERE username = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs("alice", 1).WillReturnError(gorm.ErrRecordNotFound)

	_, err := service.Login(context.Background(), "alice", "password")
	if err == nil || err.Error() != "username atau password salah" {
		t.Fatalf("expected generic credential error, got %v", err)
	}
	assertExpectations(t, mock)
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	service, mock := newMockService(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	mock.ExpectQuery(`SELECT .* FROM "users" WHERE username = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs("alice", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "password_hash", "is_admin"}).AddRow(7, "alice", string(hash), false))

	_, err = service.Login(context.Background(), "alice", "wrong-password")
	if err == nil || err.Error() != "username atau password salah" {
		t.Fatalf("expected generic credential error, got %v", err)
	}
	assertExpectations(t, mock)
}

func TestLoginCreatesSession(t *testing.T) {
	service, mock := newMockService(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	mock.ExpectQuery(`SELECT .* FROM "users" WHERE username = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs("alice", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "password_hash", "is_admin"}).AddRow(7, "alice", string(hash), false))
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "sessions"`).
		WithArgs(7, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(4))
	mock.ExpectCommit()

	sessionID, err := service.Login(context.Background(), " alice ", "password123")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if len(sessionID) != 43 {
		t.Fatalf("expected URL-safe encoding of 32 random bytes, got %q", sessionID)
	}
	assertExpectations(t, mock)
}

func TestLoginReturnsDatabaseError(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectQuery(`SELECT .* FROM "users" WHERE username = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs("alice", 1).WillReturnError(errors.New("database unavailable"))
	if _, err := service.Login(context.Background(), "alice", "password"); err == nil {
		t.Fatal("expected database error")
	}
	assertExpectations(t, mock)
}

func TestLogoutDeletesSession(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "sessions" WHERE session_id = \$1`).
		WithArgs("session-id").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := service.Logout(context.Background(), "session-id"); err != nil {
		t.Fatalf("Logout returned error: %v", err)
	}
	assertExpectations(t, mock)
}

func TestLogoutEmptySessionIsNoOp(t *testing.T) {
	if err := (&Service{}).Logout(context.Background(), ""); err != nil {
		t.Fatalf("expected empty session logout to be a no-op, got %v", err)
	}
}

func TestValidateSessionRejectsUnknownSession(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectQuery(`SELECT .* FROM "sessions" WHERE session_id = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs("missing", 1).WillReturnError(gorm.ErrRecordNotFound)
	if _, err := service.ValidateSession(context.Background(), "missing"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expected ErrInvalidSession, got %v", err)
	}
	assertExpectations(t, mock)
}

func TestValidateSessionReturnsUserAndRefreshesIdleTimestamp(t *testing.T) {
	service, mock := newMockService(t)
	now := time.Now()
	mock.ExpectQuery(`SELECT .* FROM "sessions" WHERE session_id = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs("valid", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "session_id", "created_at", "last_seen_at", "expires_at"}).
			AddRow(4, 7, "valid", now.Add(-time.Hour), now.Add(-time.Minute), now.Add(time.Hour)))
	mock.ExpectQuery(`SELECT "id" FROM "users" WHERE "users"\."id" = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs(7, 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "sessions" SET "last_seen_at"=\$1 WHERE id = \$2`).
		WithArgs(sqlmock.AnyArg(), 4).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	userID, err := service.ValidateSession(context.Background(), "valid")
	if err != nil {
		t.Fatalf("ValidateSession returned error: %v", err)
	}
	if userID != 7 {
		t.Fatalf("expected user ID 7, got %d", userID)
	}
	assertExpectations(t, mock)
}

func TestValidateSessionRejectsExpiredAndDeletesIt(t *testing.T) {
	service, mock := newMockService(t)
	now := time.Now()
	mock.ExpectQuery(`SELECT .* FROM "sessions" WHERE session_id = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs("expired", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "session_id", "created_at", "last_seen_at", "expires_at"}).
			AddRow(4, 7, "expired", now.Add(-time.Hour), now.Add(-time.Minute), now.Add(-time.Second)))
	mock.ExpectQuery(`SELECT "id" FROM "users" WHERE "users"\."id" = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs(7, 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "sessions" WHERE "sessions"\."id" = \$1`).
		WithArgs(4).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if _, err := service.ValidateSession(context.Background(), "expired"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expected ErrInvalidSession, got %v", err)
	}
	assertExpectations(t, mock)
}

func TestCreateGroupRejectsEmptyName(t *testing.T) {
	if err := (&Service{}).CreateGroup(context.Background(), " \t"); err == nil {
		t.Fatal("expected empty group name to be rejected")
	}
}

func TestCreateGroupStoresTrimmedName(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "groups"`)).
		WithArgs("team").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))
	mock.ExpectCommit()
	if err := service.CreateGroup(context.Background(), " team "); err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	assertExpectations(t, mock)
}

func TestAssignUserToGroupRejectsInvalidIDs(t *testing.T) {
	service := &Service{}
	for _, ids := range [][2]int{{0, 1}, {1, 0}, {-1, 1}, {1, -1}} {
		if err := service.AssignUserToGroup(context.Background(), ids[0], ids[1]); err == nil {
			t.Fatalf("expected invalid IDs %v to be rejected", ids)
		}
	}
}

func TestAssignUserToGroupCreatesMembership(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT .* FROM "users" WHERE "users"\."id" = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs(7, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "username", "password_hash", "is_admin"}).AddRow(7, "alice", "hash", false))
	mock.ExpectQuery(`SELECT .* FROM "groups" WHERE "groups"\."id" = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs(2, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(2, "team"))
	mock.ExpectExec(`INSERT INTO "user_groups"`).
		WithArgs(7, 2).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := service.AssignUserToGroup(context.Background(), 7, 2); err != nil {
		t.Fatalf("AssignUserToGroup returned error: %v", err)
	}
	assertExpectations(t, mock)
}

func TestSetGroupDBAccessRejectsInvalidInput(t *testing.T) {
	service := &Service{}
	for _, input := range []struct {
		groupID, dbID int
		level         string
	}{{0, 1, AccessLevelViewOnly}, {1, 0, AccessLevelViewOnly}, {1, 1, "ADMIN"}} {
		if err := service.SetGroupDBAccess(context.Background(), input.groupID, input.dbID, input.level); err == nil {
			t.Fatalf("expected invalid input %+v to be rejected", input)
		}
	}
}

func TestSetGroupDBAccessStoresPermission(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT .* FROM "groups" WHERE "groups"\."id" = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs(2, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(2, "team"))
	mock.ExpectQuery(`SELECT .* FROM "target_db_infos" WHERE "target_db_infos"\."id" = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs(9, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "db_type", "host", "port", "user", "password", "db_name"}).AddRow(9, "db", "postgres", "localhost", 5432, "u", "p", "db"))
	mock.ExpectExec(`INSERT INTO "group_db_accesses"`).
		WithArgs(2, 9, AccessLevelViewEdit).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := service.SetGroupDBAccess(context.Background(), 2, 9, " view_edit "); err != nil {
		t.Fatalf("SetGroupDBAccess returned error: %v", err)
	}
	assertExpectations(t, mock)
}

func TestEvaluateDBAccessUsesEffectiveGroupLevel(t *testing.T) {
	for _, tt := range []struct {
		name, required string
		levels         []string
		wantDenied     bool
	}{
		{"view grants view", AccessLevelViewOnly, []string{AccessLevelViewOnly}, false},
		{"view denies edit", AccessLevelViewEdit, []string{AccessLevelViewOnly}, true},
		{"edit grants view", AccessLevelViewOnly, []string{AccessLevelViewEdit}, false},
		{"edit grants edit", AccessLevelViewEdit, []string{AccessLevelViewEdit}, false},
		{"no grant denies", AccessLevelViewOnly, nil, true},
		{"highest group level grants edit", AccessLevelViewEdit, []string{AccessLevelViewOnly, AccessLevelViewEdit}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service, mock := newMockService(t)
			rows := sqlmock.NewRows([]string{"access_level"})
			for _, level := range tt.levels {
				rows.AddRow(level)
			}
			mock.ExpectQuery(`SELECT gda\.access_level FROM user_groups AS ug JOIN group_db_accesses AS gda ON gda\.group_id = ug\.group_id WHERE ug\.user_id = \$1 AND gda\.target_db_id = \$2`).
				WithArgs(7, 9).WillReturnRows(rows)
			err := service.EvaluateDBAccess(context.Background(), 7, 9, tt.required)
			if tt.wantDenied && !errors.Is(err, ErrDBAccessDenied) {
				t.Fatalf("expected access denial, got %v", err)
			}
			if !tt.wantDenied && err != nil {
				t.Fatalf("expected access grant, got %v", err)
			}
			assertExpectations(t, mock)
		})
	}
}

func TestEvaluateDBAccessRejectsInvalidInput(t *testing.T) {
	service := &Service{}
	for _, input := range []struct {
		userID, dbID int
		level        string
	}{{0, 1, AccessLevelViewOnly}, {1, 0, AccessLevelViewOnly}, {1, 1, "ADMIN"}} {
		if err := service.EvaluateDBAccess(context.Background(), input.userID, input.dbID, input.level); err == nil {
			t.Fatalf("expected invalid input %+v to be rejected", input)
		}
	}
}

func TestIsAdminReturnsStoredRole(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectQuery(`SELECT "id","is_admin" FROM "users" WHERE "users"\."id" = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs(7, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "is_admin"}).AddRow(7, true))
	isAdmin, err := service.IsAdmin(context.Background(), 7)
	if err != nil || !isAdmin {
		t.Fatalf("expected user to be admin, got isAdmin=%v err=%v", isAdmin, err)
	}
	assertExpectations(t, mock)
}

func TestIsAdminReturnsFalseForMissingUser(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectQuery(`SELECT "id","is_admin" FROM "users" WHERE "users"\."id" = \$1 ORDER BY .* LIMIT \$2`).
		WithArgs(7, 1).WillReturnError(gorm.ErrRecordNotFound)
	isAdmin, err := service.IsAdmin(context.Background(), 7)
	if err != nil || isAdmin {
		t.Fatalf("expected missing user to be non-admin, got isAdmin=%v err=%v", isAdmin, err)
	}
	assertExpectations(t, mock)
}

func TestIsAdminRejectsNonPositiveID(t *testing.T) {
	if _, err := (&Service{}).IsAdmin(context.Background(), 0); err == nil {
		t.Fatal("expected non-positive user ID to be rejected")
	}
}

func TestRegisterReturnsInsertError(t *testing.T) {
	db, mock, rawDB := newMockGormDB(t)
	defer rawDB.Close()
	service := &Service{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "users"`).
		WithArgs("abcd", sqlmock.AnyArg(), false).
		WillReturnError(errors.New("duplicate username"))
	mock.ExpectRollback()

	err := service.Register(context.Background(), "abcd", "password123")
	if err == nil {
		t.Fatalf("expected got error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("not all database expectations were met: %v", err)
	}
}

func TestListUsersReturnsSafeUserDetailsInIDOrder(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectQuery(`SELECT "id","username","is_admin" FROM "users" ORDER BY id ASC`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "is_admin"}).
			AddRow(2, "alice", false).
			AddRow(5, "admin", true))

	users, err := service.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers returned error: %v", err)
	}
	if len(users) != 2 || users[0] != (models.UserSummary{ID: 2, Username: "alice", IsAdmin: false}) || users[1] != (models.UserSummary{ID: 5, Username: "admin", IsAdmin: true}) {
		t.Fatalf("unexpected user summaries: %+v", users)
	}
	assertExpectations(t, mock)
}

func TestListUsersReturnsEmptyList(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectQuery(`SELECT "id","username","is_admin" FROM "users" ORDER BY id ASC`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "is_admin"}))

	users, err := service.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers returned error: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("expected no users, got %+v", users)
	}
	assertExpectations(t, mock)
}

func TestListUsersReturnsDatabaseError(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectQuery(`SELECT "id","username","is_admin" FROM "users" ORDER BY id ASC`).
		WillReturnError(errors.New("database unavailable"))

	users, err := service.ListUsers(context.Background())
	if err == nil || users != nil {
		t.Fatalf("expected list error and nil result, got users=%+v err=%v", users, err)
	}
	assertExpectations(t, mock)
}

func TestListGroupsReturnsDetailsInIDOrder(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectQuery(`SELECT "id","name" FROM "groups" ORDER BY id ASC`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
			AddRow(1, "analysts").
			AddRow(3, "developers"))

	groups, err := service.ListGroups(context.Background())
	if err != nil {
		t.Fatalf("ListGroups returned error: %v", err)
	}
	if len(groups) != 2 || groups[0] != (models.GroupSummary{ID: 1, Name: "analysts"}) || groups[1] != (models.GroupSummary{ID: 3, Name: "developers"}) {
		t.Fatalf("unexpected group summaries: %+v", groups)
	}
	assertExpectations(t, mock)
}

func TestListGroupsReturnsEmptyList(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectQuery(`SELECT "id","name" FROM "groups" ORDER BY id ASC`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))

	groups, err := service.ListGroups(context.Background())
	if err != nil {
		t.Fatalf("ListGroups returned error: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("expected no groups, got %+v", groups)
	}
	assertExpectations(t, mock)
}

func TestListGroupsReturnsDatabaseError(t *testing.T) {
	service, mock := newMockService(t)
	mock.ExpectQuery(`SELECT "id","name" FROM "groups" ORDER BY id ASC`).
		WillReturnError(errors.New("database unavailable"))

	groups, err := service.ListGroups(context.Background())
	if err == nil || groups != nil {
		t.Fatalf("expected list error and nil result, got groups=%+v err=%v", groups, err)
	}
	assertExpectations(t, mock)
}
