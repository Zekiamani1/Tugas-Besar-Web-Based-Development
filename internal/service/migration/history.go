package migration

import "wbd/internal/models"

func HideRollbackPairs(history []models.MigrationRecord) []models.MigrationRecord {
	applied := make(map[int]models.MigrationRecord)
	for _, entry := range history {
		if entry.ExecutionID > 0 && entry.Status == StatusApplied &&
			(entry.Action == ActionApply || entry.Action == ActionForward) {
			applied[entry.ExecutionID] = entry
		}
	}

	hidden := make(map[int]bool)
	for _, entry := range history {
		if entry.ExecutionID <= 0 || entry.Action != ActionRollback ||
			entry.Status != StatusApplied || entry.RevertsExecutionID == nil {
			continue
		}
		target, ok := applied[*entry.RevertsExecutionID]
		if ok && target.ExecutionID < entry.ExecutionID &&
			target.ID == entry.ID && target.TargetDBID == entry.TargetDBID {
			hidden[entry.ExecutionID] = true
			hidden[target.ExecutionID] = true
		}
	}

	visible := make([]models.MigrationRecord, 0, len(history))
	for _, entry := range history {
		if !hidden[entry.ExecutionID] {
			visible = append(visible, entry)
		}
	}
	return visible
}
