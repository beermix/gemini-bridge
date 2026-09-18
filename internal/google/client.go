package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gemini-bridge/internal/account"
)

const (
	// DefaultPrimaryBaseURL is the primary Google Cloud Code internal API base URL.
	DefaultPrimaryBaseURL = "https://cloudcode-pa.googleapis.com/v1internal"
	// DefaultFallbackBaseURL is the secondary Google Cloud Code internal API base URL.
	DefaultFallbackBaseURL = "https://daily-cloudcode-pa.googleapis.com/v1internal"
	// DefaultUserAgent is the default User-Agent sent in upstream requests.
	DefaultUserAgent = "antigravity/1.11.3 Linux/amd64"
)

// UpstreamError represents an HTTP error response returned by Google upstream.
type UpstreamError struct {
	StatusCode int
	Body       string
}

// Error formats the UpstreamError with status code and body.
func (e *UpstreamError) Error() string {
	return fmt.Sprintf("upstream error (status %d): %s", e.StatusCode, e.Body)
}

// Client interacts with the Google Cloud Code internal Gemini API endpoints.
type Client struct {
	mu              sync.RWMutex
	primaryBaseURL  string
	fallbackBaseURL string
	activeEndpoint  string
	timeout         time.Duration

	transportMu sync.RWMutex
	transports  map[string]*http.Transport
}

// ShortEndpoint returns a simplified, human-friendly name for an endpoint URL.
func ShortEndpoint(rawURL string) string {
	if rawURL == "" {
		return "cloudcode-pa"
	}
	if strings.Contains(rawURL, "daily-cloudcode-pa") {
		return "daily-cloudcode-pa"
	}
	if strings.Contains(rawURL, "cloudcode-pa") {
		return "cloudcode-pa"
	}
	u, err := url.Parse(rawURL)
	if err == nil && u.Host != "" {
		host := u.Host
		if strings.HasSuffix(host, ".googleapis.com") {
			return strings.TrimSuffix(host, ".googleapis.com")
		}
		return host
	}
	return rawURL
}

// NewClient creates a new Client with default base URLs and the given timeout.
func NewClient(timeout time.Duration) *Client {
	return &Client{
		primaryBaseURL:  DefaultPrimaryBaseURL,
		fallbackBaseURL: DefaultFallbackBaseURL,
		activeEndpoint:  ShortEndpoint(DefaultPrimaryBaseURL),
		timeout:         timeout,
		transports:      make(map[string]*http.Transport),
	}
}

// ActiveEndpoint returns the currently active / last used upstream endpoint name.
func (c *Client) ActiveEndpoint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.activeEndpoint == "" {
		return ShortEndpoint(c.primaryBaseURL)
	}
	return c.activeEndpoint
}

func (c *Client) setActiveEndpoint(endpoint string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activeEndpoint = ShortEndpoint(endpoint)
}

// SetBaseURLs overrides the primary and fallback base URLs (useful for unit tests).
func (c *Client) SetBaseURLs(primary, fallback string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.primaryBaseURL = primary
	c.fallbackBaseURL = fallback
	c.activeEndpoint = ShortEndpoint(primary)
}

// PrimaryBaseURL returns the configured primary base URL.
func (c *Client) PrimaryBaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.primaryBaseURL
}

// FallbackBaseURL returns the configured fallback base URL.
func (c *Client) FallbackBaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fallbackBaseURL
}

func (c *Client) getBaseURLs() (string, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.primaryBaseURL, c.fallbackBaseURL
}

// CloseIdleConnections closes all idle HTTP connections across cached transports.
func (c *Client) CloseIdleConnections() {
	c.transportMu.Lock()
	defer c.transportMu.Unlock()
	for _, tr := range c.transports {
		tr.CloseIdleConnections()
	}
}

