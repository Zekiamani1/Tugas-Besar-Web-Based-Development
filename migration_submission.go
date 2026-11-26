package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"go.opentelemetry.io/otel"

	"wbd/internal/middleware"
	"wbd/internal/models"
	"wbd/internal/service/interfaces"
	"wbd/internal/service/migration"
)

var ErrApprovedMigrationQueueIntegration = errors.New("approved migration queue integration unavailable")

func (app *App) QueueApprovedMigration(ctx context.Context, submission interfaces.MigrationSubmission, approvalID string) (int, error) {
	reader, ok := app.migrationApproval.(interfaces.ApprovedMigrationReader)
	if !ok {
		return 0, ErrApprovedMigrationQueueIntegration
	}
	jobs, ok := app.MigrationService.(interface {
		SubmitJob(context.Context, models.MigrationJob) (int, error)
	})
	if !ok {
		return 0, ErrApprovedMigrationQueueIntegration
	}
	id, err := strconv.Atoi(approvalID)
	if err != nil || id <= 0 {
		return 0, migration.ErrApprovalDecision
	}
	approvalID = strconv.Itoa(id)
	approved, err := reader.GetApprovedMigration(ctx, approvalID)
	if err != nil {
		return 0, err
	}
	if approved != submission {
		return 0, migration.ErrApprovalDecision
	}
	ctx, span := otel.Tracer("wbd/async").Start(ctx, "api.submit")
	defer span.End()
	return jobs.SubmitJob(ctx, models.MigrationJob{
		ApprovalID: approvalID, TargetDBID: approved.TargetDBID, MigrationID: approved.MigrationID,
		Actor: strconv.Itoa(approved.ActorID), Action: approved.Action, Mode: approved.Mode,
		BatchSize: approved.BatchSize, BatchTable: approved.BatchTable, BatchAttribute: approved.BatchAttribute,
		Revision: approved.Revision, ExpectedExecutionID: approved.ExpectedExecutionID,
	})
}

