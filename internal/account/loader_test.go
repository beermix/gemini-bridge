package account_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gemini-bridge/internal/account"
)

func TestLoader_InterfaceCompliance(t *testing.T) {
	var _ account.AccountLoader = account.NewLoader()
	var _ account.AccountLoader = account.NewFileLoader()
}

func TestLoadAccounts_ExportFile(t *testing.T) {
	tempDir := t.TempDir()

	exportContent := `{
		"version": "1.0",
		"exportedAt": 1725880000,
		"accounts": [
			{
				"provider": "google",
				"email": "user1@example.com",
				"name": "User One",
				"token": {
					"access_token": "ya29.user1-access-token",
					"refresh_token": "1//user1-refresh-token",
					"expires_in": 3600,
					"expiry_timestamp": 1725883600,
					"token_type": "Bearer",
					"project_id": "proj-user1"
				},
				"status": "active"
			}
		]
	}`

	exportPath := filepath.Join(tempDir, "cloud-accounts-export-2026-09-09.json")
	if err := os.WriteFile(exportPath, []byte(exportContent), 0644); err != nil {
		t.Fatalf("failed to write export file: %v", err)
	}

	loader := account.NewLoader()
	accounts, err := loader.LoadAccounts(tempDir)
	if err != nil {
		t.Fatalf("LoadAccounts failed: %v", err)
	}

	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}

	acc := accounts[0]
	if acc.Email != "user1@example.com" {
		t.Errorf("expected email user1@example.com, got %s", acc.Email)
	}
	if acc.ID != "user1@example.com" {
		t.Errorf("expected ID to default to email user1@example.com, got %s", acc.ID)
	}
	if acc.Provider != "google" {
		t.Errorf("expected provider google, got %s", acc.Provider)
	}
	if acc.Name != "User One" {
		t.Errorf("expected name User One, got %s", acc.Name)
	}
	if acc.Status != "active" {
		t.Errorf("expected status active, got %s", acc.Status)
	}
	if acc.Token.AccessToken != "ya29.user1-access-token" {
		t.Errorf("expected access token ya29.user1-access-token, got %s", acc.Token.AccessToken)
	}
	if acc.Token.RefreshToken != "1//user1-refresh-token" {
		t.Errorf("expected refresh token 1//user1-refresh-token, got %s", acc.Token.RefreshToken)
	}
	if acc.Token.TokenType != "Bearer" {
		t.Errorf("expected token type Bearer, got %s", acc.Token.TokenType)
	}
	if acc.Token.ProjectID != "proj-user1" {
		t.Errorf("expected project ID proj-user1, got %s", acc.Token.ProjectID)
	}
	if acc.Token.ExpiresIn != 3600 {
		t.Errorf("expected expires in 3600, got %d", acc.Token.ExpiresIn)
	}
	if acc.Token.ExpiryTimestamp != 1725883600 {
		t.Errorf("expected expiry timestamp 1725883600, got %d", acc.Token.ExpiryTimestamp)
	}
}

func TestLoadAccounts_AccountsJSON(t *testing.T) {
	tempDir := t.TempDir()

	accountsJSONContent := `[
		{
			"id": "custom-id-2",
			"provider": "google",
			"email": "user2@example.com",
			"name": "User Two",
			"token": {
				"access_token": "ya29.user2-token",
				"refresh_token": "1//user2-refresh",
				"expires_in": 3600,
				"expiry_timestamp": 1725883600,
				"token_type": "Bearer"
			},
			"status": "active"
		}
	]`

	filePath := filepath.Join(tempDir, "accounts.json")
	if err := os.WriteFile(filePath, []byte(accountsJSONContent), 0644); err != nil {
		t.Fatalf("failed to write accounts.json: %v", err)
	}

	loader := account.NewLoader()
	accounts, err := loader.LoadAccounts(tempDir)
	if err != nil {
		t.Fatalf("LoadAccounts failed: %v", err)
	}

	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}

	acc := accounts[0]
	if acc.ID != "custom-id-2" {
		t.Errorf("expected custom-id-2, got %s", acc.ID)
	}
	if acc.Email != "user2@example.com" {
		t.Errorf("expected user2@example.com, got %s", acc.Email)
	}
}

