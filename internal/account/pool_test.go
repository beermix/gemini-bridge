package account

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type mockLoader struct {
	mu        sync.Mutex
	accounts  []*CloudAccount
	pinnedID  string
	loadErr   error
	saveErr   error
	saveCalls int
}

func (m *mockLoader) LoadAccounts(dir string) ([]*CloudAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	out := make([]*CloudAccount, len(m.accounts))
	for i, a := range m.accounts {
		cp := *a
		cp.Token = a.Token
		out[i] = &cp
	}
	return out, nil
}

func (m *mockLoader) SaveAccountsCache(dir string, accounts []*CloudAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveCalls++
	if m.saveErr != nil {
		return m.saveErr
	}
	m.accounts = accounts
	return nil
}

func (m *mockLoader) LoadPinnedAccount(dir string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pinnedID, nil
}

func (m *mockLoader) SavePinnedAccount(dir string, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pinnedID = accountID
	return nil
}

func makeTestAccount(id, email string, expiryOffsetSec int64, projectID string) *CloudAccount {
	now := time.Now().Unix()
	return &CloudAccount{
		ID:       id,
		Provider: "google",
		Email:    email,
		Name:     "Test " + id,
		Status:   "active",
		Token: CloudToken{
			AccessToken:     "token-" + id,
			RefreshToken:    "refresh-" + id,
			TokenType:       "Bearer",
			ProjectID:       projectID,
			ExpiresIn:       3600,
			ExpiryTimestamp: now + expiryOffsetSec,
		},
	}
}

func TestPool_NewPool_Success(t *testing.T) {
	accs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 3600, "proj-1"),
		makeTestAccount("acc-2", "a2@test.com", 3600, "proj-2"),
	}
	loader := &mockLoader{accounts: accs}

	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pool == nil {
		t.Fatalf("expected non-nil pool")
	}

	accounts := pool.GetAccounts()
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accounts))
	}
}

func TestPool_NewPool_Error(t *testing.T) {
	loader := &mockLoader{loadErr: fmt.Errorf("disk failure")}
	_, err := NewPool(loader, "/test/dir")
	if err == nil {
		t.Fatalf("expected error from failed loader")
	}

	_, err = NewPool(nil, "/test/dir")
	if err == nil {
		t.Fatalf("expected error from nil loader")
	}
}

func TestPool_RoundRobin_Leasing(t *testing.T) {
	accs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 3600, "proj-1"),
		makeTestAccount("acc-2", "a2@test.com", 3600, "proj-2"),
		makeTestAccount("acc-3", "a3@test.com", 3600, "proj-3"),
	}
	loader := &mockLoader{accounts: accs}

	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1st lease -> acc-1
	a1, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected lease error: %v", err)
	}
	if a1.ID != "acc-1" {
		t.Errorf("expected acc-1, got %s", a1.ID)
	}

	// 2nd lease -> acc-2
	a2, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected lease error: %v", err)
	}
	if a2.ID != "acc-2" {
		t.Errorf("expected acc-2, got %s", a2.ID)
	}

	// 3rd lease -> acc-3
	a3, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected lease error: %v", err)
	}
	if a3.ID != "acc-3" {
		t.Errorf("expected acc-3, got %s", a3.ID)
	}

	// 4th lease -> wraps around to acc-1
	a4, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected lease error: %v", err)
	}
	if a4.ID != "acc-1" {
		t.Errorf("expected acc-1, got %s", a4.ID)
	}
}

