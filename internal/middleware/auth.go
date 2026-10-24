package middleware

import (
	"context"
	"errors"
	"net/http"
	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/interfaces"
)

const SessionCookieName = "session_id"

type contextKey string

const userIDContextKey contextKey = "authenticated-user-id"

//helper ambil userid
func userIDFromContext(ctx context.Context) (int, bool) {
	userID, ok := ctx.Value(userIDContextKey).(int)

	return userID, ok && userID > 0
}

func RequireAuth(ac interfaces.AccessControlService, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//validate session using access control's validatesession

		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		userID, err := ac.ValidateSession(r.Context(), cookie.Value)
		if errors.Is(err, accesscontrol.ErrInvalidSession) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if userID <= 0 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userIDContextKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
