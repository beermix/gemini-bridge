package account

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuth_RefreshAccessToken_Success(t *testing.T) {
	var requestedForm url.Values
	var requestedContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		requestedForm, _ = url.ParseQuery(string(body))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "new-access-token-xyz",
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	}))
	defer server.Close()

	restore := SetTokenURLForTest(server.URL)
	defer restore()

	acc := &CloudAccount{
		Email: "test@example.com",
		Token: CloudToken{
			RefreshToken: "valid-refresh-token",
			ProjectID:    "preserve-me",
		},
	}

	token, err := RefreshAccessToken(acc, "")
	if err != nil {
		t.Fatalf("RefreshAccessToken failed: %v", err)
	}

	if !strings.HasPrefix(requestedContentType, "application/x-www-form-urlencoded") {
		t.Errorf("expected Content-Type application/x-www-form-urlencoded, got %q", requestedContentType)
	}
	if requestedForm.Get("grant_type") != "refresh_token" {
		t.Errorf("expected grant_type=refresh_token, got %q", requestedForm.Get("grant_type"))
	}
	if requestedForm.Get("client_id") != DefaultOAuthClientID {
		t.Errorf("expected client_id=%s, got %q", DefaultOAuthClientID, requestedForm.Get("client_id"))
	}
	if requestedForm.Get("client_secret") != DefaultOAuthClientSecret {
		t.Errorf("expected client_secret=%s, got %q", DefaultOAuthClientSecret, requestedForm.Get("client_secret"))
	}
	if requestedForm.Get("refresh_token") != "valid-refresh-token" {
		t.Errorf("expected refresh_token=valid-refresh-token, got %q", requestedForm.Get("refresh_token"))
	}

	if token.AccessToken != "new-access-token-xyz" {
		t.Errorf("expected AccessToken=new-access-token-xyz, got %q", token.AccessToken)
	}
	if token.TokenType != "Bearer" {
		t.Errorf("expected TokenType=Bearer, got %q", token.TokenType)
	}
	if token.ExpiresIn != 3600 {
		t.Errorf("expected ExpiresIn=3600, got %d", token.ExpiresIn)
	}
	if token.ExpiryTimestamp <= time.Now().Unix() {
		t.Errorf("expected ExpiryTimestamp to be in future, got %d", token.ExpiryTimestamp)
	}
	if token.ProjectID != "preserve-me" {
		t.Errorf("expected ProjectID preserved as preserve-me, got %q", token.ProjectID)
	}
	if acc.Token.AccessToken != "new-access-token-xyz" {
		t.Errorf("expected account.Token.AccessToken to be updated, got %q", acc.Token.AccessToken)
	}
}

func TestOAuth_RefreshAccessToken_Rotation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "new-access-token-rot",
			"refresh_token": "rotated-refresh-token-123",
			"expires_in":    1800,
			"token_type":    "Bearer",
		})
	}))
	defer server.Close()

	restore := SetTokenURLForTest(server.URL)
	defer restore()

	acc := &CloudAccount{
		Email: "test@example.com",
		Token: CloudToken{
			RefreshToken: "old-refresh-token",
		},
	}

	token, err := RefreshAccessToken(acc, "")
	if err != nil {
		t.Fatalf("RefreshAccessToken failed: %v", err)
	}

	if token.RefreshToken != "rotated-refresh-token-123" {
		t.Errorf("expected rotated refresh token, got %q", token.RefreshToken)
	}
	if acc.Token.RefreshToken != "rotated-refresh-token-123" {
		t.Errorf("expected account refresh token to be updated, got %q", acc.Token.RefreshToken)
	}
}

