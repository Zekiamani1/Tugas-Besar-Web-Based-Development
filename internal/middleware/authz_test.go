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

	"wbd/internal/models"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/interfaces"
)

type mockAuthzService struct {
	interfaces.AccessControlService
	mock.Mock
}

// Compile-time check bahwa mock memenuhi AccessControlService
var _ interfaces.AccessControlService = (*mockAuthzService)(nil)

// Mock ValidateSession karena kedua middleware memakai RequireAuth
func (m *mockAuthzService) ValidateSession(
	ctx context.Context,
	sessionID string,
) (int, error) {
	args := m.Called(ctx, sessionID)
	return args.Int(0), args.Error(1)
}

func (m *mockAuthzService) ValidateSessionWithRotation(
	ctx context.Context,
	sessionID string,
) (models.SessionValidation, error) {
	args := m.Called(ctx, sessionID)
	if validation, ok := args.Get(0).(models.SessionValidation); ok {
		return validation, args.Error(1)
	}
	return models.SessionValidation{}, args.Error(1)
}

// Mock Platform Admin
func (m *mockAuthzService) IsAdmin(
	ctx context.Context,
	userID int,
) (bool, error) {
	args := m.Called(ctx, userID)
	return args.Bool(0), args.Error(1)
}

// Mock permission database
func (m *mockAuthzService) EvaluateDBAccess(
	ctx context.Context,
	userID, targetDBID int,
	requiredLevel string,
) error {
	args := m.Called(ctx, userID, targetDBID, requiredLevel)
	return args.Error(0)
}

func TestRequireAdminAccess(t *testing.T) {
	tests := []struct {
		name        string
		sessionID   string
		userID      int
		sessionErr  error
		isAdmin     bool
		adminErr    error
		checkAdmin  bool
		wantStatus  int
		wantHandler bool
	}{
		{
			name:        "admin allowed",
			sessionID:   "admin-session",
			userID:      7,
			isAdmin:     true,
			checkAdmin:  true,
			wantStatus:  http.StatusNoContent,
			wantHandler: true,
		},
		{
			name:       "non admin forbidden",
			sessionID:  "user-session",
			userID:     8,
			isAdmin:    false,
			checkAdmin: true,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "admin service error",
			sessionID:  "admin-session",
			userID:     7,
			isAdmin:    true,
			adminErr:   errors.New("database unavailable"),
			checkAdmin: true,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "missing cookie",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid session",
			sessionID:  "invalid-session",
			sessionErr: accesscontrol.ErrInvalidSession,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid user ID",
			sessionID:  "session-zero",
			userID:     0,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := new(mockAuthzService)

			// RequireAuth  melakukan validasi session
			if tt.sessionID != "" {
				ac.On(
					"ValidateSessionWithRotation",
					mock.Anything,
					tt.sessionID,
				).Return(models.SessionValidation{UserID: tt.userID, CurrentSessionID: tt.sessionID}, tt.sessionErr).Once()
			}

			// Hanya session valid yang boleh sampai ke IsAdmin
			if tt.checkAdmin {
				ac.On(
					"IsAdmin",
					mock.Anything,
					tt.userID,
				).Return(tt.isAdmin, tt.adminErr).Once()
			}

			nextCalled := false

			next := func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true

				userID, ok := userIDFromContext(r.Context())
				assert.True(t, ok)
				assert.Equal(t, tt.userID, userID)

				w.WriteHeader(http.StatusNoContent)
			}

			req := httptest.NewRequest(
				http.MethodGet,
				"/admin",
				nil,
			)

			if tt.sessionID != "" {
				req.AddCookie(&http.Cookie{
					Name:  SessionCookieName,
					Value: tt.sessionID,
				})
			}

			rec := httptest.NewRecorder()

			RequireAdminAccess(ac, next).ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantHandler, nextCalled)

			ac.AssertExpectations(t)
		})
	}
}