func TestPool_Pinning(t *testing.T) {
	accs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 3600, "proj-1"),
		makeTestAccount("acc-2", "a2@test.com", 3600, "proj-2"),
		makeTestAccount("acc-3", "a3@test.com", 3600, "proj-3"),
	}
	loader := &mockLoader{accounts: accs}

	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	isPinned, pinnedID := pool.IsPinned()
	if isPinned || pinnedID != "" {
		t.Errorf("expected unpinned initially, got %v, %s", isPinned, pinnedID)
	}

	// Pin acc-2
	pool.PinAccount("acc-2")
	isPinned, pinnedID = pool.IsPinned()
	if !isPinned || pinnedID != "acc-2" {
		t.Errorf("expected pinned acc-2, got %v, %s", isPinned, pinnedID)
	}

	// Next 3 leases must all return acc-2
	for i := 0; i < 3; i++ {
		acc, err := pool.LeaseAccount("gemini-2.5-pro")
		if err != nil {
			t.Fatalf("lease %d failed: %v", i, err)
		}
		if acc.ID != "acc-2" {
			t.Errorf("lease %d: expected acc-2, got %s", i, acc.ID)
		}
	}

	// Pin by email
	pool.PinAccount("a3@test.com")
	acc, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("lease after pin by email failed: %v", err)
	}
	if acc.ID != "acc-3" {
		t.Errorf("expected acc-3, got %s", acc.ID)
	}

	// Unpin -> back to Round-Robin
	pool.Unpin()
	isPinned, pinnedID = pool.IsPinned()
	if isPinned || pinnedID != "" {
		t.Errorf("expected unpinned after Unpin(), got %v, %s", isPinned, pinnedID)
	}

	// Pin unknown account
	pool.PinAccount("unknown-acc")
	_, err = pool.LeaseAccount("gemini-2.5-pro")
	if err == nil {
		t.Fatalf("expected error when leasing unknown pinned account")
	}

	// Pin account and put it on cooldown
	pool.PinAccount("acc-1")
	pool.MarkCooldown("acc-1", 30*time.Second)
	_, err = pool.LeaseAccount("gemini-2.5-pro")
	if err == nil {
		t.Fatalf("expected error when pinned account is in cooldown")
	}
}

func TestPool_Cooldown_And_Failover(t *testing.T) {
	accs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 3600, "proj-1"),
		makeTestAccount("acc-2", "a2@test.com", 3600, "proj-2"),
		makeTestAccount("acc-3", "a3@test.com", 3600, "proj-3"),
	}
	loader := &mockLoader{accounts: accs}

	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Put acc-1 on cooldown
	pool.MarkCooldown("acc-1", 1*time.Minute)

	// In Round-Robin, acc-1 should be skipped
	a, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected lease error: %v", err)
	}
	if a.ID != "acc-2" {
		t.Errorf("expected acc-2 (acc-1 skipped), got %s", a.ID)
	}

	b, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected lease error: %v", err)
	}
	if b.ID != "acc-3" {
		t.Errorf("expected acc-3, got %s", b.ID)
	}

	// Next lease wraps around, acc-1 still on cooldown -> returns acc-2
	c, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected lease error: %v", err)
	}
	if c.ID != "acc-2" {
		t.Errorf("expected acc-2, got %s", c.ID)
	}

	// Put all accounts on cooldown
	pool.MarkCooldown("acc-2", 1*time.Minute)
	pool.MarkCooldown("acc-3", 1*time.Minute)

	// All accounts in cooldown -> returns error
	_, err = pool.LeaseAccount("gemini-2.5-pro")
	if err == nil {
		t.Fatalf("expected error when all accounts are in cooldown")
	}
	expectedSub := "no available accounts (all in cooldown or rate limited)"
	if err.Error() != expectedSub {
		t.Errorf("expected error %q, got %q", expectedSub, err.Error())
	}

	// Clear cooldown for acc-1
	pool.MarkCooldown("acc-1", 0)
	d, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected error after clearing cooldown: %v", err)
	}
	if d.ID != "acc-1" {
		t.Errorf("expected acc-1 after clearing cooldown, got %s", d.ID)
	}
}

func TestPool_TokenExpiry_AutoRefresh(t *testing.T) {
	// acc-1 has expired token (offset -100s)
	// acc-2 has valid token (offset +3600s)
	accs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", -100, "proj-1"),
		makeTestAccount("acc-2", "a2@test.com", 3600, "proj-2"),
	}
	loader := &mockLoader{accounts: accs}

	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	refreshedAccs := make(map[string]int)
	pool.refreshTokenFunc = func(account *CloudAccount, proxyURL string) (*CloudToken, error) {
		refreshedAccs[account.ID]++
		now := time.Now().Unix()
		return &CloudToken{
			AccessToken:     "new-refreshed-token-" + account.ID,
			RefreshToken:    account.Token.RefreshToken,
			TokenType:       "Bearer",
			ProjectID:       account.Token.ProjectID,
			ExpiresIn:       3600,
			ExpiryTimestamp: now + 3600,
		}, nil
	}

	// Lease acc-1 -> should trigger refresh and save cache
	acc, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected lease error: %v", err)
	}
	if acc.ID != "acc-1" {
		t.Fatalf("expected acc-1, got %s", acc.ID)
	}
	if refreshedAccs["acc-1"] != 1 {
		t.Errorf("expected acc-1 to be refreshed once, got %d", refreshedAccs["acc-1"])
	}
	if acc.Token.AccessToken != "new-refreshed-token-acc-1" {
		t.Errorf("expected updated access token, got %s", acc.Token.AccessToken)
	}
	if loader.saveCalls < 1 {
		t.Errorf("expected SaveAccountsCache to be called, got %d calls", loader.saveCalls)
	}

	// Lease acc-2 -> valid token (> 300s), should NOT trigger refresh
	acc2, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected lease error: %v", err)
	}
	if acc2.ID != "acc-2" {
		t.Fatalf("expected acc-2, got %s", acc2.ID)
	}
	if refreshedAccs["acc-2"] != 0 {
		t.Errorf("expected acc-2 not to be refreshed, got %d", refreshedAccs["acc-2"])
	}
}

