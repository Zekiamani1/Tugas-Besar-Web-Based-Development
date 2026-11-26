package interfaces

import (
	"context"
	"time"
)

type MigrationReviewGroup struct {
	GroupID   int        `json:"group_id"`
	Status    string     `json:"status"`
	DecidedBy *int       `json:"decided_by"`
	DecidedAt *time.Time `json:"decided_at"`
}

type MigrationReview struct {
	ID                  int                    `json:"review_id"`
	TargetDBID          int                    `json:"target_db_id"`
	MigrationID         int                    `json:"migration_id"`
	Status              string                 `json:"status"`
	ActorID             int                    `json:"actor_id"`
	Action              string                 `json:"action"`
	Mode                string                 `json:"mode"`
	BatchSize           int                    `json:"batch_size"`
	BatchTable          string                 `json:"batch_table,omitempty"`
	BatchAttribute      string                 `json:"batch_attribute,omitempty"`
	Revision            int                    `json:"revision"`
	ExpectedExecutionID int                    `json:"expected_execution_id,omitempty"`
	SQLSnapshot         string                 `json:"sql_snapshot"`
	Groups              []MigrationReviewGroup `json:"groups"`
}

// MigrationSubmission identifies exactly what the requester wants reviewed.
type MigrationSubmission struct {
	TargetDBID          int
	MigrationID         int
	ActorID             int
	Action              string
	Mode                string
	BatchSize           int
	BatchTable          string
	BatchAttribute      string
	Revision            int
	ExpectedExecutionID int
}

// MigrationApproval accepts a request for review. Implementations validate the
// referenced revision or execution and persist the review before returning its ID.
// Execution is scheduled separately after approval.
type MigrationApproval interface {
	SubmitMigration(context.Context, MigrationSubmission) (string, error)
}

// ApprovedMigrationReader returns a submission only when its review is approved.
type ApprovedMigrationReader interface {
	GetApprovedMigration(context.Context, string) (MigrationSubmission, error)
}
