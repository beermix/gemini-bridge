package account

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// Pool manages a pool of CloudAccounts, providing Round-Robin distribution,
// pinning, automatic 429 cooldowns, token auto-refresh, and project ID discovery.
type Pool struct {
	mu              sync.RWMutex
	refreshMu       sync.Mutex
	loader          AccountLoader
	configDir       string
	accounts        []*CloudAccount
	index           int
	pinnedAccountID string

	// Overridable functions for testing and customization
	refreshTokenFunc func(account *CloudAccount, proxyURL string) (*CloudToken, error)
	fetchProjectFunc func(account *CloudAccount, proxyURL string) (string, error)
	nowFunc          func() time.Time
}

// NewPool creates a new Pool instance and loads accounts from configDir using the provided loader.
func NewPool(loader AccountLoader, configDir string) (*Pool, error) {
	if loader == nil {
		return nil, errors.New("account loader cannot be nil")
	}

	accounts, err := loader.LoadAccounts(configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load accounts from %s: %w", configDir, err)
	}

	pinnedID := ""
	if pid, err := loader.LoadPinnedAccount(configDir); err == nil && pid != "" {
		for _, acc := range accounts {
			if acc.ID == pid || acc.Email == pid {
				pinnedID = pid
				break
			}
		}
		if pinnedID == "" {
			log.Printf("Warning: pinned account %q not found in loaded accounts from %s, using round-robin", pid, configDir)
		}
	}

	return &Pool{
		loader:           loader,
		configDir:        configDir,
		accounts:         accounts,
		index:            0,
		pinnedAccountID:  pinnedID,
		refreshTokenFunc: RefreshAccessToken,
		fetchProjectFunc: FetchProjectID,
		nowFunc:          time.Now,
	}, nil
}

// GetAccounts returns a copy of the accounts slice in the pool,
// performing a copy of each CloudAccount struct to avoid data races with callers.
func (p *Pool) GetAccounts() []*CloudAccount {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]*CloudAccount, len(p.accounts))
	for i, acc := range p.accounts {
		cp := *acc
		result[i] = &cp
	}
	return result
}

// PinAccount sets the pinned account ID (or email) and persists it to configuration.
// All subsequent leases will target this account.
func (p *Pool) PinAccount(accountID string) error {
	p.mu.Lock()
	p.pinnedAccountID = accountID
	loader := p.loader
	dir := p.configDir
	p.mu.Unlock()

	if loader != nil && dir != "" {
		if err := loader.SavePinnedAccount(dir, accountID); err != nil {
			log.Printf("Warning: failed to persist pinned account %q to %s: %v", accountID, dir, err)
			return err
		}
	}
	return nil
}

// Unpin clears the pinned account, returning the pool to Round-Robin selection,
// and persists the unpinned state to configuration.
func (p *Pool) Unpin() error {
	p.mu.Lock()
	p.pinnedAccountID = ""
	loader := p.loader
	dir := p.configDir
	p.mu.Unlock()

	if loader != nil && dir != "" {
		if err := loader.SavePinnedAccount(dir, ""); err != nil {
			log.Printf("Warning: failed to persist unpin to %s: %v", dir, err)
			return err
		}
	}
	return nil
}

// IsPinned returns whether an account is currently pinned, and if so, its ID.
func (p *Pool) IsPinned() (bool, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pinnedAccountID != "", p.pinnedAccountID
}

// GetActiveAccount returns a copy of the currently active account (pinned account if set, or next round-robin candidate).
func (p *Pool) GetActiveAccount() *CloudAccount {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var target *CloudAccount
	if p.pinnedAccountID != "" {
		for _, acc := range p.accounts {
			if acc.ID == p.pinnedAccountID || acc.Email == p.pinnedAccountID {
				target = acc
				break
			}
		}
	} else if len(p.accounts) > 0 {
		idx := p.index % len(p.accounts)
		target = p.accounts[idx]
	}

	if target != nil {
		cp := *target
		return &cp
	}
	return nil
}

// MarkCooldown puts an account on cooldown for the given duration, or clears cooldown if duration <= 0.
func (p *Pool) MarkCooldown(accountID string, duration time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.nowFunc()
	for _, acc := range p.accounts {
		if acc.ID == accountID || acc.Email == accountID {
			if duration > 0 {
				acc.CooldownUntil = now.Add(duration)
				acc.Status = "cooldown"
			} else {
				acc.CooldownUntil = time.Time{}
				if acc.Status == "cooldown" {
					acc.Status = "active"
				}
			}
			break
		}
	}
}