func TestPool_TokenExpiry_BufferWithin5Minutes(t *testing.T) {
	// Token expires in 200s (< 300s buffer) -> should trigger refresh
	accs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 200, "proj-1"),
	}
	loader := &mockLoader{accounts: accs}
	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	refreshCalled := false
	pool.refreshTokenFunc = func(account *CloudAccount, proxyURL string) (*CloudToken, error) {
		refreshCalled = true
		now := time.Now().Unix()
		return &CloudToken{
			AccessToken:     "refreshed-token",
			RefreshToken:    account.Token.RefreshToken,
			TokenType:       "Bearer",
			ProjectID:       account.Token.ProjectID,
			ExpiresIn:       3600,
			ExpiryTimestamp: now + 3600,
		}, nil
	}

	acc, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("lease error: %v", err)
	}
	if !refreshCalled {
		t.Errorf("expected refresh to be called for token expiring within 5 minutes")
	}
	if acc.Token.AccessToken != "refreshed-token" {
		t.Errorf("expected updated access token")
	}
}

func TestPool_FetchProjectID_AutoDiscovery(t *testing.T) {
	// Account missing project_id
	accs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 3600, ""),
	}
	loader := &mockLoader{accounts: accs}
	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	projectFetched := false
	pool.fetchProjectFunc = func(account *CloudAccount, proxyURL string) (string, error) {
		projectFetched = true
		return "discovered-project-id", nil
	}

	acc, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("lease error: %v", err)
	}
	if !projectFetched {
		t.Errorf("expected FetchProjectID to be called")
	}
	if acc.Token.ProjectID != "discovered-project-id" {
		t.Errorf("expected project ID 'discovered-project-id', got %q", acc.Token.ProjectID)
	}
	if loader.saveCalls < 1 {
		t.Errorf("expected loader cache save after project discovery")
	}
}

func TestPool_Reload(t *testing.T) {
	initialAccs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 3600, "proj-1"),
		makeTestAccount("acc-2", "a2@test.com", 3600, "proj-2"),
	}
	loader := &mockLoader{accounts: initialAccs}
	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Pin acc-2 and mark acc-1 on cooldown
	pool.PinAccount("acc-2")
	pool.MarkCooldown("acc-1", 10*time.Minute)

	// Prepare updated accounts on loader (acc-2 still present, acc-3 added, acc-1 kept)
	newAccs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 3600, "proj-1"),
		makeTestAccount("acc-2", "a2@test.com", 3600, "proj-2"),
		makeTestAccount("acc-3", "a3@test.com", 3600, "proj-3"),
	}
	loader.accounts = newAccs

	if err := pool.Reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	// Pinned account should be preserved
	isPinned, pinnedID := pool.IsPinned()
	if !isPinned || pinnedID != "acc-2" {
		t.Errorf("expected pinned acc-2 preserved after reload, got %v, %s", isPinned, pinnedID)
	}

	// Cooldown for acc-1 should be preserved
	all := pool.GetAccounts()
	if len(all) != 3 {
		t.Fatalf("expected 3 accounts after reload, got %d", len(all))
	}
	var acc1 *CloudAccount
	for _, a := range all {
		if a.ID == "acc-1" {
			acc1 = a
			break
		}
	}
	if acc1 == nil || !acc1.IsCooldown() {
		t.Errorf("expected acc-1 cooldown to be preserved across reload")
	}

	// Now update loader removing acc-2 -> reload should unpin
	loader.accounts = []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 3600, "proj-1"),
		makeTestAccount("acc-3", "a3@test.com", 3600, "proj-3"),
	}
	if err := pool.Reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	isPinned, _ = pool.IsPinned()
	if isPinned {
		t.Errorf("expected unpinned because acc-2 was removed")
	}
}

