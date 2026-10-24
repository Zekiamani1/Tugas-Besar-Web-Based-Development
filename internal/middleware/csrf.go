package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"mime"
	"net/http"
	"strings"
)

const CSRFTokenCookieName = "csrf_token"
const csrfFormFieldName = "_csrf"

// CSRFProtection protects browser form submissions with a cookie-and-form token.
// JSON API requests remain usable; cross-origin JSON requests require a CORS
// preflight, and this application does not enable cross-origin access.
func CSRFProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			cookie, err := r.Cookie(CSRFTokenCookieName)
			if err != nil || !validCSRFToken(cookie.Value) {
				token, err := newCSRFToken()
				if err != nil {
					http.Error(w, "could not initialize request protection", http.StatusInternalServerError)
					return
				}
				setCSRFCookie(w, r, token)
				cookie = &http.Cookie{Name: CSRFTokenCookieName, Value: token}
			}
			// Make the token available to page renderers on the first request too.
			r = r.Clone(r.Context())
			cookies := r.Cookies()
			r.Header.Del("Cookie")
			for _, existing := range cookies {
				if existing.Name != CSRFTokenCookieName {
					r.AddCookie(existing)
				}
			}
			r.AddCookie(cookie)
			next.ServeHTTP(w, r)
			return
		}

		if isJSONRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		if !isFormContentType(r) {
			http.Error(w, "CSRF token required", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form body", http.StatusBadRequest)
			return
		}
		cookie, err := r.Cookie(CSRFTokenCookieName)
		if err != nil || !validCSRFToken(cookie.Value) {
			http.Error(w, "CSRF token required", http.StatusForbidden)
			return
		}
		provided := r.PostForm.Get(csrfFormFieldName)
		if !validCSRFToken(provided) || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(provided)) != 1 {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions || method == http.MethodTrace
}

func isJSONRequest(r *http.Request) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && (contentType == "application/json" || strings.HasSuffix(contentType, "+json"))
}

func isFormContentType(r *http.Request) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && (contentType == "application/x-www-form-urlencoded" || contentType == "multipart/form-data")
}

func newCSRFToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func validCSRFToken(token string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == 32
}

func setCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFTokenCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}