// Reload reloads accounts from the loader, preserving pinned status and active cooldowns.
func (p *Pool) Reload() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	accounts, err := p.loader.LoadAccounts(p.configDir)
	if err != nil {
		return fmt.Errorf("failed to reload accounts: %w", err)
	}

	// Preserve in-memory cooldowns and statuses
	cooldownMap := make(map[string]time.Time)
	statusMap := make(map[string]string)
	for _, old := range p.accounts {
		if !old.CooldownUntil.IsZero() {
			if old.ID != "" {
				cooldownMap[old.ID] = old.CooldownUntil
			}
			if old.Email != "" {
				cooldownMap[old.Email] = old.CooldownUntil
			}
		}
		if old.Status != "" {
			if old.ID != "" {
				statusMap[old.ID] = old.Status
			}
			if old.Email != "" {
				statusMap[old.Email] = old.Status
			}
		}
	}

	// Reload pinned status from config if available
	if p.loader != nil && p.configDir != "" {
		if pid, err := p.loader.LoadPinnedAccount(p.configDir); err == nil {
			p.pinnedAccountID = pid
		}
	}

	pinnedFound := false
	for _, acc := range accounts {
		cd := time.Time{}
		if acc.ID != "" {
			cd = cooldownMap[acc.ID]
		}
		if cd.IsZero() && acc.Email != "" {
			cd = cooldownMap[acc.Email]
		}

		if !cd.IsZero() && acc.CooldownUntil.IsZero() {
			acc.CooldownUntil = cd
			var st string
			if acc.ID != "" {
				st = statusMap[acc.ID]
			}
			if st == "" && acc.Email != "" {
				st = statusMap[acc.Email]
			}
			if st != "" {
				acc.Status = st
			}
		}
		if p.pinnedAccountID != "" && (acc.ID == p.pinnedAccountID || acc.Email == p.pinnedAccountID) {
			pinnedFound = true
		}
	}

	if p.pinnedAccountID != "" && !pinnedFound {
		p.pinnedAccountID = ""
	}

	p.accounts = accounts
	if len(p.accounts) == 0 {
		p.index = 0
	} else {
		p.index = p.index % len(p.accounts)
	}

	if err := p.loader.SaveAccountsCache(p.configDir, p.accounts); err != nil {
		return fmt.Errorf("failed to save accounts cache after reload: %w", err)
	}

	return nil
}

// RefreshToken manually triggers token refresh and project ID discovery for a specific account.
func (p *Pool) RefreshToken(accountID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var target *CloudAccount
	for _, acc := range p.accounts {
		if acc.ID == accountID || acc.Email == accountID {
			target = acc
			break
		}
	}
	if target == nil {
		return fmt.Errorf("account %q not found", accountID)
	}

	if p.refreshTokenFunc != nil {
		newToken, err := p.refreshTokenFunc(target, target.ProxyURL)
		if err != nil {
			return fmt.Errorf("failed to refresh token for %s: %w", target.Email, err)
		}
		if newToken != nil {
			target.Token = *newToken
		}
	}

	if target.Token.ProjectID == "" && p.fetchProjectFunc != nil {
		projectID, err := p.fetchProjectFunc(target, target.ProxyURL)
		if err != nil {
			return fmt.Errorf("failed to fetch project ID for %s: %w", target.Email, err)
		}
		target.Token.ProjectID = projectID
	}

	if p.loader != nil && p.configDir != "" {
		if err := p.loader.SaveAccountsCache(p.configDir, p.accounts); err != nil {
			return fmt.Errorf("failed to save accounts cache: %w", err)
		}
	}

	return nil
}