func (c *Client) getHTTPClient(acc *account.CloudAccount, isStreaming bool) (*http.Client, error) {
	proxyURL := ""
	if acc != nil {
		proxyURL = acc.ProxyURL
	}

	c.transportMu.RLock()
	tr, ok := c.transports[proxyURL]
	c.transportMu.RUnlock()

	if !ok {
		c.transportMu.Lock()
		tr, ok = c.transports[proxyURL]
		if !ok {
			var baseTr *http.Transport
			if defaultTr, okDef := http.DefaultTransport.(*http.Transport); okDef {
				baseTr = defaultTr.Clone()
			} else {
				baseTr = &http.Transport{
					Proxy: http.ProxyFromEnvironment,
				}
			}

			if proxyURL != "" {
				cleanURL := proxyURL
				if !strings.Contains(cleanURL, "://") {
					cleanURL = "http://" + cleanURL
				}
				u, err := url.Parse(cleanURL)
				if err != nil {
					c.transportMu.Unlock()
					return nil, fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
				}
				baseTr.Proxy = http.ProxyURL(u)
			}

			if c.timeout > 0 {
				baseTr.ResponseHeaderTimeout = c.timeout
			}

			c.transports[proxyURL] = baseTr
			tr = baseTr
		}
		c.transportMu.Unlock()
	}

	client := &http.Client{
		Transport: tr,
	}
	if !isStreaming && c.timeout > 0 {
		client.Timeout = c.timeout
	}
	return client, nil
}

func buildEndpoint(baseURL, action string) string {
	actionPath := action
	query := ""
	if idx := strings.Index(action, "?"); idx != -1 {
		actionPath = action[:idx]
		query = action[idx+1:]
	}

	base := strings.TrimRight(baseURL, "/:")
	u, err := url.Parse(base)
	if err != nil {
		if query != "" {
			return base + ":" + actionPath + "?" + query
		}
		return base + ":" + actionPath
	}

	if u.Path == "" {
		u.Path = "/:" + actionPath
	} else {
		u.Path = strings.TrimRight(u.Path, "/") + ":" + actionPath
	}

	if query != "" {
		if u.RawQuery != "" {
			u.RawQuery += "&" + query
		} else {
			u.RawQuery = query
		}
	}
	return u.String()
}

func (c *Client) newRequest(ctx context.Context, endpoint string, payload []byte, acc *account.CloudAccount, projectID string, customUA string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+acc.Token.AccessToken)

	ua := DefaultUserAgent
	if customUA != "" {
		ua = customUA
	}
	req.Header.Set("User-Agent", ua)

	targetProject := acc.Token.ProjectID
	if targetProject == "" {
		targetProject = projectID
	}
	if targetProject != "" && targetProject != "aicode-consumers" {
		req.Header.Set("x-goog-user-project", targetProject)
	}

	return req, nil
}

func shouldFallback(err error, resp *http.Response, ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	if err != nil {
		return true
	}
	if resp != nil && (resp.StatusCode >= 500 && resp.StatusCode <= 599 || resp.StatusCode == http.StatusTooManyRequests) {
		return true
	}
	return false
}

// StreamGenerateContent sends a streaming generation request to Google Cloud Code upstream.
// On success, it returns the raw SSE response body as an io.ReadCloser.
// On non-2xx HTTP status, it reads the error response and returns an *UpstreamError.
// On 5xx or network connection error from the primary endpoint, it attempts the fallback endpoint once.
func (c *Client) StreamGenerateContent(ctx context.Context, acc *account.CloudAccount, body *GeminiInternalRequest) (io.ReadCloser, error) {
	rc, _, err := c.StreamGenerateContentWithEndpoint(ctx, acc, body)
	return rc, err
}