func (app *App) handleSubmitMigration(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.AuthenticatedUserID(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	dbID, dbErr := strconv.Atoi(r.PathValue("dbID"))
	id, idErr := strconv.Atoi(r.PathValue("migrationID"))
	if dbErr != nil || idErr != nil || dbID <= 0 || id <= 0 {
		http.Error(w, "invalid database or migration ID", http.StatusBadRequest)
		return
	}

	var input struct {
		Action              string `json:"action"`
		Mode                string `json:"mode"`
		BatchSize           int    `json:"batch_size"`
		BatchTable          string `json:"batch_table"`
		BatchAttribute      string `json:"batch_attribute"`
		Revision            int    `json:"revision"`
		ExpectedExecutionID int    `json:"expected_execution_id"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if input.Mode == "" {
		input.Mode = "STANDARD"
	}
	if (input.Mode == "STANDARD" && (input.BatchSize != 0 || input.BatchTable != "" || input.BatchAttribute != "")) ||
		(input.Mode == "BATCH" && (input.BatchSize <= 0 || input.BatchSize > 10000 || input.BatchTable == "" || input.BatchAttribute == "")) ||
		(input.Mode != "STANDARD" && input.Mode != "BATCH") {
		http.Error(w, "invalid migration mode or batch configuration", http.StatusBadRequest)
		return
	}
	switch input.Action {
	case migration.ActionApply:
		if input.Revision <= 0 || input.ExpectedExecutionID != 0 {
			http.Error(w, "apply requires a draft revision", http.StatusBadRequest)
			return
		}
	case migration.ActionRollback, migration.ActionForward:
		if input.ExpectedExecutionID <= 0 || input.Revision != 0 {
			http.Error(w, "rollback and forward require an expected execution ID", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "invalid migration action", http.StatusBadRequest)
		return
	}
	if app.migrationApproval == nil {
		http.Error(w, "migration approval unavailable", http.StatusServiceUnavailable)
		return
	}
	reviewID, err := app.migrationApproval.SubmitMigration(r.Context(), interfaces.MigrationSubmission{
		TargetDBID: dbID, MigrationID: id, ActorID: actor,
		Action: input.Action, Mode: input.Mode, BatchSize: input.BatchSize,
		BatchTable: input.BatchTable, BatchAttribute: input.BatchAttribute,
		Revision: input.Revision, ExpectedExecutionID: input.ExpectedExecutionID,
	})
	if errors.Is(err, migration.ErrInvalidJob) || errors.Is(err, migration.ErrInvalidMigration) {
		http.Error(w, "invalid migration submission", http.StatusBadRequest)
		return
	}
	if errors.Is(err, migration.ErrStaleRevision) || errors.Is(err, migration.ErrIrreversible) || errors.Is(err, migration.ErrApprovalGroupsUnavailable) {
		http.Error(w, "migration cannot be submitted for review", http.StatusConflict)
		return
	}
	if err != nil || reviewID == "" {
		http.Error(w, "unable to submit migration for review", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(struct {
		ReviewID string `json:"review_id"`
	}{ReviewID: reviewID})
}

func (app *App) migrationReviewFromRequest(w http.ResponseWriter, r *http.Request) (interfaces.MigrationReview, bool) {
	dbID, dbErr := strconv.Atoi(r.PathValue("dbID"))
	migrationID, migrationErr := strconv.Atoi(r.PathValue("migrationID"))
	reviewID, reviewErr := strconv.Atoi(r.PathValue("reviewID"))
	if dbErr != nil || migrationErr != nil || reviewErr != nil || dbID <= 0 || migrationID <= 0 || reviewID <= 0 {
		http.Error(w, "invalid review reference", http.StatusBadRequest)
		return interfaces.MigrationReview{}, false
	}
	reader, ok := app.migrationApproval.(interface {
		GetMigrationReview(context.Context, int, int, int) (interfaces.MigrationReview, error)
	})
	if !ok {
		http.Error(w, "migration review unavailable", http.StatusServiceUnavailable)
		return interfaces.MigrationReview{}, false
	}
	review, err := reader.GetMigrationReview(r.Context(), dbID, migrationID, reviewID)
	if err != nil {
		http.Error(w, "migration review not found", http.StatusNotFound)
		return interfaces.MigrationReview{}, false
	}
	return review, true
}

func (app *App) handleListPendingMigrationReviews(w http.ResponseWriter, r *http.Request) {
	dbID, err := strconv.Atoi(r.PathValue("dbID"))
	actor, authenticated := middleware.AuthenticatedUserID(r.Context())
	if err != nil || dbID <= 0 {
		http.Error(w, "invalid database ID", http.StatusBadRequest)
		return
	}
	if !authenticated {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	service, ok := app.migrationApproval.(interface {
		ListPendingMigrationReviews(context.Context, int, int) ([]interfaces.MigrationReview, error)
	})
	if !ok {
		http.Error(w, "migration review unavailable", http.StatusServiceUnavailable)
		return
	}
	reviews, err := service.ListPendingMigrationReviews(r.Context(), dbID, actor)
	if err != nil {
		http.Error(w, "unable to list migration reviews", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reviews)
}

func (app *App) handleGetMigrationReview(w http.ResponseWriter, r *http.Request) {
	review, ok := app.migrationReviewFromRequest(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(review)
}

func (app *App) handleMigrationReviewDecision(w http.ResponseWriter, r *http.Request) {
	review, ok := app.migrationReviewFromRequest(w, r)
	if !ok {
		return
	}
	actor, ok := middleware.AuthenticatedUserID(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var input struct {
		GroupID  int    `json:"group_id"`
		Decision string `json:"decision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(new(json.RawMessage)) != io.EOF || input.GroupID <= 0 || (input.Decision != "APPROVED" && input.Decision != "REJECTED") {
		http.Error(w, "invalid decision", http.StatusBadRequest)
		return
	}
	service, ok := app.migrationApproval.(interface {
		DecideMigrationReview(context.Context, int, int, int, string) error
	})
	if !ok {
		http.Error(w, "migration decision unavailable", http.StatusServiceUnavailable)
		return
	}
	retryHandoff := false
	if review.Status == "APPROVED" && input.Decision == "APPROVED" {
		for _, group := range review.Groups {
			if group.GroupID == input.GroupID && group.Status == "APPROVED" && group.DecidedBy != nil && *group.DecidedBy == actor {
				retryHandoff = true
				break
			}
		}
	}
	if !retryHandoff {
		if err := service.DecideMigrationReview(r.Context(), review.ID, input.GroupID, actor, input.Decision); err != nil {
			http.Error(w, "migration decision not permitted", http.StatusConflict)
			return
		}
		review, ok = app.migrationReviewFromRequest(w, r)
		if !ok {
			return
		}
	}
	if review.Status == "APPROVED" {
		jobID, err := app.QueueApprovedMigration(r.Context(), interfaces.MigrationSubmission{
			TargetDBID: review.TargetDBID, MigrationID: review.MigrationID, ActorID: review.ActorID,
			Action: review.Action, Mode: review.Mode, BatchSize: review.BatchSize,
			BatchTable: review.BatchTable, BatchAttribute: review.BatchAttribute,
			Revision: review.Revision, ExpectedExecutionID: review.ExpectedExecutionID,
		}, strconv.Itoa(review.ID))
		switch {
		case errors.Is(err, migration.ErrJobConflict), errors.Is(err, migration.ErrStaleRevision), errors.Is(err, migration.ErrApprovalGroupsUnavailable), errors.Is(err, migration.ErrApprovalDecision):
			http.Error(w, "approved migration cannot be queued", http.StatusConflict)
			return
		case err != nil:
			http.Error(w, "unable to queue approved migration", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Location", fmt.Sprintf("/databases/%d/migrations/%d?job_id=%d", review.TargetDBID, review.MigrationID, jobID))
	}
	w.WriteHeader(http.StatusNoContent)
}
