package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AdminClient provides an IPC client for communicating with AdminServer.
type AdminClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewAdminClient creates a new AdminClient targeting baseURL.
// If baseURL is empty, it defaults to "http://127.0.0.1:8046".
func NewAdminClient(baseURL string) *AdminClient {
	target := strings.TrimSpace(baseURL)
	if target == "" {
		target = "http://127.0.0.1:8046"
	}

	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "http://" + target
	}
	target = strings.TrimRight(target, "/")

	return &AdminClient{
		baseURL: target,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// BaseURL returns the configured base URL for the admin client.
func (c *AdminClient) BaseURL() string {
	return c.baseURL
}

// SetHTTPClient sets a custom http.Client (e.g. for testing).
func (c *AdminClient) SetHTTPClient(httpClient *http.Client) {
	if httpClient != nil {
		c.httpClient = httpClient
	}
}

// GetStatus retrieves the current service status from the admin server.
func (c *AdminClient) GetStatus() (*StatusResponse, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/status", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build status request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("admin client request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := checkErrorResponse(resp); err != nil {
		return nil, err
	}

	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode status response: %w", err)
	}

	return &status, nil
}

// PinAccount instructs the server to pin traffic to the given account ID or email.
func (c *AdminClient) PinAccount(accountID string) error {
	if accountID == "" {
		return errors.New("account_id cannot be empty")
	}

	body := SelectRequest{
		AccountID: accountID,
		Unpin:     false,
	}

	return c.postJSON("/api/select", body)
}

// Unpin clears pinned account selection, returning the server to round-robin.
func (c *AdminClient) Unpin() error {
	body := SelectRequest{
		Unpin: true,
	}

	return c.postJSON("/api/select", body)
}

// Reload triggers reloading of account configuration from disk.
func (c *AdminClient) Reload() error {
	return c.postJSON("/api/reload", map[string]any{})
}

// RefreshToken triggers token refresh and project ID discovery for the given account.
func (c *AdminClient) RefreshToken(accountID string) error {
	if accountID == "" {
		return errors.New("account_id cannot be empty")
	}

	body := RefreshRequest{
		AccountID: accountID,
	}

	return c.postJSON("/api/refresh", body)
}

func (c *AdminClient) postJSON(path string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal request payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("admin client request failed: %w", err)
	}
	defer resp.Body.Close()

	return checkErrorResponse(resp)
}

func checkErrorResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var respMsg ResponseMessage
	if err := json.Unmarshal(body, &respMsg); err == nil && respMsg.Error != "" {
		return fmt.Errorf("admin server error (%d): %s", resp.StatusCode, respMsg.Error)
	}

	trimmed := strings.TrimSpace(string(body))
	if trimmed != "" {
		return fmt.Errorf("admin server error (%d): %s", resp.StatusCode, trimmed)
	}
	return fmt.Errorf("admin server error with status code %d", resp.StatusCode)
}
