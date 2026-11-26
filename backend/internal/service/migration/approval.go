package migration

import (
	"context"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wbd/internal/models"
	"wbd/internal/service/interfaces"
)

type migrationReview struct {
	ID                  int    `gorm:"primaryKey"`
	TargetDBID          int    `gorm:"not null;index"`
	MigrationID         int    `gorm:"not null;index"`
	ActorID             int    `gorm:"not null"`
	Action              string `gorm:"not null"`
	Mode                string `gorm:"not null"`
	BatchSize           int
	BatchTable          string
	BatchAttribute      string
	Revision            int
	ExpectedExecutionID int
	SQLSnapshot         string `gorm:"not null"`
	Status              string `gorm:"not null"`
	CreatedAt           time.Time
}

type migrationReviewGroup struct {
	ReviewID  int    `gorm:"primaryKey;autoIncrement:false"`
	GroupID   int    `gorm:"primaryKey;autoIncrement:false"`
	Status    string `gorm:"not null"`
	DecidedBy *int
	DecidedAt *time.Time
}

var ErrApprovalGroupsUnavailable = errors.New("all database access groups must have a leader")
var ErrApprovalDecision = errors.New("approval decision is not permitted")

func (s *Service) GetApprovedMigration(ctx context.Context, approvalID string) (interfaces.MigrationSubmission, error) {
	id, err := strconv.Atoi(approvalID)
	if err != nil || id <= 0 {
		return interfaces.MigrationSubmission{}, ErrApprovalDecision
	}
	var review migrationReview
	if err := s.internalDB.WithContext(ctx).Where("id = ? AND status = ?", id, "APPROVED").First(&review).Error; err != nil {
		return interfaces.MigrationSubmission{}, err
	}
	var invalid int64
	err = s.internalDB.WithContext(ctx).Raw(`SELECT COUNT(*) FROM group_db_accesses a
		LEFT JOIN migration_review_groups r ON r.group_id = a.group_id AND r.review_id = ?
		LEFT JOIN groups g ON g.id = a.group_id
		LEFT JOIN user_groups u ON u.group_id = g.id AND u.user_id = g.leader_id
		WHERE a.target_db_id = ? AND (r.group_id IS NULL OR r.status <> 'APPROVED' OR g.leader_id IS NULL OR u.user_id IS NULL OR r.decided_by IS DISTINCT FROM g.leader_id)`, id, review.TargetDBID).Scan(&invalid).Error
	if err != nil {
		return interfaces.MigrationSubmission{}, err
	}
	if invalid != 0 {
		return interfaces.MigrationSubmission{}, ErrApprovalGroupsUnavailable
	}
	var count int64
	if err := s.internalDB.WithContext(ctx).Model(&models.GroupDBAccess{}).Where("target_db_id = ?", review.TargetDBID).Count(&count).Error; err != nil {
		return interfaces.MigrationSubmission{}, err
	}
	if count == 0 {
		return interfaces.MigrationSubmission{}, ErrApprovalGroupsUnavailable
	}
	var definition models.Migration
	if err := s.internalDB.WithContext(ctx).Where("id = ? AND target_db_id = ?", review.MigrationID, review.TargetDBID).First(&definition).Error; err != nil {
		return interfaces.MigrationSubmission{}, err
	}
	query := definition.SQLUp
	if review.Action == ActionRollback {
		query = definition.SQLDown
	}
	if query != review.SQLSnapshot || (review.Action == ActionApply && definition.Revision != review.Revision) {
		return interfaces.MigrationSubmission{}, ErrStaleRevision
	}
	return interfaces.MigrationSubmission{
		TargetDBID: review.TargetDBID, MigrationID: review.MigrationID, ActorID: review.ActorID,
		Action: review.Action, Mode: review.Mode, BatchSize: review.BatchSize, BatchTable: review.BatchTable,
		BatchAttribute: review.BatchAttribute, Revision: review.Revision, ExpectedExecutionID: review.ExpectedExecutionID,
	}, nil
}