func TestOAuth_RefreshAccessToken_Errors(t *testing.T) {
	t.Run("nil account", func(t *testing.T) {
		_, err := RefreshAccessToken(nil, "")
		if err == nil {
			t.Fatal("expected error for nil account, got nil")
		}
	})

	t.Run("empty refresh token", func(t *testing.T) {
		acc := &CloudAccount{Email: "test@example.com"}
		_, err := RefreshAccessToken(acc, "")
		if err == nil {
			t.Fatal("expected error for empty refresh token, got nil")
		}
	})

	t.Run("http 400 invalid grant", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
		}))
		defer server.Close()

		restore := SetTokenURLForTest(server.URL)
		defer restore()

		acc := &CloudAccount{Email: "test@example.com", Token: CloudToken{RefreshToken: "revoked-token"}}
		_, err := RefreshAccessToken(acc, "")
		if err == nil {
			t.Fatal("expected error for HTTP 400, got nil")
		}
		if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "invalid_grant") {
			t.Errorf("expected error message containing status 400 and invalid_grant, got %v", err)
		}
	})

	t.Run("http 500 server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Internal Server Error"))
		}))
		defer server.Close()

		restore := SetTokenURLForTest(server.URL)
		defer restore()

		acc := &CloudAccount{Email: "test@example.com", Token: CloudToken{RefreshToken: "test-token"}}
		_, err := RefreshAccessToken(acc, "")
		if err == nil {
			t.Fatal("expected error for HTTP 500, got nil")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("expected error containing 500, got %v", err)
		}
	})

	t.Run("invalid json response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not json"))
		}))
		defer server.Close()

		restore := SetTokenURLForTest(server.URL)
		defer restore()

		acc := &CloudAccount{Email: "test@example.com", Token: CloudToken{RefreshToken: "test-token"}}
		_, err := RefreshAccessToken(acc, "")
		if err == nil {
			t.Fatal("expected error for invalid json, got nil")
		}
	})

	t.Run("missing access token in response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"expires_in":3600}`))
		}))
		defer server.Close()

		restore := SetTokenURLForTest(server.URL)
		defer restore()

		acc := &CloudAccount{Email: "test@example.com", Token: CloudToken{RefreshToken: "test-token"}}
		_, err := RefreshAccessToken(acc, "")
		if err == nil {
			t.Fatal("expected error for missing access token, got nil")
		}
	})
}

func TestOAuth_FetchProjectID_Success(t *testing.T) {
	var requestedAuthHeader string
	var requestedUserAgent string
	var requestedContentType string
	var requestedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedAuthHeader = r.Header.Get("Authorization")
		requestedUserAgent = r.Header.Get("User-Agent")
		requestedContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&requestedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"cloudaicompanionProject": "discovered-project-789",
		})
	}))
	defer server.Close()

	restore := SetLoadCodeAssistURLForTest(server.URL)
	defer restore()

	acc := &CloudAccount{
		Email: "test@example.com",
		Token: CloudToken{
			AccessToken: "valid-access-token",
		},
	}

	projectID, err := FetchProjectID(acc, "")
	if err != nil {
		t.Fatalf("FetchProjectID failed: %v", err)
	}

	if projectID != "discovered-project-789" {
		t.Errorf("expected projectID=discovered-project-789, got %q", projectID)
	}
	if acc.Token.ProjectID != "discovered-project-789" {
		t.Errorf("expected acc.Token.ProjectID to be updated, got %q", acc.Token.ProjectID)
	}

	if requestedAuthHeader != "Bearer valid-access-token" {
		t.Errorf("expected Authorization: Bearer valid-access-token, got %q", requestedAuthHeader)
	}
	expectedUA := "antigravity/" + runtime.GOOS + "/" + runtime.GOARCH
	if requestedUserAgent != expectedUA {
		t.Errorf("expected User-Agent %q, got %q", expectedUA, requestedUserAgent)
	}
	if !strings.HasPrefix(requestedContentType, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", requestedContentType)
	}

	meta, ok := requestedBody["metadata"].(map[string]interface{})
	if !ok || meta["ideType"] != "ANTIGRAVITY" {
		t.Errorf("expected body metadata.ideType=ANTIGRAVITY, got %v", requestedBody)
	}
}

func TestOAuth_FetchProjectID_Errors(t *testing.T) {
	t.Run("nil account", func(t *testing.T) {
		_, err := FetchProjectID(nil, "")
		if err == nil {
			t.Fatal("expected error for nil account, got nil")
		}
	})

	t.Run("empty access token", func(t *testing.T) {
		acc := &CloudAccount{Email: "test@example.com"}
		_, err := FetchProjectID(acc, "")
		if err == nil {
			t.Fatal("expected error for empty access token, got nil")
		}
	})

	t.Run("http 403 error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"forbidden"}`))
		}))
		defer server.Close()

		restore := SetLoadCodeAssistURLForTest(server.URL)
		defer restore()

		acc := &CloudAccount{Email: "test@example.com", Token: CloudToken{AccessToken: "token"}}
		_, err := FetchProjectID(acc, "")
		if err == nil {
			t.Fatal("expected error for HTTP 403, got nil")
		}
		if !strings.Contains(err.Error(), "403") {
			t.Errorf("expected error to contain 403, got %v", err)
		}
	})

	t.Run("missing project ID in response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()

		restore := SetLoadCodeAssistURLForTest(server.URL)
		defer restore()

		acc := &CloudAccount{Email: "test@example.com", Token: CloudToken{AccessToken: "token"}}
		_, err := FetchProjectID(acc, "")
		if err == nil {
			t.Fatal("expected error for missing project ID, got nil")
		}
	})
}

