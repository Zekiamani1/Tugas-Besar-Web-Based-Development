package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wbd/internal/middleware"
	"wbd/internal/models"
	"wbd/internal/service/interfaces"
	"wbd/internal/service/migration"
)

type reviewAPIStub struct {
	review interfaces.MigrationReview
	actor  int
}

func (s *reviewAPIStub) SubmitMigration(context.Context, interfaces.MigrationSubmission) (string, error) {
	return "4", nil
}

func (s *reviewAPIStub) GetMigrationReview(_ context.Context, dbID, migrationID, reviewID int) (interfaces.MigrationReview, error) {
	if dbID != s.review.TargetDBID || migrationID != s.review.MigrationID || reviewID != s.review.ID {
		return interfaces.MigrationReview{}, errors.New("not found")
	}
	return s.review, nil
}

func (s *reviewAPIStub) DecideMigrationReview(_ context.Context, reviewID, groupID, actorID int, decision string) error {
	if reviewID != s.review.ID || groupID != 3 || decision != "APPROVED" {
		return errors.New("invalid decision")
	}
	s.actor = actorID
	return nil
}

func TestMigrationReviewAPI(t *testing.T) {
	ac := &mockAccessControlService{
		validateSessionFunc:  func(context.Context, string) (int, error) { return 7, nil },
		evaluateDBAccessFunc: func(context.Context, int, int, string) error { return nil },
	}
	approval := &reviewAPIStub{review: interfaces.MigrationReview{ID: 4, TargetDBID: 1, MigrationID: 2, Status: "PENDING_REVIEW"}}
	app := NewAppWithConfig(ac, nil, nil, CookieConfig{MigrationApproval: approval})
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/databases/1/migrations/2/reviews/4", "", http.StatusOK},
		{http.MethodPost, "/databases/1/migrations/2/reviews/4/decision", "{\"group_id\":3,\"decision\":\"APPROVED\"}", http.StatusNoContent},
		{http.MethodGet, "/databases/1/migrations/9/reviews/4", "", http.StatusNotFound},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "session"})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s %s: got %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	if approval.actor != 7 {
		t.Fatalf("decision actor=%d", approval.actor)
	}
}

type submitMigrationFunc func(context.Context, interfaces.MigrationSubmission) (string, error)

func (f submitMigrationFunc) SubmitMigration(ctx context.Context, input interfaces.MigrationSubmission) (string, error) {
	return f(ctx, input)
}

type approvedMigrationStub struct {
	submitMigrationFunc
	read func(context.Context, string) (interfaces.MigrationSubmission, error)
}

func (s approvedMigrationStub) GetApprovedMigration(ctx context.Context, id string) (interfaces.MigrationSubmission, error) {
	return s.read(ctx, id)
}

func TestQueueApprovedMigration(t *testing.T) {
	submission := interfaces.MigrationSubmission{
		TargetDBID: 1, MigrationID: 2, ActorID: 7, Action: migration.ActionRollback, Mode: "BATCH",
		BatchSize: 25, BatchTable: "public.items", BatchAttribute: "id", ExpectedExecutionID: 9,
	}
	for _, tc := range []struct {
		name       string
		inputActor int
		readError  error
		wantError  error
	}{
		{name: "approved", inputActor: 7},
		{name: "pending or rejected", inputActor: 7, readError: migration.ErrApprovalDecision, wantError: migration.ErrApprovalDecision},
		{name: "submission differs", inputActor: 8, wantError: migration.ErrApprovalDecision},
	} {
		t.Run(tc.name, func(t *testing.T) {
			approval := approvedMigrationStub{read: func(_ context.Context, id string) (interfaces.MigrationSubmission, error) {
				if id != "4" {
					t.Fatalf("approval ID=%q", id)
				}
				return submission, tc.readError
			}}
			jobs := &mockMigrationService{submitJobFunc: func(_ context.Context, job models.MigrationJob) (int, error) {
				if tc.wantError != nil {
					t.Fatal("invalid approval reached the job submitter")
				}
				want := models.MigrationJob{
					ApprovalID: "4", TargetDBID: 1, MigrationID: 2, Actor: "7", Action: migration.ActionRollback,
					Mode: "BATCH", BatchSize: 25, BatchTable: "public.items", BatchAttribute: "id", ExpectedExecutionID: 9,
				}
				if job != want {
					t.Fatalf("job=%+v, want %+v", job, want)
				}
				return 12, nil
			}}
			app := NewAppWithConfig(nil, nil, jobs, CookieConfig{MigrationApproval: approval})
			input := submission
			input.ActorID = tc.inputActor
			id, err := app.QueueApprovedMigration(context.Background(), input, "4")
			if !errors.Is(err, tc.wantError) || (tc.wantError == nil && id != 12) || (tc.wantError != nil && id != 0) {
				t.Fatalf("job ID=%d error=%v", id, err)
			}
		})
	}
}

func TestMigrationSubmission(t *testing.T) {
	ac := &mockAccessControlService{
		validateSessionFunc:  func(context.Context, string) (int, error) { return 7, nil },
		evaluateDBAccessFunc: func(context.Context, int, int, string) error { return nil },
	}
	calls := 0
	approval := submitMigrationFunc(func(_ context.Context, input interfaces.MigrationSubmission) (string, error) {
		calls++
		want := interfaces.MigrationSubmission{TargetDBID: 1, MigrationID: 2, ActorID: 7, Action: "ROLLBACK", Mode: "STANDARD", ExpectedExecutionID: 9}
		if input != want {
			t.Fatalf("submission = %+v, want %+v", input, want)
		}
		return "review-1", nil
	})
	for _, tc := range []struct {
		body     string
		approval interfaces.MigrationApproval
		status   int
	}{
		{`{"action":"ROLLBACK","expected_execution_id":9}`, approval, http.StatusAccepted},
		{`{"action":"APPLY","revision":4}`, nil, http.StatusServiceUnavailable},
		{`{"action":"APPLY","revision":4,"actor_id":8}`, approval, http.StatusBadRequest},
		{`{"action":"FORWARD"}`, approval, http.StatusBadRequest},
	} {
		app := NewAppWithConfig(ac, nil, nil, CookieConfig{MigrationApproval: tc.approval})
		req := httptest.NewRequest(http.MethodPost, "/databases/1/migrations/2/submit", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "session"})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != tc.status || (tc.status == http.StatusAccepted && strings.TrimSpace(rec.Body.String()) != `{"review_id":"review-1"}`) {
			t.Fatalf("response = %d %s, want status %d", rec.Code, rec.Body.String(), tc.status)
		}
	}
	if calls != 1 {
		t.Fatalf("approval received %d submissions, want 1", calls)
	}
}

func TestDirectMigrationExecutionBlockedWithApproval(t *testing.T) {
	ac := &mockAccessControlService{
		validateSessionFunc:  func(context.Context, string) (int, error) { return 7, nil },
		evaluateDBAccessFunc: func(context.Context, int, int, string) error { return nil },
	}
	approval := submitMigrationFunc(func(context.Context, interfaces.MigrationSubmission) (string, error) {
		t.Fatal("direct execute should not submit a review")
		return "", nil
	})
	app := NewAppWithConfig(ac, nil, nil, CookieConfig{MigrationApproval: approval})
	req := httptest.NewRequest(http.MethodPost, "/databases/1/migrations/2/execute", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("direct execution response: %d %s", rec.Code, rec.Body.String())
	}
}
