package admin_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gemini-bridge/internal/account"
	"gemini-bridge/internal/admin"
	"gemini-bridge/internal/proxy"
)

type mockLoader struct {
	accounts []*account.CloudAccount
	pinnedID string
	loadErr  error
}

func (m *mockLoader) LoadAccounts(dir string) ([]*account.CloudAccount, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	res := make([]*account.CloudAccount, len(m.accounts))
	for i, a := range m.accounts {
		cp := *a
		res[i] = &cp
	}
	return res, nil
}

func (m *mockLoader) SaveAccountsCache(dir string, accounts []*account.CloudAccount) error {
	return nil
}

func (m *mockLoader) LoadPinnedAccount(dir string) (string, error) {
	return m.pinnedID, nil
}

func (m *mockLoader) SavePinnedAccount(dir string, accountID string) error {
	m.pinnedID = accountID
	return nil
}

type mockStatsProvider struct {
	stats proxy.ServerStats
}

func (m *mockStatsProvider) GetStats() proxy.ServerStats {
	return m.stats
}

func setupTestServer(t *testing.T, loader *mockLoader, stats *mockStatsProvider) (*admin.AdminServer, *account.Pool, *httptest.Server, *admin.AdminClient) {
	t.Helper()

	if loader == nil {
		loader = &mockLoader{
			accounts: []*account.CloudAccount{
				{
					ID:       "acc-1",
					Email:    "acc1@example.com",
					Name:     "Account 1",
					Provider: "google",
					Status:   "active",
					Token: account.CloudToken{
						AccessToken:     "token-1",
						ProjectID:       "proj-1",
						ExpiryTimestamp: time.Now().Add(1 * time.Hour).Unix(),
					},
				},
				{
					ID:       "acc-2",
					Email:    "acc2@example.com",
					Name:     "Account 2",
					Provider: "google",
					Status:   "active",
					Token: account.CloudToken{
						AccessToken:     "token-2",
						ProjectID:       "proj-2",
						ExpiryTimestamp: time.Now().Add(1 * time.Hour).Unix(),
					},
				},
			},
		}
	}

	pool, err := account.NewPool(loader, "/test-dir")
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	if stats == nil {
		stats = &mockStatsProvider{
			stats: proxy.ServerStats{
				TotalRequests:     100,
				SuccessRequests:   90,
				RateLimitRequests: 5,
				ErrorRequests:     5,
				RecentLogs: []proxy.RequestLogEntry{
					{
						Method:       "POST",
						Path:         "/v1/chat/completions",
						Model:        "gpt-4o",
						TargetModel:  "gemini-3-flash",
						Status:       200,
						AccountEmail: "acc1@example.com",
					},
				},
			},
		}
	}

	server := admin.NewAdminServer(pool, stats)
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	client := admin.NewAdminClient(ts.URL)
	return server, pool, ts, client
}

func TestAdmin_GetStatus(t *testing.T) {
	_, _, _, client := setupTestServer(t, nil, nil)

	status, err := client.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}

	if status.Mode != "sticky" {
		t.Errorf("expected Mode 'sticky', got %q", status.Mode)
	}
	if status.PinnedAccountID != "" {
		t.Errorf("expected empty PinnedAccountID, got %q", status.PinnedAccountID)
	}
	if len(status.Accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(status.Accounts))
	}
	if status.Accounts[0].ID != "acc-1" || status.Accounts[1].ID != "acc-2" {
		t.Errorf("unexpected account IDs: %s, %s", status.Accounts[0].ID, status.Accounts[1].ID)
	}
	if status.Stats.TotalRequests != 100 {
		t.Errorf("expected 100 total requests, got %d", status.Stats.TotalRequests)
	}
	if len(status.Stats.RecentLogs) != 1 {
		t.Errorf("expected 1 recent log entry, got %d", len(status.Stats.RecentLogs))
	}
	if status.Uptime == "" {
		t.Error("expected non-empty Uptime")
	}
}