func TestOAuth_ProxySupport(t *testing.T) {
	var proxyHits int64
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":            "proxy-token",
			"expires_in":              3600,
			"token_type":              "Bearer",
			"cloudaicompanionProject": "proxy-project",
		})
	}))
	defer targetServer.Close()

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&proxyHits, 1)
		// Forward request to target server
		req, err := http.NewRequest(r.Method, r.RequestURI, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for k, v := range r.Header {
			req.Header[k] = v
		}
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxyServer.Close()

	restoreToken := SetTokenURLForTest(targetServer.URL)
	defer restoreToken()
	restoreProject := SetLoadCodeAssistURLForTest(targetServer.URL)
	defer restoreProject()

	// 1. Test RefreshAccessToken with proxyURL parameter
	acc1 := &CloudAccount{
		Email: "proxy1@example.com",
		Token: CloudToken{RefreshToken: "refresh-tok-1"},
	}
	_, err := RefreshAccessToken(acc1, proxyServer.URL)
	if err != nil {
		t.Fatalf("RefreshAccessToken with proxy failed: %v", err)
	}

	// 2. Test FetchProjectID with account.ProxyURL
	acc2 := &CloudAccount{
		Email:    "proxy2@example.com",
		ProxyURL: proxyServer.URL,
		Token:    CloudToken{AccessToken: "access-tok-2"},
	}
	_, err = FetchProjectID(acc2, "")
	if err != nil {
		t.Fatalf("FetchProjectID with account.ProxyURL failed: %v", err)
	}

	if hits := atomic.LoadInt64(&proxyHits); hits != 2 {
		t.Errorf("expected 2 proxy hits, got %d", hits)
	}
}

func TestOAuth_InvalidProxyURL(t *testing.T) {
	acc := &CloudAccount{
		Email: "test@example.com",
		Token: CloudToken{RefreshToken: "tok", AccessToken: "tok"},
	}
	invalidProxy := "http://[invalid-host-bracket"

	_, err := RefreshAccessToken(acc, invalidProxy)
	if err == nil {
		t.Error("expected error for invalid proxy URL in RefreshAccessToken")
	}

	_, err = FetchProjectID(acc, invalidProxy)
	if err == nil {
		t.Error("expected error for invalid proxy URL in FetchProjectID")
	}
}

func TestOAuth_ClientCredentials_Format(t *testing.T) {
	if !strings.HasPrefix(DefaultOAuthClientSecret, "GOC"+"SPX-") {
		t.Errorf("expected DefaultOAuthClientSecret to start with 'GOCSPX-', got: %q", DefaultOAuthClientSecret)
	}
	if len(DefaultOAuthClientSecret) != 35 {
		t.Errorf("expected DefaultOAuthClientSecret length 35, got %d", len(DefaultOAuthClientSecret))
	}
	if !strings.HasSuffix(DefaultOAuthClientID, "apps.googleusercontent.com") {
		t.Errorf("expected DefaultOAuthClientID to end with apps.googleusercontent.com, got %s", DefaultOAuthClientID)
	}
}

func TestOAuth_EnvOverrides(t *testing.T) {
	origID := os.Getenv("ANTIGRAVITY_CLIENT_ID")
	origSecret := os.Getenv("ANTIGRAVITY_CLIENT_SECRET")
	defer func() {
		os.Setenv("ANTIGRAVITY_CLIENT_ID", origID)
		os.Setenv("ANTIGRAVITY_CLIENT_SECRET", origSecret)
	}()

	os.Unsetenv("ANTIGRAVITY_CLIENT_ID")
	os.Unsetenv("ANTIGRAVITY_CLIENT_SECRET")
	if GetOAuthClientID() != DefaultOAuthClientID {
		t.Errorf("expected DefaultOAuthClientID when unset")
	}
	if GetOAuthClientSecret() != DefaultOAuthClientSecret {
		t.Errorf("expected DefaultOAuthClientSecret when unset")
	}

	os.Setenv("ANTIGRAVITY_CLIENT_ID", "custom-client-id")
	os.Setenv("ANTIGRAVITY_CLIENT_SECRET", "custom-client-secret")
	if GetOAuthClientID() != "custom-client-id" {
		t.Errorf("expected overridden client ID")
	}
	if GetOAuthClientSecret() != "custom-client-secret" {
		t.Errorf("expected overridden client secret")
	}
}