func (s *Service) GetMigrationReview(ctx context.Context, targetDBID, migrationID, reviewID int) (interfaces.MigrationReview, error) {
	var review migrationReview
	if err := s.internalDB.WithContext(ctx).Where("id = ? AND target_db_id = ? AND migration_id = ?", reviewID, targetDBID, migrationID).First(&review).Error; err != nil {
		return interfaces.MigrationReview{}, err
	}
	result := interfaces.MigrationReview{ID: review.ID, TargetDBID: review.TargetDBID, MigrationID: review.MigrationID, Status: review.Status, ActorID: review.ActorID, Action: review.Action, Mode: review.Mode, BatchSize: review.BatchSize, BatchTable: review.BatchTable, BatchAttribute: review.BatchAttribute, Revision: review.Revision, ExpectedExecutionID: review.ExpectedExecutionID, SQLSnapshot: review.SQLSnapshot, Groups: []interfaces.MigrationReviewGroup{}}
	if err := s.internalDB.WithContext(ctx).Model(&migrationReviewGroup{}).Where("review_id = ?", reviewID).Order("group_id ASC").Scan(&result.Groups).Error; err != nil {
		return interfaces.MigrationReview{}, err
	}
	return result, nil
}

func (s *Service) ListPendingMigrationReviews(ctx context.Context, targetDBID, leaderID int) ([]interfaces.MigrationReview, error) {
	if targetDBID <= 0 || leaderID <= 0 {
		return nil, ErrApprovalDecision
	}
	var reviews []migrationReview
	err := s.internalDB.WithContext(ctx).Model(&migrationReview{}).
		Where("target_db_id = ? AND status = ?", targetDBID, "PENDING_REVIEW").
		Where(`EXISTS (SELECT 1 FROM migration_review_groups r
			JOIN groups g ON g.id = r.group_id
			JOIN user_groups u ON u.group_id = g.id AND u.user_id = g.leader_id
			JOIN group_db_accesses a ON a.group_id = g.id AND a.target_db_id = migration_reviews.target_db_id
			WHERE r.review_id = migration_reviews.id AND r.status = 'PENDING' AND g.leader_id = ?)`, leaderID).
		Order("id ASC").Find(&reviews).Error
	if err != nil {
		return nil, err
	}
	result := make([]interfaces.MigrationReview, 0, len(reviews))
	for _, review := range reviews {
		detail, err := s.GetMigrationReview(ctx, review.TargetDBID, review.MigrationID, review.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, detail)
	}
	return result, nil
}

