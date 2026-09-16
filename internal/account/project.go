package account

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync"
	"time"
)

const (
	// DefaultLoadCodeAssistURL is the internal Cloud Code API endpoint used by Antigravity to discover project context.
	DefaultLoadCodeAssistURL = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
)

var (
	loadCodeAssistURLMu sync.RWMutex
	loadCodeAssistURL   = DefaultLoadCodeAssistURL
)

func getLoadCodeAssistURL() string {
	loadCodeAssistURLMu.RLock()
	defer loadCodeAssistURLMu.RUnlock()
	return loadCodeAssistURL
}

// SetLoadCodeAssistURLForTest overrides the loadCodeAssist URL for unit tests and returns a restore function.
func SetLoadCodeAssistURLForTest(u string) func() {
	loadCodeAssistURLMu.Lock()
	old := loadCodeAssistURL
	loadCodeAssistURL = u
	loadCodeAssistURLMu.Unlock()
	return func() {
		loadCodeAssistURLMu.Lock()
		loadCodeAssistURL = old
		loadCodeAssistURLMu.Unlock()
	}
}

func defaultUserAgent() string {
	return fmt.Sprintf("antigravity/%s/%s", runtime.GOOS, runtime.GOARCH)
}

// FetchProjectID discovers the Google Cloud project ID (cloudaicompanionProject) for the given account
// using the loadCodeAssist internal API.
// If successful, it updates account.Token.ProjectID and returns the project ID string.
func FetchProjectID(account *CloudAccount, proxyURL string) (string, error) {
	if account == nil {
		return "", fmt.Errorf("account is nil")
	}
	if account.Token.AccessToken == "" {
		return "", fmt.Errorf("account %q has empty access token", account.Email)
	}

	targetProxy := resolveProxy(account, proxyURL)
	client, err := createHTTPClient(targetProxy, 30*time.Second)
	if err != nil {
		return "", err
	}

	reqBody := map[string]interface{}{
		"metadata": map[string]string{
			"ideType": "ANTIGRAVITY",
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, getLoadCodeAssistURL(), bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to create loadCodeAssist request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+account.Token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", defaultUserAgent())

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("loadCodeAssist request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read loadCodeAssist response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("loadCodeAssist failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var data map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &data); err != nil {
		return "", fmt.Errorf("failed to parse loadCodeAssist response JSON: %w", err)
	}

	var projectID string
	if p, ok := data["cloudaicompanionProject"].(string); ok && p != "" {
		projectID = p
	} else if p, ok := data["projectId"].(string); ok && p != "" {
		projectID = p
	} else if p, ok := data["project_id"].(string); ok && p != "" {
		projectID = p
	}

	if projectID == "" {
		return "", fmt.Errorf("no project ID found in loadCodeAssist response")
	}

	account.Token.ProjectID = projectID
	return projectID, nil
}