// StreamGenerateContentWithEndpoint sends a streaming generation request and returns the endpoint used.
func (c *Client) StreamGenerateContentWithEndpoint(ctx context.Context, acc *account.CloudAccount, body *GeminiInternalRequest) (io.ReadCloser, string, error) {
	if acc == nil {
		return nil, "", fmt.Errorf("account is nil")
	}
	if acc.Token.AccessToken == "" {
		return nil, "", fmt.Errorf("account %q has empty access token", acc.Email)
	}
	if body == nil {
		return nil, "", fmt.Errorf("request body is nil")
	}

	httpClient, err := c.getHTTPClient(acc, true)
	if err != nil {
		return nil, "", err
	}

	reqBody := *body
	if reqBody.Project == "" && acc.Token.ProjectID != "" {
		reqBody.Project = acc.Token.ProjectID
	}
	payload, err := json.Marshal(&reqBody)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	primaryURL, fallbackURL := c.getBaseURLs()
	primaryEndpoint := buildEndpoint(primaryURL, "streamGenerateContent?alt=sse")

	req, err := c.newRequest(ctx, primaryEndpoint, payload, acc, reqBody.Project, body.UserAgent)
	if err != nil {
		return nil, ShortEndpoint(primaryURL), err
	}

	usedURL := primaryURL
	resp, doErr := httpClient.Do(req)
	if shouldFallback(doErr, resp, ctx) && fallbackURL != "" && fallbackURL != primaryURL {
		if resp != nil {
			resp.Body.Close()
		}
		fallbackEndpoint := buildEndpoint(fallbackURL, "streamGenerateContent?alt=sse")
		fallbackReq, fErr := c.newRequest(ctx, fallbackEndpoint, payload, acc, reqBody.Project, body.UserAgent)
		if fErr == nil {
			usedURL = fallbackURL
			resp, doErr = httpClient.Do(fallbackReq)
		}
	}

	usedEP := ShortEndpoint(usedURL)
	if doErr != nil {
		return nil, usedEP, doErr
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, usedEP, &UpstreamError{
			StatusCode: resp.StatusCode,
			Body:       string(bodyBytes),
		}
	}

	c.setActiveEndpoint(usedURL)
	return resp.Body, usedEP, nil
}

// GenerateContent sends a non-streaming generation request to Google Cloud Code upstream.
// It parses the response (handling both `{ "response": { ... } }` wrapped envelope and bare envelope)
// into *GeminiResponse.
// On non-2xx HTTP status, it reads the error response and returns an *UpstreamError.
// On 5xx or network connection error from the primary endpoint, it attempts the fallback endpoint once.
func (c *Client) GenerateContent(ctx context.Context, acc *account.CloudAccount, body *GeminiInternalRequest) (*GeminiResponse, error) {
	resp, _, err := c.GenerateContentWithEndpoint(ctx, acc, body)
	return resp, err
}

// GenerateContentWithEndpoint sends a non-streaming generation request and returns the endpoint used.
func (c *Client) GenerateContentWithEndpoint(ctx context.Context, acc *account.CloudAccount, body *GeminiInternalRequest) (*GeminiResponse, string, error) {
	if acc == nil {
		return nil, "", fmt.Errorf("account is nil")
	}
	if acc.Token.AccessToken == "" {
		return nil, "", fmt.Errorf("account %q has empty access token", acc.Email)
	}
	if body == nil {
		return nil, "", fmt.Errorf("request body is nil")
	}

	httpClient, err := c.getHTTPClient(acc, false)
	if err != nil {
		return nil, "", err
	}

	reqBody := *body
	if reqBody.Project == "" && acc.Token.ProjectID != "" {
		reqBody.Project = acc.Token.ProjectID
	}
	payload, err := json.Marshal(&reqBody)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	primaryURL, fallbackURL := c.getBaseURLs()
	primaryEndpoint := buildEndpoint(primaryURL, "generateContent")

	req, err := c.newRequest(ctx, primaryEndpoint, payload, acc, reqBody.Project, body.UserAgent)
	if err != nil {
		return nil, ShortEndpoint(primaryURL), err
	}

	usedURL := primaryURL
	resp, doErr := httpClient.Do(req)
	if shouldFallback(doErr, resp, ctx) && fallbackURL != "" && fallbackURL != primaryURL {
		if resp != nil {
			resp.Body.Close()
		}
		fallbackEndpoint := buildEndpoint(fallbackURL, "generateContent")
		fallbackReq, fErr := c.newRequest(ctx, fallbackEndpoint, payload, acc, reqBody.Project, body.UserAgent)
		if fErr == nil {
			usedURL = fallbackURL
			resp, doErr = httpClient.Do(fallbackReq)
		}
	}

	usedEP := ShortEndpoint(usedURL)
	if doErr != nil {
		return nil, usedEP, doErr
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, usedEP, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, usedEP, &UpstreamError{
			StatusCode: resp.StatusCode,
			Body:       string(bodyBytes),
		}
	}

	c.setActiveEndpoint(usedURL)
	geminiResp, err := ParseGeminiResponse(bodyBytes)
	return geminiResp, usedEP, err
}
