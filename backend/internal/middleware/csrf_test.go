package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const csrfTestToken = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestCSRFProtectionRequiresMatchingFormToken(t *testing.T) {
	nextCalled := false
	handler := CSRFProtection(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("rejects form without token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/change", strings.NewReader("value=1"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
		if nextCalled {
			t.Fatal("next handler was called without a CSRF token")
		}
	})

	t.Run("accepts matching cookie and form token", func(t *testing.T) {
		nextCalled = false
		req := httptest.NewRequest(http.MethodPost, "/change", strings.NewReader("value=1&_csrf="+csrfTestToken))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: CSRFTokenCookieName, Value: csrfTestToken})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent || !nextCalled {
			t.Fatalf("status = %d, next called = %t; want %d and true", rec.Code, nextCalled, http.StatusNoContent)
		}
	})
}
