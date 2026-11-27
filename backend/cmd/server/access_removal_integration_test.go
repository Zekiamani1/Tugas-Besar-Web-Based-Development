package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"wbd/internal/models"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/dbmanagement"
	"wbd/internal/service/migration"
)

func TestAccessRemovalPostgresIntegration(t *testing.T) {
	ctx := context.Background()
	container := startTestPostgres(t, "access_removal")
	db := openGorm(t, container)
	ac := accesscontrol.NewService(db, accesscontrol.SessionConfig{AbsoluteTTL: time.Hour, IdleTimeout: time.Hour, RotationInterval: time.Minute, GracePeriod: time.Second})
	dbm := dbmanagement.NewService(db)
	mig := migration.NewService(db, dbm)
	if err := mig.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"member", "leader"} {
		if err := ac.Register(ctx, name, "password123"); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"one", "two"} {
		if err := ac.CreateGroup(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	var member, leader models.User
	var one, two models.Group
	if err := db.Where("username = ?", "member").First(&member).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("username = ?", "leader").First(&leader).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("name = ?", "one").First(&one).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("name = ?", "two").First(&two).Error; err != nil {
		t.Fatal(err)
	}
	target := models.TargetDBInfo{Name: "test target", DBType: "postgres", DBName: "target"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	for _, group := range []models.Group{one, two} {
		if err := ac.AssignUserToGroup(ctx, member.ID, group.ID); err != nil {
			t.Fatal(err)
		}
		if err := ac.AssignUserToGroup(ctx, leader.ID, group.ID); err != nil {
			t.Fatal(err)
		}
		if err := ac.SetGroupLeader(ctx, group.ID, leader.ID); err != nil {
			t.Fatal(err)
		}
		if err := ac.SetGroupDBAccess(ctx, group.ID, target.ID, accesscontrol.AccessLevelViewEdit); err != nil {
			t.Fatal(err)
		}
	}
	app := NewAppWithConfig(ac, dbm, mig, CookieConfig{MigrationApproval: mig})
	memberCookie := loginThroughApp(t, app, "member", "password123")
	if err := db.Model(&leader).Update("is_admin", true).Error; err != nil {
		t.Fatal(err)
	}
	adminCookie := loginThroughApp(t, app, "leader", "password123")
	for _, path := range []string{fmt.Sprintf("/admin/groups/%d/members/%d", one.ID, member.ID), fmt.Sprintf("/admin/groups/%d/databases/%d/access", one.ID, target.ID)} {
		if rec := sendAccessControlRequest(t, app, http.MethodDelete, path, nil, memberCookie); rec.Code != http.StatusForbidden {
			t.Fatalf("non-admin removal: %d %s", rec.Code, rec.Body.String())
		}
	}
	if err := ac.RemoveUserFromGroup(ctx, leader.ID, one.ID); !errors.Is(err, accesscontrol.ErrGroupLeaderRemoval) {
		t.Fatalf("leader removal: %v", err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(ctx, "SELECT pg_advisory_lock(3111, $1)", target.ID); err != nil {
		t.Fatal(err)
	}
	if err := ac.RemoveUserFromGroup(ctx, member.ID, one.ID); !errors.Is(err, accesscontrol.ErrAccessChangeBusy) {
		t.Fatalf("active worker lock: %v", err)
	}
	if err := ac.RevokeGroupDBAccess(ctx, one.ID, target.ID); !errors.Is(err, accesscontrol.ErrAccessChangeBusy) {
		t.Fatalf("active worker lock: %v", err)
	}
	if _, err := lock.ExecContext(ctx, "SELECT pg_advisory_unlock(3111, $1)", target.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO migration_reviews (target_db_id, migration_id, actor_id, action, mode, sql_snapshot, status, created_at) VALUES (?, 1, ?, 'APPLY', 'STANDARD', 'SELECT 1', 'PENDING_REVIEW', now())`, target.ID, member.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := ac.RevokeGroupDBAccess(ctx, one.ID, target.ID); !errors.Is(err, accesscontrol.ErrAccessChangeBusy) {
		t.Fatalf("pending review: %v", err)
	}
	if err := ac.RemoveUserFromGroup(ctx, member.ID, one.ID); !errors.Is(err, accesscontrol.ErrAccessChangeBusy) {
		t.Fatalf("pending review member removal: %v", err)
	}
	if err := db.Exec("DELETE FROM migration_reviews WHERE target_db_id = ?", target.ID).Error; err != nil {
		t.Fatal(err)
	}
	job := models.MigrationJob{TargetDBID: target.ID, MigrationID: 1, Actor: "1", Action: "APPLY", Mode: "STANDARD", Status: "QUEUED"}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	if err := ac.RevokeGroupDBAccess(ctx, one.ID, target.ID); !errors.Is(err, accesscontrol.ErrAccessChangeBusy) {
		t.Fatalf("queued job: %v", err)
	}
	if err := db.Delete(&job).Error; err != nil {
		t.Fatal(err)
	}
	session, err := ac.Login(ctx, "member", "password123")
	if err != nil {
		t.Fatal(err)
	}
	removePath := fmt.Sprintf("/admin/groups/%d/members/%d", one.ID, member.ID)
	if rec := sendAccessControlRequest(t, app, http.MethodDelete, removePath, nil, adminCookie); rec.Code != http.StatusNoContent {
		t.Fatalf("admin member removal: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := ac.ValidateSession(ctx, session); err == nil {
		t.Fatal("session should be revoked")
	}
	if err := ac.EvaluateDBAccess(ctx, member.ID, target.ID, accesscontrol.AccessLevelViewEdit); err != nil {
		t.Fatalf("access from other group lost: %v", err)
	}
	if err := ac.RemoveUserFromGroup(ctx, member.ID, one.ID); !errors.Is(err, accesscontrol.ErrAccessRelationNotFound) {
		t.Fatalf("missing membership: %v", err)
	}
	revokePath := fmt.Sprintf("/admin/groups/%d/databases/%d/access", two.ID, target.ID)
	if rec := sendAccessControlRequest(t, app, http.MethodDelete, revokePath, nil, adminCookie); rec.Code != http.StatusNoContent {
		t.Fatalf("admin access revocation: %d %s", rec.Code, rec.Body.String())
	}
	if err := ac.EvaluateDBAccess(ctx, member.ID, target.ID, accesscontrol.AccessLevelViewOnly); !errors.Is(err, accesscontrol.ErrDBAccessDenied) {
		t.Fatalf("access should be denied: %v", err)
	}
	if err := ac.RevokeGroupDBAccess(ctx, two.ID, target.ID); !errors.Is(err, accesscontrol.ErrAccessRelationNotFound) {
		t.Fatalf("missing permission: %v", err)
	}
	if err := db.Exec("DROP TABLE migration_reviews").Error; err != nil {
		t.Fatal(err)
	}
	if err := ac.RevokeGroupDBAccess(ctx, one.ID, target.ID); err == nil {
		t.Fatal("revocation succeeded without review checks")
	}
	if err := ac.EvaluateDBAccess(ctx, leader.ID, target.ID, accesscontrol.AccessLevelViewOnly); err != nil {
		t.Fatalf("failed revocation removed access: %v", err)
	}
}
