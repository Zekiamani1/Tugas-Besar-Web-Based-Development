package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"wbd/internal/middleware"
	"wbd/internal/service/interfaces"
	"wbd/internal/service/migration"
)

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
		Revision            int    `json:"revision"`
		ExpectedExecutionID int    `json:"expected_execution_id"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		http.Error(w, "invalid request body", http.StatusBadRequest)
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
		Action: input.Action, Revision: input.Revision, ExpectedExecutionID: input.ExpectedExecutionID,
	})
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
