package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	cacheMutex  sync.Mutex
	configMutex sync.Mutex
	_           AccountLoader = (*FileLoader)(nil)
)

const (
	cacheFileName  = "cloud-accounts-cache.json"
	configFileName = "config.json"
)

// FileLoader implements AccountLoader reading and persisting accounts on the filesystem.
type FileLoader struct{}

// NewLoader creates a new FileLoader instance.
func NewLoader() *FileLoader {
	return &FileLoader{}
}

// NewFileLoader creates a new FileLoader instance.
func NewFileLoader() *FileLoader {
	return &FileLoader{}
}

// LoadAccounts loads accounts from dir using FileLoader.
func (l *FileLoader) LoadAccounts(dir string) ([]*CloudAccount, error) {
	return LoadAccounts(dir)
}

// SaveAccountsCache saves accounts to dir using FileLoader.
func (l *FileLoader) SaveAccountsCache(dir string, accounts []*CloudAccount) error {
	return SaveAccountsCache(dir, accounts)
}

// LoadPinnedAccount loads pinned account ID from dir using FileLoader.
func (l *FileLoader) LoadPinnedAccount(dir string) (string, error) {
	return LoadPinnedAccount(dir)
}

// SavePinnedAccount saves pinned account ID to dir using FileLoader.
func (l *FileLoader) SavePinnedAccount(dir string, accountID string) error {
	return SavePinnedAccount(dir, accountID)
}

// LoadAccounts scans dir for export files (cloud-accounts-export-*.json, accounts.json)
// and merges cached state (cloud-accounts-cache.json).
func LoadAccounts(dir string) ([]*CloudAccount, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to access config dir %q: %w", dir, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read config dir %q: %w", dir, err)
	}

	var exportFiles []string
	hasAccountsJSON := false
	hasCache := false

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if matched, _ := filepath.Match("cloud-accounts-export-*.json", name); matched {
			exportFiles = append(exportFiles, name)
		} else if name == "accounts.json" {
			hasAccountsJSON = true
		} else if name == cacheFileName {
			hasCache = true
		}
	}

	sort.Strings(exportFiles)

	accountsMap := make(map[string]*CloudAccount)
	var accountOrder []string

	addOrUpdate := func(acc *CloudAccount) {
		key := acc.ID
		if key == "" {
			key = acc.Email
		}
		if key == "" {
			return
		}
		if _, exists := accountsMap[key]; !exists {
			accountOrder = append(accountOrder, key)
		}
		accountsMap[key] = acc
	}

	// 1. Process export files
	for _, fileName := range exportFiles {
		fullPath := filepath.Join(dir, fileName)
		accs, err := readAccountFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read export file %s: %w", fileName, err)
		}
		for _, acc := range accs {
			addOrUpdate(acc)
		}
	}

	// 2. Process accounts.json if present
	if hasAccountsJSON {
		fullPath := filepath.Join(dir, "accounts.json")
		accs, err := readAccountFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read accounts.json: %w", err)
		}
		for _, acc := range accs {
			addOrUpdate(acc)
		}
	}

	// 3. Process cache file if present and merge updated tokens / state
	if hasCache {
		fullPath := filepath.Join(dir, cacheFileName)
		cachedAccs, err := readAccountFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read cache file %s: %w", cacheFileName, err)
		}

		for _, cached := range cachedAccs {
			key := cached.ID
			if key == "" {
				key = cached.Email
			}
			if key == "" {
				continue
			}

			if existing, found := accountsMap[key]; found {
				// Merge cached token details
				if cached.Token.AccessToken != "" {
					existing.Token.AccessToken = cached.Token.AccessToken
				}
				if cached.Token.RefreshToken != "" {
					existing.Token.RefreshToken = cached.Token.RefreshToken
				}
				if cached.Token.TokenType != "" {
					existing.Token.TokenType = cached.Token.TokenType
				}
				if cached.Token.ProjectID != "" {
					existing.Token.ProjectID = cached.Token.ProjectID
				}
				if cached.Token.ExpiresIn != 0 {
					existing.Token.ExpiresIn = cached.Token.ExpiresIn
				}
				if cached.Token.ExpiryTimestamp != 0 {
					existing.Token.ExpiryTimestamp = cached.Token.ExpiryTimestamp
				}
				if cached.Status != "" {
					existing.Status = cached.Status
				}
				if !cached.CooldownUntil.IsZero() {
					existing.CooldownUntil = cached.CooldownUntil
				}
				if cached.ProxyURL != "" {
					existing.ProxyURL = cached.ProxyURL
				}
			} else {
				addOrUpdate(cached)
			}
		}
	}

	result := make([]*CloudAccount, 0, len(accountOrder))
	for _, key := range accountOrder {
		result = append(result, accountsMap[key])
	}

	return result, nil
}

func readAccountFile(filePath string) ([]*CloudAccount, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return []*CloudAccount{}, nil
	}

	// 1. Try envelope with "accounts" field or single object
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err == nil {
		if accountsRaw, ok := raw["accounts"]; ok {
			var accounts []*CloudAccount
			if err := json.Unmarshal(accountsRaw, &accounts); err != nil {
				return nil, fmt.Errorf("invalid 'accounts' field in %s: %w", filepath.Base(filePath), err)
			}
			return accounts, nil
		}

		// Check if it represents a single account object
		var single CloudAccount
		if err := json.Unmarshal([]byte(trimmed), &single); err == nil && (single.Email != "" || single.Token.RefreshToken != "") {
			return []*CloudAccount{&single}, nil
		}
	}

	// 2. Try array of accounts
	var list []*CloudAccount
	if err := json.Unmarshal([]byte(trimmed), &list); err == nil {
		return list, nil
	}

	return nil, fmt.Errorf("unrecognized account json format in %s", filepath.Base(filePath))
}

