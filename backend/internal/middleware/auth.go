package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/interfaces"
)

const SessionCookieName = "session_id"

type contextKey string

const userIDContextKey contextKey = "authenticated-user-id"

// helper ambil userid
func userIDFromContext(ctx context.Context) (int, bool) {
	userID, ok := ctx.Value(userIDContextKey).(int)

	return userID, ok && userID > 0
}

func rejectUnauthenticated(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/login?reason=session", http.StatusSeeOther)
		return
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func RequireAuth(ac interfaces.AccessControlService, next http.HandlerFunc, secureCookie ...bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//validate session using access control's validatesession

		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			rejectUnauthenticated(w, r)
			return
		}

		validation, err := ac.ValidateSessionWithRotation(r.Context(), cookie.Value)
		if errors.Is(err, accesscontrol.ErrInvalidSession) {
			rejectUnauthenticated(w, r)
			return
		}
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if validation.UserID <= 0 {
			rejectUnauthenticated(w, r)
			return
		}
		if validation.CurrentSessionID != "" && validation.CurrentSessionID != cookie.Value {
			secure := len(secureCookie) > 0 && secureCookie[0]
			http.SetCookie(w, &http.Cookie{
				Name:     SessionCookieName,
				Value:    validation.CurrentSessionID,
				Path:     "/",
				HttpOnly: true,
				Secure:   secure,
				SameSite: http.SameSiteLaxMode,
			})
		}

		ctx := context.WithValue(r.Context(), userIDContextKey, validation.UserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
