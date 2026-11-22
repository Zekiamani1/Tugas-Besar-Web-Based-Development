package interfaces

import "context"

// MigrationSubmission identifies exactly what the requester wants reviewed.
type MigrationSubmission struct {
	TargetDBID          int
	MigrationID         int
	ActorID             int
	Action              string
	Revision            int
	ExpectedExecutionID int
}

// MigrationApproval accepts a request for review. Implementations validate the
// referenced revision or execution and persist the review before returning its ID.
// Execution is scheduled separately after approval.
type MigrationApproval interface {
	SubmitMigration(context.Context, MigrationSubmission) (string, error)
}