// SaveAccountsCache atomically persists accounts to cloud-accounts-cache.json in dir,
// preserving any currently pinned account.
func SaveAccountsCache(dir string, accounts []*CloudAccount) error {
	// Preserve existing pinned account from config.json or existing cache
	pinnedID := ""
	cfgPath := filepath.Join(dir, configFileName)
	if cfgData, err := os.ReadFile(cfgPath); err == nil {
		var raw map[string]interface{}
		if err := json.Unmarshal(cfgData, &raw); err == nil {
			for _, key := range []string{"pinned_account_id", "pinnedAccountId", "pinned_account", "pinned"} {
				if val, ok := raw[key]; ok {
					if s, ok := val.(string); ok {
						pinnedID = strings.TrimSpace(s)
						break
					}
				}
			}
		}
	}

	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	cachePath := filepath.Join(dir, cacheFileName)
	if pinnedID == "" {
		if cData, err := os.ReadFile(cachePath); err == nil {
			var env ExportEnvelope
			if err := json.Unmarshal(cData, &env); err == nil {
				pinnedID = env.PinnedAccountID
			}
		}
	}

	if accounts == nil {
		accounts = []*CloudAccount{}
	}

	now := time.Now().Unix()
	envelope := ExportEnvelope{
		Version:         "1.0",
		ExportedAt:      now,
		UpdatedAt:       now,
		PinnedAccountID: pinnedID,
		Accounts:        accounts,
	}

	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal accounts cache: %w", err)
	}
	data = append(data, '\n')

	return atomicWriteFile(cachePath, data)
}

// LoadPinnedAccount reads the pinned account ID from config.json or cloud-accounts-cache.json.
func LoadPinnedAccount(dir string) (string, error) {
	configMutex.Lock()
	defer configMutex.Unlock()

	// 1. Check config.json (or bridge-config.json)
	for _, cfgName := range []string{configFileName, "bridge-config.json"} {
		cfgPath := filepath.Join(dir, cfgName)
		if data, err := os.ReadFile(cfgPath); err == nil {
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err == nil {
				for _, key := range []string{"pinned_account_id", "pinnedAccountId", "pinned_account", "pinned"} {
					if val, ok := raw[key]; ok {
						if s, ok := val.(string); ok {
							return strings.TrimSpace(s), nil
						}
					}
				}
			}
		}
	}

	// 2. Check cloud-accounts-cache.json
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	cachePath := filepath.Join(dir, cacheFileName)
	if data, err := os.ReadFile(cachePath); err == nil {
		var env ExportEnvelope
		if err := json.Unmarshal(data, &env); err == nil && env.PinnedAccountID != "" {
			return strings.TrimSpace(env.PinnedAccountID), nil
		}
	}

	// 3. Check accounts.json or export files envelope
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if matched, _ := filepath.Match("cloud-accounts-export-*.json", name); matched || name == "accounts.json" {
				if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
					var env ExportEnvelope
					if err := json.Unmarshal(data, &env); err == nil && env.PinnedAccountID != "" {
						return strings.TrimSpace(env.PinnedAccountID), nil
					}
				}
			}
		}
	}

	return "", nil
}

// SavePinnedAccount atomically saves the pinned account ID into config.json and updates cloud-accounts-cache.json.
func SavePinnedAccount(dir string, accountID string) error {
	configMutex.Lock()
	defer configMutex.Unlock()

	trimmedID := strings.TrimSpace(accountID)

	// 1. Save to config.json
	cfgPath := filepath.Join(dir, configFileName)
	configMap := make(map[string]interface{})
	if data, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(data, &configMap)
	}
	configMap["pinned_account_id"] = trimmedID

	data, err := json.MarshalIndent(configMap, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal bridge config: %w", err)
	}
	data = append(data, '\n')

	if err := atomicWriteFile(cfgPath, data); err != nil {
		return fmt.Errorf("failed to write %s: %w", configFileName, err)
	}

	// 2. Update cloud-accounts-cache.json if it exists
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	cachePath := filepath.Join(dir, cacheFileName)
	if cData, err := os.ReadFile(cachePath); err == nil {
		var env ExportEnvelope
		if err := json.Unmarshal(cData, &env); err == nil {
			env.PinnedAccountID = trimmedID
			env.UpdatedAt = time.Now().Unix()
			if outData, err := json.MarshalIndent(env, "", "  "); err == nil {
				outData = append(outData, '\n')
				_ = atomicWriteFile(cachePath, outData)
			}
		}
	}

	return nil
}

func atomicWriteFile(targetPath string, data []byte) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	baseName := filepath.Base(targetPath)
	tmpPath := filepath.Join(dir, fmt.Sprintf("%s.%d.tmp", baseName, time.Now().UnixNano()))

	tmpFile, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write to temporary file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to sync temporary file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temporary file: %w", err)
	}

	// Atomic rename to targetPath
	if err := os.Rename(tmpPath, targetPath); err != nil {
		// On Windows, if target exists and cannot be replaced atomically directly
		_ = os.Remove(targetPath)
		if retryErr := os.Rename(tmpPath, targetPath); retryErr != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to rename %s to %s: %w", tmpPath, targetPath, retryErr)
		}
	}

	return nil
}
