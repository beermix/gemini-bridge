package account

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

func decodeBase64(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

const (
	// DefaultTokenURL is Google's OAuth2 token endpoint.
	DefaultTokenURL = "https://oauth2.googleapis.com/token"
)

var (
	// DefaultOAuthClientID is the Google OAuth client ID used by Antigravity.
	DefaultOAuthClientID = decodeBase64("MTA3MTAwNjA2MDU5MS10bWhzc2luMmgyMWxjcmUyMzV2dG9sb2poNGc0MDNlcC5hcHBzLmdvb2dsZXVzZXJjb250ZW50LmNvbQ==")
	// DefaultOAuthClientSecret is the Google OAuth client secret used by Antigravity.
	DefaultOAuthClientSecret = decodeBase64("R0NDU1BYLUs1OEZXUjQ4NkxkTEoxbUxCOHNYQzR6NnFEQWY=")
)

var (
	tokenURLMu sync.RWMutex
	tokenURL   = DefaultTokenURL
)

func getTokenURL() string {
	tokenURLMu.RLock()
	defer tokenURLMu.RUnlock()
	return tokenURL
}

// SetTokenURLForTest overrides the OAuth token URL for unit tests and returns a restore function.
func SetTokenURLForTest(u string) func() {
	tokenURLMu.Lock()
	old := tokenURL
	tokenURL = u
	tokenURLMu.Unlock()
	return func() {
		tokenURLMu.Lock()
		tokenURL = old
		tokenURLMu.Unlock()
	}
}

// resolveProxy returns explicitProxy if non-empty, otherwise account.ProxyURL if set, or empty string.
func resolveProxy(account *CloudAccount, explicitProxy string) string {
	if explicitProxy != "" {
		return explicitProxy
	}
	if account != nil && account.ProxyURL != "" {
		return account.ProxyURL
	}
	return ""
}

// createHTTPClient creates an http.Client supporting optional HTTP/HTTPS/SOCKS5 proxy.
func createHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxyURL != "" {
		cleanURL := proxyURL
		if !strings.Contains(cleanURL, "://") {
			cleanURL = "http://" + cleanURL
		}
		u, err := url.Parse(cleanURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}, nil
}

// RefreshAccessToken exchanges an account's refresh_token for a fresh access_token via Google OAuth2.
// If successful, it updates account.Token and returns a copy of the updated token.
func RefreshAccessToken(account *CloudAccount, proxyURL string) (*CloudToken, error) {
	if account == nil {
		return nil, fmt.Errorf("account is nil")
	}
	if account.Token.RefreshToken == "" {
		return nil, fmt.Errorf("account %q has empty refresh token", account.Email)
	}

	targetProxy := resolveProxy(account, proxyURL)
	client, err := createHTTPClient(targetProxy, 30*time.Second)
	if err != nil {
		return nil, err
	}

	form := url.Values{
		"client_id":     {DefaultOAuthClientID},
		"client_secret": {DefaultOAuthClientSecret},
		"refresh_token": {account.Token.RefreshToken},
		"grant_type":    {"refresh_token"},
	}

	req, err := http.NewRequest(http.MethodPost, getTokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read token refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth token refresh failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}

	if err := json.Unmarshal(bodyBytes, &data); err != nil {
		return nil, fmt.Errorf("failed to parse token refresh response JSON: %w", err)
	}

	if data.AccessToken == "" {
		return nil, fmt.Errorf("oauth token refresh response missing access_token")
	}

	now := time.Now().Unix()
	account.Token.AccessToken = data.AccessToken
	if data.RefreshToken != "" {
		account.Token.RefreshToken = data.RefreshToken
	}
	if data.TokenType != "" {
		account.Token.TokenType = data.TokenType
	} else if account.Token.TokenType == "" {
		account.Token.TokenType = "Bearer"
	}
	if data.ExpiresIn > 0 {
		account.Token.ExpiresIn = data.ExpiresIn
		account.Token.ExpiryTimestamp = now + data.ExpiresIn
	}

	tokenCopy := account.Token
	return &tokenCopy, nil
}
