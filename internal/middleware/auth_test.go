package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"wbd/internal/models"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/interfaces"
)

type mockAuthService struct {
	interfaces.AccessControlService
	mock.Mock
}

// Compile-time check bahwa mockAuthService memenuhi AccessControlService.
var _ interfaces.AccessControlService = (*mockAuthService)(nil)

func (m *mockAuthService) ValidateSession(
	ctx context.Context,
	sessionID string,
) (int, error) {
	args := m.Called(ctx, sessionID)
	return args.Int(0), args.Error(1)
}

func (m *mockAuthService) ValidateSessionWithRotation(
	ctx context.Context,
	sessionID string,
) (models.SessionValidation, error) {
	args := m.Called(ctx, sessionID)
	if validation, ok := args.Get(0).(models.SessionValidation); ok {
		return validation, args.Error(1)
	}
	return models.SessionValidation{}, args.Error(1)
}

// Test request tanpa session cookie
func TestRequireAuthMissingCookie(t *testing.T) {
	ac := new(mockAuthService)

	nextCalled := false
	next := func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()

	RequireAuth(ac, next).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, nextCalled)

	ac.AssertNotCalled(t, "ValidateSessionWithRotation", mock.Anything, mock.Anything)
}

func TestRequireAuthBrowserRedirectsToLogin(t *testing.T) {
	ac := new(mockAuthService)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	RequireAuth(ac, func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthenticated browser reached handler")
	}).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login?reason=session", rec.Header().Get("Location"))
	ac.AssertNotCalled(t, "ValidateSessionWithRotation", mock.Anything, mock.Anything)
}

// Test session ID tidak valid.
func TestRequireAuthInvalidSession(t *testing.T) {
	ac := new(mockAuthService)

	ac.On(
		"ValidateSessionWithRotation",
		mock.Anything,
		"invalid-token",
	).Return(models.SessionValidation{}, accesscontrol.ErrInvalidSession).Once()

	nextCalled := false
	next := func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: "invalid-token",
	})

	rec := httptest.NewRecorder()

	RequireAuth(ac, next).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, nextCalled)
	ac.AssertExpectations(t)
}

// Test error session yang dibungkus.
func TestRequireAuthWrappedInvalidSession(t *testing.T) {
	ac := new(mockAuthService)

	wrappedErr := fmt.Errorf(
		"validate session: %w",
		accesscontrol.ErrInvalidSession,
	)

	ac.On(
		"ValidateSessionWithRotation",
		mock.Anything,
		"expired-token",
	).Return(models.SessionValidation{}, wrappedErr).Once()

	nextCalled := false
	next := func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: "expired-token",
	})

	rec := httptest.NewRecorder()

	RequireAuth(ac, next).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, nextCalled)
	ac.AssertExpectations(t)
}

// Test ketika service mengalami error internal.
func TestRequireAuthServiceError(t *testing.T) {
	ac := new(mockAuthService)

	ac.On(
		"ValidateSessionWithRotation",
		mock.Anything,
		"valid-token",
	).Return(models.SessionValidation{}, errors.New("database unavailable")).Once()

	nextCalled := false
	next := func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: "valid-token",
	})

	rec := httptest.NewRecorder()

	RequireAuth(ac, next).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.False(t, nextCalled)
	ac.AssertExpectations(t)
}

// Test ketika service berhasil, tetapi UserID tidak valid.
func TestRequireAuthInvalidUserID(t *testing.T) {
	ac := new(mockAuthService)

	ac.On(
		"ValidateSessionWithRotation",
		mock.Anything,
		"valid-token",
	).Return(models.SessionValidation{}, nil).Once()

	nextCalled := false
	next := func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: "valid-token",
	})

	rec := httptest.NewRecorder()

	RequireAuth(ac, next).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, nextCalled)
	ac.AssertExpectations(t)
}

// Test session valid dan UserID diteruskan ke context.
func TestRequireAuthValidSession(t *testing.T) {
	ac := new(mockAuthService)

	ac.On(
		"ValidateSessionWithRotation",
		mock.Anything,
		"valid-token",
	).Return(models.SessionValidation{UserID: 7, CurrentSessionID: "valid-token"}, nil).Once()

	nextCalled := false

	next := func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true

		userID, ok := userIDFromContext(r.Context())

		require.True(t, ok)
		assert.Equal(t, 7, userID)

		w.WriteHeader(http.StatusOK)
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: "valid-token",
	})

	rec := httptest.NewRecorder()

	RequireAuth(ac, next).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, nextCalled)
	ac.AssertExpectations(t)
}

func TestRequireAuthRefreshesRotatedSessionCookie(t *testing.T) {
	ac := new(mockAuthService)
	ac.On(
		"ValidateSessionWithRotation",
		mock.Anything,
		"previous-token",
	).Return(models.SessionValidation{UserID: 7, CurrentSessionID: "current-token", UsedGracePeriod: true}, nil).Once()

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "previous-token"})
	rec := httptest.NewRecorder()
	RequireAuth(ac, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, true).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == SessionCookieName {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected middleware to refresh session cookie")
	}
	if sessionCookie.Value != "current-token" || !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected refreshed session cookie: %+v", sessionCookie)
	}
	ac.AssertExpectations(t)
}

// Test helper userIDFromContext.
func TestUserIDFromContext(t *testing.T) {
	t.Run("valid user ID", func(t *testing.T) {
		ctx := context.WithValue(
			context.Background(),
			userIDContextKey,
			7,
		)

		userID, ok := userIDFromContext(ctx)

		assert.True(t, ok)
		assert.Equal(t, 7, userID)
	})

	t.Run("missing user ID", func(t *testing.T) {
		userID, ok := userIDFromContext(context.Background())

		assert.False(t, ok)
		assert.Equal(t, 0, userID)
	})

	t.Run("invalid user ID", func(t *testing.T) {
		ctx := context.WithValue(
			context.Background(),
			userIDContextKey,
			0,
		)

		_, ok := userIDFromContext(ctx)

		assert.False(t, ok)
	})
}
