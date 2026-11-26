package migration

import (
	"testing"

	"wbd/internal/models"
)

func TestHideRollbackPairs(t *testing.T) {
	original := 2
	history := []models.MigrationRecord{
		{ID: 1, ExecutionID: 1, Action: ActionApply, Status: StatusApplied},
		{ID: 2, ExecutionID: original, Action: ActionApply, Status: StatusApplied},
		{ID: 2, ExecutionID: 3, Action: ActionRollback, Status: StatusFailed, RevertsExecutionID: &original},
		{ID: 2, ExecutionID: 4, Action: ActionRollback, Status: StatusApplied, RevertsExecutionID: &original},
		{ID: 2, ExecutionID: 5, Action: ActionForward, Status: StatusApplied},
	}
	visible := HideRollbackPairs(history)
	if len(visible) != 3 || visible[0].ExecutionID != 1 || visible[1].ExecutionID != 3 || visible[2].ExecutionID != 5 {
		t.Fatalf("unexpected filtered history: %+v", visible)
	}
	if history[1].ExecutionID != original || history[3].ExecutionID != 4 {
		t.Fatal("filter modified original history")
	}
}