func TestAdmin_PinAndUnpin(t *testing.T) {
	_, pool, _, client := setupTestServer(t, nil, nil)

	// 1. Pin account 1
	err := client.PinAccount("acc-1")
	if err != nil {
		t.Fatalf("PinAccount() error = %v", err)
	}

	status, err := client.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if status.Mode != "pinned" {
		t.Errorf("expected Mode 'pinned', got %q", status.Mode)
	}
	if status.PinnedAccountID != "acc-1" {
		t.Errorf("expected PinnedAccountID 'acc-1', got %q", status.PinnedAccountID)
	}

	isPinned, pinnedID := pool.IsPinned()
	if !isPinned || pinnedID != "acc-1" {
		t.Errorf("pool.IsPinned() = (%v, %q), expected (true, 'acc-1')", isPinned, pinnedID)
	}

	// 2. Pin account 2
	err = client.PinAccount("acc-2")
	if err != nil {
		t.Fatalf("PinAccount('acc-2') error = %v", err)
	}

	status, err = client.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if status.Mode != "pinned" || status.PinnedAccountID != "acc-2" {
		t.Errorf("expected Mode 'pinned' and PinnedAccountID 'acc-2', got (%q, %q)", status.Mode, status.PinnedAccountID)
	}

	// 3. Unpin
	err = client.Unpin()
	if err != nil {
		t.Fatalf("Unpin() error = %v", err)
	}

	status, err = client.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if status.Mode != "sticky" {
		t.Errorf("expected Mode 'sticky', got %q", status.Mode)
	}
	if status.PinnedAccountID != "" {
		t.Errorf("expected empty PinnedAccountID, got %q", status.PinnedAccountID)
	}

	isPinned, pinnedID = pool.IsPinned()
	if isPinned || pinnedID != "" {
		t.Errorf("pool.IsPinned() = (%v, %q), expected (false, '')", isPinned, pinnedID)
	}
}

func TestAdmin_Reload(t *testing.T) {
	loader := &mockLoader{
		accounts: []*account.CloudAccount{
			{
				ID:       "acc-initial",
				Email:    "initial@example.com",
				Status:   "active",
				Provider: "google",
			},
		},
	}

	_, _, _, client := setupTestServer(t, loader, nil)

	status, err := client.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if len(status.Accounts) != 1 || status.Accounts[0].ID != "acc-initial" {
		t.Fatalf("unexpected initial accounts: %+v", status.Accounts)
	}

	// Update loader to simulate new accounts on disk
	loader.accounts = []*account.CloudAccount{
		{
			ID:       "acc-reloaded-1",
			Email:    "reloaded1@example.com",
			Status:   "active",
			Provider: "google",
		},
		{
			ID:       "acc-reloaded-2",
			Email:    "reloaded2@example.com",
			Status:   "active",
			Provider: "google",
		},
	}

	err = client.Reload()
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	status, err = client.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if len(status.Accounts) != 2 {
		t.Fatalf("expected 2 reloaded accounts, got %d", len(status.Accounts))
	}
	if status.Accounts[0].ID != "acc-reloaded-1" || status.Accounts[1].ID != "acc-reloaded-2" {
		t.Errorf("unexpected reloaded accounts: %s, %s", status.Accounts[0].ID, status.Accounts[1].ID)
	}

	// Test Reload error case
	loader.loadErr = errors.New("disk failure reading config")
	err = client.Reload()
	if err == nil {
		t.Fatal("expected error from Reload() when loader fails, got nil")
	}
	if !strings.Contains(err.Error(), "disk failure") {
		t.Errorf("expected error message to contain 'disk failure', got %q", err.Error())
	}
}

func TestAdmin_RefreshToken(t *testing.T) {
	loader := &mockLoader{
		accounts: []*account.CloudAccount{
			{
				ID:       "acc-target",
				Email:    "target@example.com",
				Status:   "active",
				Provider: "google",
				Token: account.CloudToken{
					AccessToken:     "old-token",
					ExpiryTimestamp: time.Now().Unix(),
				},
			},
		},
	}

	_, pool, _, client := setupTestServer(t, loader, nil)

	// Since mock pool doesn't refresh tokens without network unless refresh func is mock,
	// calling RefreshToken on valid account will succeed (RefreshTokenFunc is default or mock).
	// RefreshToken on non-existent account should return error:
	err := client.RefreshToken("non-existent-id")
	if err == nil {
		t.Fatal("expected error for non-existent account, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error to contain 'not found', got %q", err.Error())
	}

	// Calling RefreshToken with empty ID
	err = client.RefreshToken("")
	if err == nil {
		t.Fatal("expected error when refreshing with empty account ID, got nil")
	}

	// Verify existing account succeeds if RefreshTokenFunc works or is mocked
	// In pool, RefreshToken attempts RefreshAccessToken if target found.
	_ = pool
}