func TestLoadAccounts_MergeWithCache(t *testing.T) {
	tempDir := t.TempDir()

	// Initial export with old token and no project_id
	exportContent := `{
		"version": "1.0",
		"accounts": [
			{
				"email": "merge@example.com",
				"name": "Merge User",
				"token": {
					"access_token": "old-token",
					"refresh_token": "refresh-merge",
					"expires_in": 100,
					"expiry_timestamp": 1000,
					"token_type": "Bearer",
					"project_id": ""
				}
			}
		]
	}`
	exportPath := filepath.Join(tempDir, "cloud-accounts-export-2026-09-01.json")
	if err := os.WriteFile(exportPath, []byte(exportContent), 0644); err != nil {
		t.Fatalf("failed to write export: %v", err)
	}

	// Cache with refreshed token and discovered project_id
	cacheContent := `{
		"version": "1.0",
		"exportedAt": 1725885000,
		"accounts": [
			{
				"email": "merge@example.com",
				"token": {
					"access_token": "new-refreshed-token",
					"refresh_token": "refresh-merge",
					"expires_in": 3600,
					"expiry_timestamp": 1725888600,
					"token_type": "Bearer",
					"project_id": "discovered-project-123"
				}
			}
		]
	}`
	cachePath := filepath.Join(tempDir, "cloud-accounts-cache.json")
	if err := os.WriteFile(cachePath, []byte(cacheContent), 0644); err != nil {
		t.Fatalf("failed to write cache: %v", err)
	}

	loader := account.NewLoader()
	accounts, err := loader.LoadAccounts(tempDir)
	if err != nil {
		t.Fatalf("LoadAccounts failed: %v", err)
	}

	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}

	acc := accounts[0]
	if acc.Email != "merge@example.com" {
		t.Errorf("expected email merge@example.com, got %s", acc.Email)
	}
	if acc.Name != "Merge User" {
		t.Errorf("expected name to be preserved from export: got %s", acc.Name)
	}
	if acc.Token.AccessToken != "new-refreshed-token" {
		t.Errorf("expected token from cache: got %s", acc.Token.AccessToken)
	}
	if acc.Token.ProjectID != "discovered-project-123" {
		t.Errorf("expected project ID from cache: got %s", acc.Token.ProjectID)
	}
	if acc.Token.ExpiryTimestamp != 1725888600 {
		t.Errorf("expected expiry timestamp 1725888600, got %d", acc.Token.ExpiryTimestamp)
	}
}