// LeaseAccount selects an account based on round-robin or pinning, skipping accounts on cooldown.
// It automatically refreshes expired tokens and discovers missing project IDs before returning.
// Concurrency optimized: candidate selection is done under p.mu, while network refresh and disk I/O
// are performed outside p.mu using p.refreshMu to avoid blocking other concurrent requests.
func (p *Pool) LeaseAccount(model string) (*CloudAccount, error) {
	candidate, needsRefresh, err := p.selectCandidate()
	if err != nil {
		return nil, err
	}

	if !needsRefresh {
		return candidate, nil
	}

	// Account needs token refresh or project ID discovery
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()

	now := p.nowFunc()
	nowUnix := now.Unix()

	// Double check under lock if another goroutine already refreshed it while waiting
	p.mu.RLock()
	stillNeedsRefresh := candidate.Token.AccessToken == "" || candidate.Token.ExpiryTimestamp == 0 || nowUnix >= (candidate.Token.ExpiryTimestamp-300) || candidate.Token.ProjectID == ""
	p.mu.RUnlock()

	if !stillNeedsRefresh {
		return candidate, nil
	}

	needSave := false
	if candidate.Token.AccessToken == "" || candidate.Token.ExpiryTimestamp == 0 || nowUnix >= (candidate.Token.ExpiryTimestamp-300) {
		if p.refreshTokenFunc != nil {
			newToken, err := p.refreshTokenFunc(candidate, candidate.ProxyURL)
			if err != nil {
				return nil, fmt.Errorf("failed to refresh access token for %s: %w", candidate.Email, err)
			}
			if newToken != nil {
				p.mu.Lock()
				candidate.Token = *newToken
				p.mu.Unlock()
			}
			needSave = true
		}
	}

	if candidate.Token.ProjectID == "" {
		if p.fetchProjectFunc != nil {
			projectID, err := p.fetchProjectFunc(candidate, candidate.ProxyURL)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch project ID for %s: %w", candidate.Email, err)
			}
			p.mu.Lock()
			candidate.Token.ProjectID = projectID
			p.mu.Unlock()
			needSave = true
		}
	}

	// Persist cache outside global p.mu lock
	if needSave && p.loader != nil && p.configDir != "" {
		p.mu.RLock()
		accountsCopy := make([]*CloudAccount, len(p.accounts))
		for i, a := range p.accounts {
			cp := *a
			accountsCopy[i] = &cp
		}
		dir := p.configDir
		loader := p.loader
		p.mu.RUnlock()

		if err := loader.SaveAccountsCache(dir, accountsCopy); err != nil {
			log.Printf("account pool: failed to save accounts cache: %v", err)
		}
	}

	return candidate, nil
}

func (p *Pool) selectCandidate() (*CloudAccount, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.accounts) == 0 {
		return nil, false, errors.New("no available accounts (all in cooldown or rate limited)")
	}

	now := p.nowFunc()

	// Update expired cooldowns to active
	for _, acc := range p.accounts {
		if !acc.CooldownUntil.IsZero() {
			if !now.Before(acc.CooldownUntil) {
				acc.CooldownUntil = time.Time{}
				if acc.Status == "cooldown" {
					acc.Status = "active"
				}
			}
		}
	}

	var candidate *CloudAccount

	// Case 1: Pinned Mode
	if p.pinnedAccountID != "" {
		for _, acc := range p.accounts {
			if acc.ID == p.pinnedAccountID || acc.Email == p.pinnedAccountID {
				candidate = acc
				break
			}
		}
		if candidate == nil {
			return nil, false, fmt.Errorf("pinned account %q not found", p.pinnedAccountID)
		}
		if !candidate.CooldownUntil.IsZero() && now.Before(candidate.CooldownUntil) {
			return nil, false, fmt.Errorf("pinned account %q is in cooldown until %s", p.pinnedAccountID, candidate.CooldownUntil.Format(time.RFC3339))
		}
		if candidate.Status == "disabled" || candidate.Status == "inactive" {
			return nil, false, fmt.Errorf("pinned account %q is inactive", p.pinnedAccountID)
		}
	} else {
		// Case 2: Round-Robin Mode
		total := len(p.accounts)
		startIndex := p.index % total
		for i := 0; i < total; i++ {
			idx := (startIndex + i) % total
			acc := p.accounts[idx]
			if acc.Status == "disabled" || acc.Status == "inactive" {
				continue
			}
			if !acc.CooldownUntil.IsZero() && now.Before(acc.CooldownUntil) {
				continue
			}
			candidate = acc
			p.index = (idx + 1) % total
			break
		}

		if candidate == nil {
			return nil, false, errors.New("no available accounts (all in cooldown or rate limited)")
		}
	}

	nowUnix := now.Unix()
	needsRefresh := candidate.Token.AccessToken == "" || candidate.Token.ExpiryTimestamp == 0 || nowUnix >= (candidate.Token.ExpiryTimestamp-300) || candidate.Token.ProjectID == ""

	return candidate, needsRefresh, nil
}
