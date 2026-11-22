package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wbd/internal/middleware"
	"wbd/internal/service/interfaces"
)

type submitMigrationFunc func(context.Context, interfaces.MigrationSubmission) (string, error)

func (f submitMigrationFunc) SubmitMigration(ctx context.Context, input interfaces.MigrationSubmission) (string, error) {
	return f(ctx, input)
}

func TestMigrationSubmission(t *testing.T) {
	ac := &mockAccessControlService{
		validateSessionFunc:  func(context.Context, string) (int, error) { return 7, nil },
		evaluateDBAccessFunc: func(context.Context, int, int, string) error { return nil },
	}
	calls := 0
	approval := submitMigrationFunc(func(_ context.Context, input interfaces.MigrationSubmission) (string, error) {
		calls++
		want := interfaces.MigrationSubmission{TargetDBID: 1, MigrationID: 2, ActorID: 7, Action: "ROLLBACK", ExpectedExecutionID: 9}
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
