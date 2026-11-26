package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS(t *testing.T) {
	t.Run("allows configured origin on normal GET request", func(t *testing.T) {
		called := false
		handler := CORS([]string{"http://localhost:3000"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/databases", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called {
			t.Fatal("expected downstream handler to be called")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
			t.Errorf("Allow-Origin = %q, want http://localhost:3000", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("Allow-Credentials = %q, want true", got)
		}
		if got := rec.Header().Get("Vary"); got != "Origin" {
			t.Errorf("Vary = %q, want Origin", got)
		}
		if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "Location" {
			t.Errorf("Expose-Headers = %q, want Location", got)
		}
	})

	t.Run("handles OPTIONS preflight for allowed origin with 204", func(t *testing.T) {
		called := false
		handler := CORS([]string{"http://localhost:3000"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodOptions, "/login", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if called {
			t.Fatal("did not expect downstream handler to run for OPTIONS preflight")
		}
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
			t.Error("expected Access-Control-Allow-Methods header")
		}
		if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
			t.Error("expected Access-Control-Allow-Headers header")
		}
	})

	t.Run("rejects disallowed origin preflight and omits CORS headers on GET", func(t *testing.T) {
		called := false
		handler := CORS([]string{"http://localhost:3000"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))

		optReq := httptest.NewRequest(http.MethodOptions, "/login", nil)
		optReq.Header.Set("Origin", "http://evil.example")
		optRec := httptest.NewRecorder()
		handler.ServeHTTP(optRec, optReq)

		if called {
			t.Fatal("did not expect downstream handler on disallowed OPTIONS")
		}
		if optRec.Code != http.StatusForbidden {
			t.Fatalf("OPTIONS status = %d, want 403", optRec.Code)
		}
		if got := optRec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("unexpected Allow-Origin header: %q", got)
		}
		if got := optRec.Header().Get("Access-Control-Expose-Headers"); got != "" {
			t.Errorf("unexpected Expose-Headers on disallowed OPTIONS: %q", got)
		}

		getReq := httptest.NewRequest(http.MethodGet, "/health", nil)
		getReq.Header.Set("Origin", "http://evil.example")
		getRec := httptest.NewRecorder()
		handler.ServeHTTP(getRec, getReq)

		if !called {
			t.Fatal("expected non-preflight request to reach downstream handler")
		}
		if got := getRec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("unexpected Allow-Origin header for disallowed origin: %q", got)
		}
		if got := getRec.Header().Get("Access-Control-Expose-Headers"); got != "" {
			t.Errorf("unexpected Expose-Headers for disallowed origin: %q", got)
		}
	})

	t.Run("normalizes multiple trimmed origins and passes requests without Origin", func(t *testing.T) {
		called := 0
		handler := CORS([]string{" http://localhost:3000/ ", "https://app.example.com/"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called++
			w.WriteHeader(http.StatusOK)
		}))

		reqWithOrigin := httptest.NewRequest(http.MethodGet, "/databases", nil)
		reqWithOrigin.Header.Set("Origin", "https://app.example.com")
		recWithOrigin := httptest.NewRecorder()
		handler.ServeHTTP(recWithOrigin, reqWithOrigin)

		if got := recWithOrigin.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Errorf("Allow-Origin = %q, want https://app.example.com", got)
		}

		reqNoOrigin := httptest.NewRequest(http.MethodGet, "/health", nil)
		recNoOrigin := httptest.NewRecorder()
		handler.ServeHTTP(recNoOrigin, reqNoOrigin)

		if called != 2 {
			t.Errorf("called = %d, want 2", called)
		}
		if got := recNoOrigin.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("unexpected Allow-Origin when Origin header absent: %q", got)
		}
	})
}