func TestPool_ManualRefreshToken(t *testing.T) {
	accs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 3600, "proj-1"),
	}
	loader := &mockLoader{accounts: accs}
	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pool.refreshTokenFunc = func(account *CloudAccount, proxyURL string) (*CloudToken, error) {
		now := time.Now().Unix()
		return &CloudToken{
			AccessToken:     "manually-refreshed",
			RefreshToken:    account.Token.RefreshToken,
			TokenType:       "Bearer",
			ProjectID:       account.Token.ProjectID,
			ExpiresIn:       3600,
			ExpiryTimestamp: now + 3600,
		}, nil
	}

	if err := pool.RefreshToken("acc-1"); err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}

	all := pool.GetAccounts()
	if all[0].Token.AccessToken != "manually-refreshed" {
		t.Errorf("expected 'manually-refreshed', got %s", all[0].Token.AccessToken)
	}

	if err := pool.RefreshToken("non-existent"); err == nil {
		t.Fatalf("expected error refreshing non-existent account")
	}
}

func TestPool_Concurrent_Access(t *testing.T) {
	accs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 3600, "proj-1"),
		makeTestAccount("acc-2", "a2@test.com", 3600, "proj-2"),
		makeTestAccount("acc-3", "a3@test.com", 3600, "proj-3"),
		makeTestAccount("acc-4", "a4@test.com", 3600, "proj-4"),
	}
	loader := &mockLoader{accounts: accs}
	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var wg sync.WaitGroup
	workers := 20
	iterations := 50

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				switch (workerID + j) % 5 {
				case 0:
					_, _ = pool.LeaseAccount("gemini-2.5-pro")
				case 1:
					_ = pool.GetAccounts()
				case 2:
					pool.MarkCooldown(fmt.Sprintf("acc-%d", (j%4)+1), 10*time.Millisecond)
				case 3:
					if j%2 == 0 {
						pool.PinAccount("acc-2")
					} else {
						pool.Unpin()
					}
				case 4:
					_ = pool.GetActiveAccount()
					_, _ = pool.IsPinned()
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestPool_RealFileLoader_Integration(t *testing.T) {
	tempDir := t.TempDir()
	loader := NewLoader()

	// 1. Write an export file to tempDir
	exportJSON := `{
		"version": "1.0",
		"exportedAt": 1700000000,
		"accounts": [
			{
				"id": "real-acc-1",
				"provider": "google",
				"email": "user1@gmail.com",
				"status": "active",
				"token": {
					"access_token": "ya29.test1",
					"refresh_token": "1//test1",
					"project_id": "test-project-1",
					"expiry_timestamp": 9999999999
				}
			},
			{
				"id": "real-acc-2",
				"provider": "google",
				"email": "user2@gmail.com",
				"status": "active",
				"token": {
					"access_token": "ya29.test2",
					"refresh_token": "1//test2",
					"project_id": "test-project-2",
					"expiry_timestamp": 9999999999
				}
			}
		]
	}`
	exportPath := filepath.Join(tempDir, "cloud-accounts-export-2026-09-09.json")
	if err := os.WriteFile(exportPath, []byte(exportJSON), 0600); err != nil {
		t.Fatalf("failed to write export file: %v", err)
	}

	pool, err := NewPool(loader, tempDir)
	if err != nil {
		t.Fatalf("NewPool with FileLoader failed: %v", err)
	}

	accs := pool.GetAccounts()
	if len(accs) != 2 {
		t.Fatalf("expected 2 accounts from FileLoader, got %d", len(accs))
	}

	// Lease both accounts in round robin
	l1, err := pool.LeaseAccount("")
	if err != nil {
		t.Fatalf("lease 1 failed: %v", err)
	}
	l2, err := pool.LeaseAccount("")
	if err != nil {
		t.Fatalf("lease 2 failed: %v", err)
	}
	if l1.ID == l2.ID {
		t.Errorf("expected different accounts leased, got %s and %s", l1.ID, l2.ID)
	}

	// Mark cooldown and verify
	pool.MarkCooldown("real-acc-1", 5*time.Minute)
	l3, err := pool.LeaseAccount("")
	if err != nil {
		t.Fatalf("lease 3 failed: %v", err)
	}
	if l3.ID != "real-acc-2" {
		t.Errorf("expected real-acc-2 when real-acc-1 in cooldown, got %s", l3.ID)
	}
}

func TestPool_DeepCopy_Isolation(t *testing.T) {
	accs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", 3600, "proj-1"),
	}
	loader := &mockLoader{accounts: accs}
	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1. GetAccounts returns copies: mutating returned copy should not affect pool
	list := pool.GetAccounts()
	if len(list) != 1 {
		t.Fatalf("expected 1 account, got %d", len(list))
	}
	list[0].Status = "mutated-status"
	list[0].CooldownUntil = time.Now().Add(1 * time.Hour)

	// Verify pool's account is intact
	freshList := pool.GetAccounts()
	if freshList[0].Status != "active" {
		t.Errorf("expected status 'active', got %q (copy was mutated)", freshList[0].Status)
	}
	if !freshList[0].CooldownUntil.IsZero() {
		t.Errorf("expected zero cooldown, got %v (copy was mutated)", freshList[0].CooldownUntil)
	}

	// 2. GetActiveAccount returns copy: mutating should not affect pool
	active := pool.GetActiveAccount()
	if active == nil {
		t.Fatalf("expected non-nil active account")
	}
	active.Status = "mutated-active"

	freshActive := pool.GetActiveAccount()
	if freshActive.Status != "active" {
		t.Errorf("expected active status 'active', got %q", freshActive.Status)
	}
}