func TestRequireDBAccess(t *testing.T) {
	tests := []struct {
		name        string
		sessionID   string
		userID      int
		sessionErr  error
		dbID        string
		level       string
		accessErr   error
		checkAccess bool
		wantStatus  int
		wantHandler bool
	}{
		{
			name:        "VIEW_ONLY allowed",
			sessionID:   "valid-session",
			userID:      7,
			dbID:        "12",
			level:       accesscontrol.AccessLevelViewOnly,
			checkAccess: true,
			wantStatus:  http.StatusNoContent,
			wantHandler: true,
		},
		{
			name:        "VIEW_EDIT allowed",
			sessionID:   "valid-session",
			userID:      7,
			dbID:        "12",
			level:       accesscontrol.AccessLevelViewEdit,
			checkAccess: true,
			wantStatus:  http.StatusNoContent,
			wantHandler: true,
		},
		{
			name:        "database access denied",
			sessionID:   "valid-session",
			userID:      7,
			dbID:        "12",
			level:       accesscontrol.AccessLevelViewEdit,
			accessErr:   accesscontrol.ErrDBAccessDenied,
			checkAccess: true,
			wantStatus:  http.StatusForbidden,
		},
		{
			name:      "wrapped database access denied",
			sessionID: "valid-session",
			userID:    7,
			dbID:      "12",
			level:     accesscontrol.AccessLevelViewEdit,
			accessErr: fmt.Errorf(
				"check permission: %w",
				accesscontrol.ErrDBAccessDenied,
			),
			checkAccess: true,
			wantStatus:  http.StatusForbidden,
		},
		{
			name:        "database service error",
			sessionID:   "valid-session",
			userID:      7,
			dbID:        "12",
			level:       accesscontrol.AccessLevelViewOnly,
			accessErr:   errors.New("database unavailable"),
			checkAccess: true,
			wantStatus:  http.StatusInternalServerError,
		},
		{
			name:       "invalid database ID",
			sessionID:  "valid-session",
			userID:     7,
			dbID:       "abc",
			level:      accesscontrol.AccessLevelViewOnly,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "zero database ID",
			sessionID:  "valid-session",
			userID:     7,
			dbID:       "0",
			level:      accesscontrol.AccessLevelViewOnly,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "negative database ID",
			sessionID:  "valid-session",
			userID:     7,
			dbID:       "-5",
			level:      accesscontrol.AccessLevelViewOnly,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing database ID",
			sessionID:  "valid-session",
			userID:     7,
			dbID:       "",
			level:      accesscontrol.AccessLevelViewOnly,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing session cookie",
			dbID:       "12",
			level:      accesscontrol.AccessLevelViewOnly,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid session",
			sessionID:  "invalid-session",
			sessionErr: accesscontrol.ErrInvalidSession,
			dbID:       "12",
			level:      accesscontrol.AccessLevelViewOnly,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid user ID",
			sessionID:  "session-zero",
			userID:     0,
			dbID:       "12",
			level:      accesscontrol.AccessLevelViewOnly,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := new(mockAuthzService)

			if tt.sessionID != "" {
				ac.On(
					"ValidateSessionWithRotation",
					mock.Anything,
					tt.sessionID,
				).Return(models.SessionValidation{UserID: tt.userID, CurrentSessionID: tt.sessionID}, tt.sessionErr).Once()
			}

			if tt.checkAccess {
				ac.On(
					"EvaluateDBAccess",
					mock.Anything,
					tt.userID,
					12,
					tt.level,
				).Return(tt.accessErr).Once()
			}

			nextCalled := false

			next := func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true

				userID, ok := userIDFromContext(r.Context())
				assert.True(t, ok)
				assert.Equal(t, tt.userID, userID)

				w.WriteHeader(http.StatusNoContent)
			}

			req := httptest.NewRequest(
				http.MethodGet,
				"/databases/12/schemas",
				nil,
			)

			req.SetPathValue("dbID", tt.dbID)

			if tt.sessionID != "" {
				req.AddCookie(&http.Cookie{
					Name:  SessionCookieName,
					Value: tt.sessionID,
				})
			}

			rec := httptest.NewRecorder()

			RequireDBAccess(
				ac,
				tt.level,
				next,
			).ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantHandler, nextCalled)

			ac.AssertExpectations(t)
		})
	}
}
