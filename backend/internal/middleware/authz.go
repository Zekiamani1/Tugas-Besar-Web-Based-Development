package middleware

import (
	"errors"
	"net/http"
	"strconv"

	"wbd/internal/service/accesscontrol"
	"wbd/internal/service/interfaces"
)

func RequireAdminAccess(ac interfaces.AccessControlService, next http.HandlerFunc, secureCookie ...bool) http.Handler {
	return RequireAuth(ac, func(w http.ResponseWriter, r *http.Request) {
		//check admin flag from session
		userID, ok := userIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		isAdmin, err := ac.IsAdmin(r.Context(), userID)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if !isAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)

	}, secureCookie...)
}

func RequireDBAccess(ac interfaces.AccessControlService, requiredLevel string, next http.HandlerFunc, secureCookie ...bool) http.Handler {
	return RequireAuth(ac, func(w http.ResponseWriter, r *http.Request) {
		//check db access level using access control's evaluatedbaccess
		userID, ok := userIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorize", http.StatusUnauthorized)
			return
		}
		dbID, err := strconv.Atoi(r.PathValue("dbID"))

		if err != nil || dbID <= 0 {
			http.Error(w, "invalid database ID", http.StatusBadRequest)
			return
		}
		err = ac.EvaluateDBAccess(
			r.Context(),
			userID,
			dbID,
			requiredLevel,
		)
		if errors.Is(err, accesscontrol.ErrDBAccessDenied) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r)
	}, secureCookie...)
}