func TestPool_CacheSaveFailure_DoesNotFailLease(t *testing.T) {
	accs := []*CloudAccount{
		makeTestAccount("acc-1", "a1@test.com", -100, "proj-1"),
	}
	loader := &mockLoader{
		accounts: accs,
		saveErr:  fmt.Errorf("disk full or permission denied"),
	}
	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pool.refreshTokenFunc = func(account *CloudAccount, proxyURL string) (*CloudToken, error) {
		now := time.Now().Unix()
		return &CloudToken{
			AccessToken:     "refreshed-token",
			RefreshToken:    account.Token.RefreshToken,
			TokenType:       "Bearer",
			ProjectID:       account.Token.ProjectID,
			ExpiresIn:       3600,
			ExpiryTimestamp: now + 3600,
		}, nil
	}

	// Lease should succeed even though SaveAccountsCache returned an error!
	acc, err := pool.LeaseAccount("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("lease should succeed despite cache save failure, got error: %v", err)
	}
	if acc.Token.AccessToken != "refreshed-token" {
		t.Errorf("expected refreshed token, got %q", acc.Token.AccessToken)
	}
}

func TestPool_Reload_CooldownEmailFallback(t *testing.T) {
	// Old account has ID "id-1" and Email "user@test.com"
	initialAccs := []*CloudAccount{
		makeTestAccount("id-1", "user@test.com", 3600, "proj-1"),
	}
	loader := &mockLoader{accounts: initialAccs}
	pool, err := NewPool(loader, "/test/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pool.MarkCooldown("user@test.com", 15*time.Minute)

	// New accounts loaded: account now has empty ID but same Email
	newAccs := []*CloudAccount{
		{
			ID:       "",
			Email:    "user@test.com",
			Provider: "google",
			Status:   "active",
			Token: CloudToken{
				AccessToken:     "new-token",
				RefreshToken:    "new-refresh",
				TokenType:       "Bearer",
				ProjectID:       "proj-1",
				ExpiresIn:       3600,
				ExpiryTimestamp: time.Now().Unix() + 3600,
			},
		},
	}
	loader.accounts = newAccs

	if err := pool.Reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	accs := pool.GetAccounts()
	if len(accs) != 1 {
		t.Fatalf("expected 1 account after reload, got %d", len(accs))
	}
	if !accs[0].IsCooldown() {
		t.Errorf("expected cooldown to be preserved via Email match")
	}
}

func TestPool_PinAccount_VPS_Reboot_Simulation(t *testing.T) {
	tempDir := t.TempDir()
	fileLoader := NewFileLoader()

	// Create initial export file with 2 accounts
	exportContent := `{
		"version": "1.0",
		"accounts": [
			{
				"id": "acc-1",
				"email": "acc-1@example.com",
				"provider": "google",
				"token": {"access_token": "tok-1", "project_id": "proj-1", "expiry_timestamp": 9999999999}
			},
			{
				"id": "acc-2",
				"email": "acc-2@example.com",
				"provider": "google",
				"token": {"access_token": "tok-2", "project_id": "proj-2", "expiry_timestamp": 9999999999}
			}
		]
	}`
	exportPath := filepath.Join(tempDir, "cloud-accounts-export-2026-09-09.json")
	if err := os.WriteFile(exportPath, []byte(exportContent), 0644); err != nil {
		t.Fatalf("failed to write export file: %v", err)
	}

	// 1. Initial boot: should be round-robin
	pool1, err := NewPool(fileLoader, tempDir)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}
	isPinned, _ := pool1.IsPinned()
	if isPinned {
		t.Errorf("expected pool1 to start in round-robin mode")
	}

	// 2. Pin to acc-2
	if err := pool1.PinAccount("acc-2"); err != nil {
		t.Fatalf("PinAccount failed: %v", err)
	}
	isPinned, pinnedID := pool1.IsPinned()
	if !isPinned || pinnedID != "acc-2" {
		t.Errorf("expected pool1 to be pinned to acc-2, got (%v, %q)", isPinned, pinnedID)
	}
	active := pool1.GetActiveAccount()
	if active == nil || active.ID != "acc-2" {
		t.Errorf("expected active account acc-2, got %+v", active)
	}

	// 3. SIMULATE VPS REBOOT: new Pool instance with same configDir
	pool2, err := NewPool(fileLoader, tempDir)
	if err != nil {
		t.Fatalf("NewPool after reboot failed: %v", err)
	}
	isPinned2, pinnedID2 := pool2.IsPinned()
	if !isPinned2 || pinnedID2 != "acc-2" {
		t.Fatalf("expected pool2 after reboot to remain PINNED to acc-2, got (%v, %q)", isPinned2, pinnedID2)
	}
	active2 := pool2.GetActiveAccount()
	if active2 == nil || active2.ID != "acc-2" {
		t.Errorf("expected active account after reboot to be acc-2, got %+v", active2)
	}

	// 4. Unpin on pool2
	if err := pool2.Unpin(); err != nil {
		t.Fatalf("Unpin failed: %v", err)
	}
	isPinnedAfterUnpin, _ := pool2.IsPinned()
	if isPinnedAfterUnpin {
		t.Errorf("expected pool2 to be unpinned")
	}

	// 5. SIMULATE SECOND VPS REBOOT: should start in round-robin
	pool3, err := NewPool(fileLoader, tempDir)
	if err != nil {
		t.Fatalf("NewPool after second reboot failed: %v", err)
	}
	isPinned3, pinnedID3 := pool3.IsPinned()
	if isPinned3 || pinnedID3 != "" {
		t.Errorf("expected pool3 after unpin and reboot to be round-robin, got (%v, %q)", isPinned3, pinnedID3)
	}
}

