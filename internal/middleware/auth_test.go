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

	ac.AssertNotCalled(t, "ValidateSession", mock.Anything, mock.Anything)
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
	ac.AssertNotCalled(t, "ValidateSession", mock.Anything, mock.Anything)
}

// Test session ID tidak valid.
func TestRequireAuthInvalidSession(t *testing.T) {
	ac := new(mockAuthService)

	ac.On(
		"ValidateSession",
		mock.Anything,
		"invalid-token",
	).Return(0, accesscontrol.ErrInvalidSession).Once()

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
		"ValidateSession",
		mock.Anything,
		"expired-token",
	).Return(0, wrappedErr).Once()

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
		"ValidateSession",
		mock.Anything,
		"valid-token",
	).Return(0, errors.New("database unavailable")).Once()

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
		"ValidateSession",
		mock.Anything,
		"valid-token",
	).Return(0, nil).Once()

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
		"ValidateSession",
		mock.Anything,
		"valid-token",
	).Return(7, nil).Once()

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
