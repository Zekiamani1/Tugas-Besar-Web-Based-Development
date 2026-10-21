package middleware

import (
	"net/http"

	"wbd/internal/service/interfaces"
)

func RequireAuth(ac interfaces.AccessControlService, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//validate session using access control's validatesession
		next.ServeHTTP(w, r)
	})
}
