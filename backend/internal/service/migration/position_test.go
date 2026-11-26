package migration

import (
	"testing"

	"wbd/internal/models"
)

func TestReplayPosition(t *testing.T) {
	a := models.MigrationExecution{ID: 1, MigrationID: 10, Action: ActionApply, Status: StatusApplied}
	b := models.MigrationExecution{ID: 2, MigrationID: 20, Action: ActionApply, Status: StatusApplied}
	c := models.MigrationExecution{ID: 3, MigrationID: 30, Action: ActionApply, Status: StatusApplied}
	undoC := models.MigrationExecution{ID: 4, MigrationID: 30, Action: ActionRollback, Status: StatusApplied, RevertsExecutionID: &c.ID}
	undoB := models.MigrationExecution{ID: 5, MigrationID: 20, Action: ActionRollback, Status: StatusApplied, RevertsExecutionID: &b.ID}
	retry := models.MigrationExecution{ID: 6, MigrationID: 20, Action: ActionForward, Status: StatusFailed}
	forward := models.MigrationExecution{ID: 7, MigrationID: 20, Action: ActionForward, Status: StatusApplied}
	history := []models.MigrationExecution{a, b, c, undoC, undoB, retry}
	position, err := replayPosition(history)
	if err != nil || len(position.active) != 1 || position.active[0].ID != a.ID ||
		len(position.forward) != 2 || position.forward[1].ID != undoB.ID {
		t.Fatalf("after failed forward: %+v, %v", position, err)
	}
	position, err = replayPosition(append(history, forward))
	if err != nil || len(position.active) != 2 || position.active[1].ID != forward.ID ||
		len(position.forward) != 1 || position.forward[0].ID != undoC.ID {
		t.Fatalf("after successful forward: %+v, %v", position, err)
	}
	if _, err := replayPosition([]models.MigrationExecution{a, b, c, undoB}); err == nil {
		t.Fatal("accepted a rollback of a non-head execution")
	}
}
