package migration

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"wbd/internal/models"
	"wbd/internal/service/interfaces"
)

func TestApprovalRequiresEveryAccessGroup(t *testing.T) {
	svc, db, _, _ := newPostgresService(t)
	ctx := context.Background()
	if err := db.AutoMigrate(&models.Group{}, &models.GroupDBAccess{}, &models.UserGroup{}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	leader1, leader2 := 7, 8
	groups := []models.Group{{Name: "editors", LeaderID: &leader1}, {Name: "viewers", LeaderID: &leader2}}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]models.UserGroup{{GroupID: groups[0].ID, UserID: leader1}, {GroupID: groups[1].ID, UserID: leader2}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]models.GroupDBAccess{{GroupID: groups[0].ID, TargetDBID: 1, AccessLevel: "VIEW_EDIT"}, {GroupID: groups[1].ID, TargetDBID: 1, AccessLevel: "VIEW_ONLY"}}).Error; err != nil {
		t.Fatal(err)
	}
	migrationID, err := svc.CreateDraft(ctx, 1, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	submission := interfaces.MigrationSubmission{TargetDBID: 1, MigrationID: migrationID, ActorID: leader1, Action: ActionApply, Mode: "STANDARD", Revision: 1}
	reviewID, err := svc.SubmitMigration(ctx, submission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetApprovedMigration(ctx, reviewID); err == nil {
		t.Fatal("pending review was accepted as approved")
	}
	id, err := strconv.Atoi(reviewID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DecideMigrationReview(ctx, id, groups[0].ID, leader2, "APPROVED"); !errors.Is(err, ErrApprovalDecision) {
		t.Fatalf("non-leader decision: %v", err)
	}
	if err := svc.DecideMigrationReview(ctx, id, groups[0].ID, leader1, "APPROVED"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetApprovedMigration(ctx, reviewID); err == nil {
		t.Fatal("review was approved before the view-only group decided")
	}
	if err := svc.DecideMigrationReview(ctx, id, groups[1].ID, leader2, "APPROVED"); err != nil {
		t.Fatal(err)
	}
	approved, err := svc.GetApprovedMigration(ctx, reviewID)
	if err != nil || approved != submission {
		t.Fatalf("approved submission=%+v err=%v", approved, err)
	}
	var jobs int64
	if err := db.Model(&models.MigrationJob{}).Count(&jobs).Error; err != nil || jobs != 0 {
		t.Fatalf("approval unexpectedly created jobs: count=%d err=%v", jobs, err)
	}
}

func TestApprovalRejectsChangedReviewConditions(t *testing.T) {
	for _, scenario := range []string{"rejected", "leader_changed", "sql_changed", "new_view_group"} {
		t.Run(scenario, func(t *testing.T) {
			svc, db, _, _ := newPostgresService(t)
			ctx := context.Background()
			if err := db.AutoMigrate(&models.Group{}, &models.GroupDBAccess{}, &models.UserGroup{}); err != nil {
				t.Fatal(err)
			}
			if err := svc.Initialize(ctx); err != nil {
				t.Fatal(err)
			}
			leader := 7
			group := models.Group{Name: "editors", LeaderID: &leader}
			if err := db.Create(&group).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&models.UserGroup{GroupID: group.ID, UserID: leader}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&models.GroupDBAccess{GroupID: group.ID, TargetDBID: 1, AccessLevel: "VIEW_EDIT"}).Error; err != nil {
				t.Fatal(err)
			}
			migrationID, err := svc.CreateDraft(ctx, 1, "SELECT 1")
			if err != nil {
				t.Fatal(err)
			}
			submission := interfaces.MigrationSubmission{TargetDBID: 1, MigrationID: migrationID, ActorID: leader, Action: ActionApply, Mode: "STANDARD", Revision: 1}
			reviewID, err := svc.SubmitMigration(ctx, submission)
			if err != nil {
				t.Fatal(err)
			}
			reviewNumber, err := strconv.Atoi(reviewID)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := svc.ListPendingMigrationReviews(ctx, 1, leader)
			if err != nil || len(pending) != 1 || pending[0].SQLSnapshot != "SELECT 1" {
				t.Fatalf("pending=%+v err=%v", pending, err)
			}
			decision := "APPROVED"
			if scenario == "rejected" {
				decision = "REJECTED"
			}
			if err := svc.DecideMigrationReview(ctx, reviewNumber, group.ID, leader, decision); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "leader_changed":
				newLeader := 8
				if err := db.Create(&models.UserGroup{GroupID: group.ID, UserID: newLeader}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&group).Update("leader_id", newLeader).Error; err != nil {
					t.Fatal(err)
				}
			case "sql_changed":
				if err := svc.UpdateDraft(ctx, 1, migrationID, "SELECT 2"); err != nil {
					t.Fatal(err)
				}
			case "new_view_group":
				newLeader := 8
				newGroup := models.Group{Name: "viewers", LeaderID: &newLeader}
				if err := db.Create(&newGroup).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&models.UserGroup{GroupID: newGroup.ID, UserID: newLeader}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&models.GroupDBAccess{GroupID: newGroup.ID, TargetDBID: 1, AccessLevel: "VIEW_ONLY"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if _, err := svc.GetApprovedMigration(ctx, reviewID); err == nil {
				t.Fatal("invalid approval was accepted")
			}
		})
	}
}
