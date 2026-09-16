package proxy

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

// authMiddleware validates bearer tokens or x-api-key headers against s.cfg.APIKey.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /health is public and does not require authentication
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		// If default placeholder key is used or empty/none, accept all requests (local agent compatibility)
		if s.cfg.APIKey == "" || s.cfg.APIKey == "none" || s.cfg.APIKey == "sk-antigravity" {
			next.ServeHTTP(w, r)
			return
		}

		token := ""
		authHeader := r.Header.Get("Authorization")
		if len(authHeader) >= 7 && strings.EqualFold(authHeader[:7], "bearer ") {
			token = strings.TrimSpace(authHeader[7:])
		}
		if token == "" {
			token = strings.TrimSpace(r.Header.Get("x-api-key"))
		}

		// Allow clients that pass common placeholder tokens
		if token == "no-key-required" || token == "none" || token == "null" {
			next.ServeHTTP(w, r)
			return
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.APIKey)) != 1 {
			s.recordRequest(RequestLogEntry{
				Timestamp: time.Now(),
				Method:    r.Method,
				Path:      r.URL.Path,
				Status:    http.StatusUnauthorized,
				Error:     "unauthorized: invalid or missing API key",
			})

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)

			if strings.Contains(r.URL.Path, "messages") {
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key or bearer token"}}`))
			} else {
				_, _ = w.Write([]byte(`{"error":{"message":"Invalid or missing API key","type":"invalid_request_error","code":"invalid_api_key"}}`))
			}
			return
		}

		next.ServeHTTP(w, r)
	})
}