func TestPool_Reload_UpdatesPinnedAccountFromConfig(t *testing.T) {
	tempDir := t.TempDir()
	fileLoader := NewFileLoader()

	exportContent := `{
		"version": "1.0",
		"accounts": [
			{
				"id": "acc-1",
				"email": "acc-1@example.com",
				"provider": "google",
				"token": {"access_token": "tok-1", "expiry_timestamp": 9999999999}
			},
			{
				"id": "acc-2",
				"email": "acc-2@example.com",
				"provider": "google",
				"token": {"access_token": "tok-2", "expiry_timestamp": 9999999999}
			}
		]
	}`
	_ = os.WriteFile(filepath.Join(tempDir, "cloud-accounts-export-2026-09-09.json"), []byte(exportContent), 0644)

	pool, err := NewPool(fileLoader, tempDir)
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}
	if isPinned, _ := pool.IsPinned(); isPinned {
		t.Errorf("expected initial pool to be round-robin")
	}

	// Edit config.json on disk directly
	cfgPath := filepath.Join(tempDir, "config.json")
	_ = os.WriteFile(cfgPath, []byte(`{"pinned_account_id": "acc-1"}`), 0644)

	// Call Reload (e.g. user pressed 'R' in TUI)
	if err := pool.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	isPinned, pinnedID := pool.IsPinned()
	if !isPinned || pinnedID != "acc-1" {
		t.Errorf("expected pool to be pinned to acc-1 after Reload, got (%v, %q)", isPinned, pinnedID)
	}

	// Edit config.json to unpin
	_ = os.WriteFile(cfgPath, []byte(`{"pinned_account_id": ""}`), 0644)
	if err := pool.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	isPinned, _ = pool.IsPinned()
	if isPinned {
		t.Errorf("expected pool to be unpinned after Reload with empty pinned_account_id")
	}
}