func TestSaveAccountsCache_Atomic(t *testing.T) {
	tempDir := t.TempDir()

	accounts := []*account.CloudAccount{
		{
			ID:       "acc-1",
			Provider: "google",
			Email:    "saved@example.com",
			Name:     "Saved User",
			Token: account.CloudToken{
				AccessToken:     "saved-access-token",
				RefreshToken:    "saved-refresh-token",
				TokenType:       "Bearer",
				ProjectID:       "saved-project-id",
				ExpiresIn:       3600,
				ExpiryTimestamp: 1725890000,
			},
			Status:        "active",
			CooldownUntil: time.Unix(1725895000, 0),
		},
	}

	loader := account.NewLoader()
	if err := loader.SaveAccountsCache(tempDir, accounts); err != nil {
		t.Fatalf("SaveAccountsCache failed: %v", err)
	}

	cacheFile := filepath.Join(tempDir, "cloud-accounts-cache.json")
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("expected cache file to exist: %v", err)
	}

	// Verify no temporary files remain
	tmpFiles, err := filepath.Glob(filepath.Join(tempDir, "*.tmp"))
	if err != nil {
		t.Fatalf("failed to glob tmp files: %v", err)
	}
	if len(tmpFiles) > 0 {
		t.Errorf("expected no .tmp files, found: %v", tmpFiles)
	}

	// Read content and verify
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatalf("failed to read cache file: %v", err)
	}

	var envelope struct {
		Version    string                  `json:"version"`
		ExportedAt int64                   `json:"exportedAt"`
		Accounts   []*account.CloudAccount `json:"accounts"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("failed to parse cache file JSON: %v", err)
	}

	if len(envelope.Accounts) != 1 {
		t.Fatalf("expected 1 account in cache file, got %d", len(envelope.Accounts))
	}
	if envelope.Accounts[0].Email != "saved@example.com" {
		t.Errorf("expected email saved@example.com, got %s", envelope.Accounts[0].Email)
	}
	if envelope.Accounts[0].Token.AccessToken != "saved-access-token" {
		t.Errorf("expected access token saved-access-token, got %s", envelope.Accounts[0].Token.AccessToken)
	}

	// Verify atomic update / overwrite
	accounts[0].Token.AccessToken = "updated-access-token"
	if err := loader.SaveAccountsCache(tempDir, accounts); err != nil {
		t.Fatalf("SaveAccountsCache overwrite failed: %v", err)
	}

	reloaded, err := loader.LoadAccounts(tempDir)
	if err != nil {
		t.Fatalf("LoadAccounts failed after update: %v", err)
	}
	if len(reloaded) != 1 {
		t.Fatalf("expected 1 reloaded account, got %d", len(reloaded))
	}
	if reloaded[0].Token.AccessToken != "updated-access-token" {
		t.Errorf("expected updated-access-token, got %s", reloaded[0].Token.AccessToken)
	}
}

func TestLoadAccounts_EmptyDir(t *testing.T) {
	tempDir := t.TempDir()

	loader := account.NewLoader()
	accounts, err := loader.LoadAccounts(tempDir)
	if err != nil {
		t.Fatalf("expected no error for empty directory, got: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("expected 0 accounts, got %d", len(accounts))
	}
}

func TestLoadAccounts_NonExistentDir(t *testing.T) {
	loader := account.NewLoader()
	_, err := loader.LoadAccounts(filepath.Join(os.TempDir(), "non-existent-dir-123456789"))
	if err == nil {
		t.Errorf("expected error for non-existent directory, got nil")
	}
}

func TestLoadAccounts_InvalidJSON(t *testing.T) {
	tempDir := t.TempDir()

	invalidContent := `{ "broken json: `
	exportPath := filepath.Join(tempDir, "cloud-accounts-export-bad.json")
	if err := os.WriteFile(exportPath, []byte(invalidContent), 0644); err != nil {
		t.Fatalf("failed to write bad file: %v", err)
	}

	loader := account.NewLoader()
	_, err := loader.LoadAccounts(tempDir)
	if err == nil {
		t.Errorf("expected error when reading invalid JSON, got nil")
	}
}

func TestCloudAccount_Helpers(t *testing.T) {
	acc := &account.CloudAccount{
		Email: "helper@example.com",
		Token: account.CloudToken{
			ExpiryTimestamp: time.Now().Unix() + 100, // expires in 100s
		},
	}

	// Not in cooldown
	if acc.IsCooldown() {
		t.Errorf("expected IsCooldown to be false")
	}

	// Set cooldown in future
	acc.CooldownUntil = time.Now().Add(60 * time.Second)
	if !acc.IsCooldown() {
		t.Errorf("expected IsCooldown to be true")
	}

	// Set cooldown in past
	acc.CooldownUntil = time.Now().Add(-10 * time.Second)
	if acc.IsCooldown() {
		t.Errorf("expected IsCooldown to be false for past cooldown")
	}

	// Token expired check: buffer of 300s should report expired because 100s < 300s
	if !acc.IsTokenExpired(300) {
		t.Errorf("expected IsTokenExpired(300) to be true")
	}
	// buffer of 50s should report NOT expired because 100s > 50s
	if acc.IsTokenExpired(50) {
		t.Errorf("expected IsTokenExpired(50) to be false")
	}
}

func TestSaveAccountsCache_Concurrent(t *testing.T) {
	tempDir := t.TempDir()
	loader := account.NewLoader()

	var wg sync.WaitGroup
	workers := 10
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			accs := []*account.CloudAccount{
				{
					Email: "worker@example.com",
					Token: account.CloudToken{AccessToken: "token"},
				},
			}
			_ = loader.SaveAccountsCache(tempDir, accs)
		}(i)
	}
	wg.Wait()

	loaded, err := loader.LoadAccounts(tempDir)
	if err != nil {
		t.Fatalf("failed to load accounts after concurrent writes: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 account loaded, got %d", len(loaded))
	}
}

