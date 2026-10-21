package middleware

import (
	"net/http"

	"wbd/internal/service/interfaces"
)

func RequireAdminAccess(ac interfaces.AccessControlService, next http.HandlerFunc) http.Handler {
	return RequireAuth(ac, func(w http.ResponseWriter, r *http.Request) {
		//check admin flag from session
		next.ServeHTTP(w, r)
	})
}

func RequireDBAccess(ac interfaces.AccessControlService, requiredLevel string, next http.HandlerFunc) http.Handler {
	return RequireAuth(ac, func(w http.ResponseWriter, r *http.Request) {
		//check db access level using access control's evaluatedbaccess
		next.ServeHTTP(w, r)
	})
}