func TestAdmin_Endpoints_Aliases(t *testing.T) {
	server, _, ts, _ := setupTestServer(t, nil, nil)

	// Test /status (alias for /api/status)
	resp, err := http.Get(ts.URL + "/status")
	if err != nil {
		t.Fatalf("GET /status error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /status status code = %d, expected 200", resp.StatusCode)
	}

	// Test /select (alias for /api/select)
	reqBody := `{"account_id":"acc-1"}`
	resp, err = http.Post(ts.URL+"/select", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /select error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /select status code = %d, expected 200", resp.StatusCode)
	}

	// Test /reload (alias for /api/reload)
	resp, err = http.Post(ts.URL+"/reload", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /reload error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /reload status code = %d, expected 200", resp.StatusCode)
	}

	// Test Method Not Allowed
	resp, err = http.Post(ts.URL+"/api/status", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/status error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/status expected 405 Method Not Allowed, got %d", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/api/reload")
	if err != nil {
		t.Fatalf("GET /api/reload error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/reload expected 405 Method Not Allowed, got %d", resp.StatusCode)
	}

	_ = server
}

func TestAdmin_InvalidRequests(t *testing.T) {
	_, _, ts, _ := setupTestServer(t, nil, nil)

	// POST /api/select with invalid JSON
	resp, err := http.Post(ts.URL+"/api/select", "application/json", strings.NewReader("invalid-json"))
	if err != nil {
		t.Fatalf("POST /api/select error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /api/select invalid JSON expected 400 Bad Request, got %d", resp.StatusCode)
	}

	// POST /api/refresh with invalid JSON
	resp, err = http.Post(ts.URL+"/api/refresh", "application/json", strings.NewReader("not-a-json"))
	if err != nil {
		t.Fatalf("POST /api/refresh error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /api/refresh invalid JSON expected 400 Bad Request, got %d", resp.StatusCode)
	}

	// POST /api/refresh with empty account_id
	resp, err = http.Post(ts.URL+"/api/refresh", "application/json", strings.NewReader(`{"account_id":""}`))
	if err != nil {
		t.Fatalf("POST /api/refresh empty ID error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /api/refresh empty account_id expected 400 Bad Request, got %d", resp.StatusCode)
	}
}

func TestAdmin_BaseURLFormatting(t *testing.T) {
	c1 := admin.NewAdminClient("127.0.0.1:8046")
	if !strings.HasPrefix(c1.BaseURL(), "http://") {
		t.Errorf("expected baseURL to start with http://, got %q", c1.BaseURL())
	}

	c2 := admin.NewAdminClient("http://127.0.0.1:8046/")
	if strings.HasSuffix(c2.BaseURL(), "/") {
		t.Errorf("expected baseURL not to end with trailing slash, got %q", c2.BaseURL())
	}

	c3 := admin.NewAdminClient("")
	if c3.BaseURL() != "http://127.0.0.1:8046" {
		t.Errorf("expected default baseURL 'http://127.0.0.1:8046', got %q", c3.BaseURL())
	}
}

func TestAdmin_ServerStartAndShutdown(t *testing.T) {
	server := admin.NewAdminServer(nil, nil)

	// Pick an open random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(addr)
	}()

	// Wait briefly for server to bind
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("unexpected Start error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Start to exit after Shutdown")
	}
}

func TestAdmin_Select_InvalidPayload(t *testing.T) {
	_, _, ts, client := setupTestServer(t, nil, nil)
	defer ts.Close()

	// Direct POST to /api/select with empty object {}
	resp, err := http.Post(ts.URL+"/api/select", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST /api/select failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 Bad Request for empty select payload, got %d", resp.StatusCode)
	}

	// Also verify client with empty accountID returns error
	if err := client.PinAccount(""); err == nil {
		t.Error("expected error when PinAccount is called with empty ID")
	}
}