func (s *Service) SubmitMigration(ctx context.Context, submission interfaces.MigrationSubmission) (string, error) {
	if submission.TargetDBID <= 0 || submission.MigrationID <= 0 || submission.ActorID <= 0 {
		return "", ErrInvalidMigration
	}
	if (submission.Mode != "STANDARD" && submission.Mode != "BATCH") ||
		(submission.Mode == "BATCH" && (submission.BatchSize <= 0 || submission.BatchSize > 10000)) {
		return "", ErrInvalidJob
	}
	if submission.Action == ActionApply {
		if submission.Revision <= 0 || submission.ExpectedExecutionID != 0 {
			return "", ErrInvalidMigration
		}
	} else if (submission.Action != ActionRollback && submission.Action != ActionForward) || submission.Revision != 0 || submission.ExpectedExecutionID <= 0 {
		return "", ErrInvalidMigration
	}
	review := migrationReview{
		TargetDBID: submission.TargetDBID, MigrationID: submission.MigrationID, ActorID: submission.ActorID,
		Action: submission.Action, Mode: submission.Mode, BatchSize: submission.BatchSize,
		BatchTable: submission.BatchTable, BatchAttribute: submission.BatchAttribute,
		Revision: submission.Revision, ExpectedExecutionID: submission.ExpectedExecutionID, Status: "PENDING_REVIEW",
	}
	err := s.internalDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDatabase(tx, submission.TargetDBID); err != nil {
			return err
		}
		var definition models.Migration
		if err := tx.Where("id = ? AND target_db_id = ?", submission.MigrationID, submission.TargetDBID).First(&definition).Error; err != nil {
			return err
		}
		if submission.Action == ActionApply && definition.Revision != submission.Revision {
			return ErrStaleRevision
		}
		review.SQLSnapshot = definition.SQLUp
		if submission.Action != ActionApply {
			var execution models.MigrationExecution
			if err := tx.Where("id = ? AND migration_id = ? AND target_db_id = ?", submission.ExpectedExecutionID, submission.MigrationID, submission.TargetDBID).First(&execution).Error; err != nil {
				return err
			}
			if definition.Revision != execution.MigrationRevision {
				return ErrStaleRevision
			}
			if submission.Action == ActionRollback {
				if definition.Irreversible || definition.SQLDown == "" {
					return ErrIrreversible
				}
				review.SQLSnapshot = definition.SQLDown
			}
		}
		if submission.Mode == "BATCH" {
			query := definition.SQLUp
			if submission.Action == ActionRollback {
				query = definition.SQLDown
			}
			job := models.MigrationJob{BatchSize: submission.BatchSize, BatchTable: submission.BatchTable, BatchAttribute: submission.BatchAttribute}
			if err := validateBatchStatement(job, query); err != nil {
				return err
			}
		} else if submission.BatchSize != 0 || submission.BatchTable != "" || submission.BatchAttribute != "" {
			return ErrInvalidJob
		}
		var groups []struct {
			ID       int
			LeaderID *int
		}
		if err := tx.Table("groups").Select("groups.id, groups.leader_id").Joins("JOIN group_db_accesses ON group_db_accesses.group_id = groups.id").Where("group_db_accesses.target_db_id = ?", submission.TargetDBID).Scan(&groups).Error; err != nil {
			return err
		}
		if len(groups) == 0 {
			return ErrApprovalGroupsUnavailable
		}
		for _, group := range groups {
			if group.LeaderID == nil {
				return ErrApprovalGroupsUnavailable
			}
			var members int64
			if err := tx.Model(&models.UserGroup{}).Where("group_id = ? AND user_id = ?", group.ID, *group.LeaderID).Count(&members).Error; err != nil {
				return err
			}
			if members != 1 {
				return ErrApprovalGroupsUnavailable
			}
		}
		if err := tx.Create(&review).Error; err != nil {
			return err
		}
		for _, group := range groups {
			if err := tx.Create(&migrationReviewGroup{ReviewID: review.ID, GroupID: group.ID, Status: "PENDING"}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return strconv.Itoa(review.ID), nil
}

func (s *Service) DecideMigrationReview(ctx context.Context, reviewID, groupID, actorID int, decision string) error {
	if reviewID <= 0 || groupID <= 0 || actorID <= 0 || (decision != "APPROVED" && decision != "REJECTED") {
		return ErrApprovalDecision
	}
	return s.internalDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var review migrationReview
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&review, reviewID).Error; err != nil {
			return err
		}
		if review.Status != "PENDING_REVIEW" {
			return ErrApprovalDecision
		}
		var changed int64
		if err := tx.Raw(`SELECT COUNT(*) FROM group_db_accesses a
			LEFT JOIN migration_review_groups r ON r.group_id = a.group_id AND r.review_id = ?
			LEFT JOIN groups g ON g.id = a.group_id
			LEFT JOIN user_groups u ON u.group_id = g.id AND u.user_id = g.leader_id
			WHERE a.target_db_id = ? AND (r.group_id IS NULL OR g.leader_id IS NULL OR u.user_id IS NULL OR (r.status = 'APPROVED' AND r.decided_by IS DISTINCT FROM g.leader_id))`, reviewID, review.TargetDBID).Scan(&changed).Error; err != nil {
			return err
		}
		if changed != 0 {
			return ErrApprovalGroupsUnavailable
		}
		var allowed int64
		if err := tx.Table("groups").Joins("JOIN user_groups ON user_groups.group_id = groups.id AND user_groups.user_id = groups.leader_id").Joins("JOIN group_db_accesses ON group_db_accesses.group_id = groups.id").Where("groups.id = ? AND groups.leader_id = ? AND group_db_accesses.target_db_id = ?", groupID, actorID, review.TargetDBID).Count(&allowed).Error; err != nil {
			return err
		}
		if allowed != 1 {
			return ErrApprovalDecision
		}
		now := time.Now().UTC()
		result := tx.Model(&migrationReviewGroup{}).Where("review_id = ? AND group_id = ? AND status = ?", reviewID, groupID, "PENDING").Updates(map[string]any{"status": decision, "decided_by": actorID, "decided_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrApprovalDecision
		}
		if decision == "REJECTED" {
			return tx.Model(&review).Update("status", "REJECTED").Error
		}
		var pending int64
		if err := tx.Model(&migrationReviewGroup{}).Where("review_id = ? AND status <> ?", reviewID, "APPROVED").Count(&pending).Error; err != nil {
			return err
		}
		if pending == 0 {
			return tx.Model(&review).Update("status", "APPROVED").Error
		}
		return nil
	})
}
