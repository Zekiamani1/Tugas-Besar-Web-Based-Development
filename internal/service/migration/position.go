package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"wbd/internal/models"
)

const (
	ActionRollback = "ROLLBACK"
	ActionForward  = "FORWARD"
)

var (
	ErrInvalidPosition = errors.New("action does not match the current migration position")
	ErrIrreversible    = errors.New("migration is irreversible")
)

type migrationPosition struct {
	active  []models.MigrationExecution
	forward []models.MigrationExecution
}

// replayPosition expects committed attempts in execution ID order.
func replayPosition(history []models.MigrationExecution) (migrationPosition, error) {
	var position migrationPosition
	for _, attempt := range history {
		if attempt.Status != StatusApplied {
			continue
		}
		switch attempt.Action {
		case ActionApply:
			position.active = append(position.active, attempt)
			position.forward = nil
		case ActionRollback:
			n := len(position.active)
			if n == 0 || attempt.RevertsExecutionID == nil ||
				position.active[n-1].ID != *attempt.RevertsExecutionID ||
				position.active[n-1].MigrationID != attempt.MigrationID {
				return position, fmt.Errorf("inconsistent rollback execution %d", attempt.ID)
			}
			position.active = position.active[:n-1]
			position.forward = append(position.forward, attempt)
		case ActionForward:
			n := len(position.forward)
			if n == 0 || position.forward[n-1].MigrationID != attempt.MigrationID {
				return position, fmt.Errorf("inconsistent forward execution %d", attempt.ID)
			}
			position.forward = position.forward[:n-1]
			position.active = append(position.active, attempt)
		default:
			return position, fmt.Errorf("unknown action on execution %d", attempt.ID)
		}
	}
	return position, nil
}

func loadPosition(db *gorm.DB, targetDBID int) (migrationPosition, error) {
	var history []models.MigrationExecution
	if err := db.Where("target_db_id = ? AND status = ?", targetDBID, StatusApplied).
		Order("id ASC").Find(&history).Error; err != nil {
		return migrationPosition{}, err
	}
	return replayPosition(history)
}

func (s *Service) GetActiveMigrations(ctx context.Context, targetDBID int) ([]models.MigrationExecution, error) {
	if targetDBID <= 0 {
		return nil, ErrInvalidMigration
	}
	position, err := loadPosition(s.internalDB.WithContext(ctx), targetDBID)
	return position.active, err
}

type ExecutionReference struct {
	ExecutionID int `json:"execution_id"`
	MigrationID int `json:"migration_id"`
	Revision    int `json:"revision"`
}

type Position struct {
	ActiveHead  *ExecutionReference `json:"active_head"`
	NextForward *ExecutionReference `json:"next_forward"`
}

func lastExecutionReference(stack []models.MigrationExecution) *ExecutionReference {
	if len(stack) == 0 {
		return nil
	}
	last := stack[len(stack)-1]
	return &ExecutionReference{ExecutionID: last.ID, MigrationID: last.MigrationID, Revision: last.MigrationRevision}
}

func (s *Service) GetPosition(ctx context.Context, targetDBID int) (Position, error) {
	if targetDBID <= 0 {
		return Position{}, ErrInvalidMigration
	}
	position, err := loadPosition(s.internalDB.WithContext(ctx), targetDBID)
	if err != nil {
		return Position{}, err
	}
	return Position{
		ActiveHead:  lastExecutionReference(position.active),
		NextForward: lastExecutionReference(position.forward),
	}, nil
}

// ExecuteReversal is called by the worker after permission and approval checks.
// expectedExecutionID pins the approved active execution (rollback) or the
// approved successful rollback (forward), preventing execution of stale jobs.
func (s *Service) ExecuteReversal(ctx context.Context, targetDBID, migrationID, expectedExecutionID int, action, actor string) (*models.MigrationExecution, error) {
	if targetDBID <= 0 || migrationID <= 0 || expectedExecutionID <= 0 ||
		strings.TrimSpace(actor) == "" || (action != ActionRollback && action != ActionForward) {
		return nil, ErrInvalidMigration
	}
	execution, version, err := s.reserveAttempt(ctx, targetDBID, func(meta *gorm.DB) (models.MigrationExecution, int, error) {
		position, err := loadPosition(meta, targetDBID)
		if err != nil {
			return models.MigrationExecution{}, 0, err
		}
		candidates := position.active
		if action == ActionForward {
			candidates = position.forward
		}
		if len(candidates) == 0 {
			return models.MigrationExecution{}, 0, ErrInvalidPosition
		}
		expected := candidates[len(candidates)-1]
		if expected.ID != expectedExecutionID || expected.MigrationID != migrationID {
			return models.MigrationExecution{}, 0, ErrInvalidPosition
		}
		var definition models.Migration
		if err := meta.Where("id = ? AND target_db_id = ? AND revision = ?",
			migrationID, targetDBID, expected.MigrationRevision).First(&definition).Error; err != nil {
			return models.MigrationExecution{}, 0, err
		}
		attempt := models.MigrationExecution{
			MigrationID: migrationID, MigrationRevision: definition.Revision, TargetDBID: targetDBID,
			Action: action, SQLSnapshot: definition.SQLUp, ExecutedBy: actor,
			Status: StatusRunning, StartedAt: time.Now().UTC(),
		}
		if action == ActionRollback {
			if definition.Irreversible || strings.TrimSpace(definition.SQLDown) == "" {
				return models.MigrationExecution{}, 0, ErrIrreversible
			}
			attempt.SQLSnapshot = definition.SQLDown
			attempt.RevertsExecutionID = &expected.ID
		}
		return attempt, expected.Version, nil
	})
	if err != nil {
		return nil, err
	}
	return s.runAttempt(ctx, execution, version)
}