func TestLoadPinnedAccount_ConfigJSONVariants(t *testing.T) {
	loader := account.NewFileLoader()

	// Test 1: pinned_account_id snake_case
	t.Run("snake_case", func(t *testing.T) {
		tempDir := t.TempDir()
		cfgPath := filepath.Join(tempDir, "config.json")
		_ = os.WriteFile(cfgPath, []byte(`{"pinned_account_id": "acc-snake@example.com"}`), 0644)

		got, err := loader.LoadPinnedAccount(tempDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "acc-snake@example.com" {
			t.Errorf("expected 'acc-snake@example.com', got %q", got)
		}
	})

	// Test 2: pinnedAccountId camelCase
	t.Run("camelCase", func(t *testing.T) {
		tempDir := t.TempDir()
		cfgPath := filepath.Join(tempDir, "config.json")
		_ = os.WriteFile(cfgPath, []byte(`{"pinnedAccountId": "acc-camel@example.com"}`), 0644)

		got, err := loader.LoadPinnedAccount(tempDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "acc-camel@example.com" {
			t.Errorf("expected 'acc-camel@example.com', got %q", got)
		}
	})

	// Test 3: pinned shorthand
	t.Run("shorthand", func(t *testing.T) {
		tempDir := t.TempDir()
		cfgPath := filepath.Join(tempDir, "config.json")
		_ = os.WriteFile(cfgPath, []byte(`{"pinned": "acc-short@example.com"}`), 0644)

		got, err := loader.LoadPinnedAccount(tempDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "acc-short@example.com" {
			t.Errorf("expected 'acc-short@example.com', got %q", got)
		}
	})

	// Test 4: cache fallback when config.json is absent
	t.Run("cache_fallback", func(t *testing.T) {
		tempDir := t.TempDir()
		cachePath := filepath.Join(tempDir, "cloud-accounts-cache.json")
		_ = os.WriteFile(cachePath, []byte(`{
			"version": "1.0",
			"pinned_account_id": "acc-cache@example.com",
			"accounts": []
		}`), 0644)

		got, err := loader.LoadPinnedAccount(tempDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "acc-cache@example.com" {
			t.Errorf("expected 'acc-cache@example.com', got %q", got)
		}
	})
}

func TestSavePinnedAccount_And_PreserveInCache(t *testing.T) {
	tempDir := t.TempDir()
	loader := account.NewFileLoader()

	// 1. Initial save of pinned account
	err := loader.SavePinnedAccount(tempDir, "acc-saved@example.com")
	if err != nil {
		t.Fatalf("SavePinnedAccount failed: %v", err)
	}

	// Verify loaded back
	got, err := loader.LoadPinnedAccount(tempDir)
	if err != nil {
		t.Fatalf("LoadPinnedAccount failed: %v", err)
	}
	if got != "acc-saved@example.com" {
		t.Errorf("expected 'acc-saved@example.com', got %q", got)
	}

	// 2. Save accounts cache (e.g. token refresh) and verify pinned ID is preserved
	accounts := []*account.CloudAccount{
		{
			ID:    "acc-saved",
			Email: "acc-saved@example.com",
			Token: account.CloudToken{AccessToken: "token123"},
		},
	}
	if err := loader.SaveAccountsCache(tempDir, accounts); err != nil {
		t.Fatalf("SaveAccountsCache failed: %v", err)
	}

	// Read cache directly
	cachePath := filepath.Join(tempDir, "cloud-accounts-cache.json")
	cacheBytes, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("failed to read cache: %v", err)
	}
	var env account.ExportEnvelope
	if err := json.Unmarshal(cacheBytes, &env); err != nil {
		t.Fatalf("failed to unmarshal cache: %v", err)
	}
	if env.PinnedAccountID != "acc-saved@example.com" {
		t.Errorf("expected cache envelope pinned_account_id 'acc-saved@example.com', got %q", env.PinnedAccountID)
	}

	// 3. Unpin and verify
	if err := loader.SavePinnedAccount(tempDir, ""); err != nil {
		t.Fatalf("SavePinnedAccount('') failed: %v", err)
	}
	gotUnpinned, err := loader.LoadPinnedAccount(tempDir)
	if err != nil {
		t.Fatalf("LoadPinnedAccount after unpin failed: %v", err)
	}
	if gotUnpinned != "" {
		t.Errorf("expected empty string after unpin, got %q", gotUnpinned)
	}
}
