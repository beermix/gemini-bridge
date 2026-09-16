package account

import (
	"encoding/json"
	"time"
)

// CloudAccount represents an Antigravity Cloud Account.
type CloudAccount struct {
	ID            string     `json:"id,omitempty"`
	Provider      string     `json:"provider"`
	Email         string     `json:"email"`
	Name          string     `json:"name,omitempty"`
	Token         CloudToken `json:"token"`
	Status        string     `json:"status,omitempty"`
	CooldownUntil time.Time  `json:"cooldown_until,omitempty"`
	ProxyURL      string     `json:"proxy_url,omitempty"`
}

// CloudToken represents OAuth token and project metadata for an account.
type CloudToken struct {
	AccessToken     string `json:"access_token"`
	RefreshToken    string `json:"refresh_token"`
	TokenType       string `json:"token_type"`
	ProjectID       string `json:"project_id,omitempty"`
	ExpiresIn       int64  `json:"expires_in,omitempty"`
	ExpiryTimestamp int64  `json:"expiry_timestamp,omitempty"`
}

// AccountLoader defines loading and persisting accounts and configuration from/to disk.
type AccountLoader interface {
	LoadAccounts(dir string) ([]*CloudAccount, error)
	SaveAccountsCache(dir string, accounts []*CloudAccount) error
	LoadPinnedAccount(dir string) (string, error)
	SavePinnedAccount(dir string, accountID string) error
}

// ExportEnvelope represents the AntigravityManager account export/cache JSON envelope.
type ExportEnvelope struct {
	Version         string          `json:"version,omitempty"`
	ExportedAt      int64           `json:"exportedAt,omitempty"`
	UpdatedAt       int64           `json:"updatedAt,omitempty"`
	PinnedAccountID string          `json:"pinned_account_id,omitempty"`
	Accounts        []*CloudAccount `json:"accounts"`
}

// BridgeConfig represents persistent bridge configuration stored in config.json.
type BridgeConfig struct {
	PinnedAccountID string `json:"pinned_account_id,omitempty"`
}

// UnmarshalJSON supports snake_case and camelCase variations for user flexibility.
func (c *BridgeConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range []string{"pinned_account_id", "pinnedAccountId", "pinned_account", "pinned"} {
		if val, ok := raw[key]; ok {
			if s, ok := val.(string); ok {
				c.PinnedAccountID = s
				return nil
			}
		}
	}
	return nil
}

// IsCooldown returns true if the account is currently cooling down after a rate limit (e.g. 429).
func (a *CloudAccount) IsCooldown() bool {
	return !a.CooldownUntil.IsZero() && time.Now().Before(a.CooldownUntil)
}

// IsTokenExpired returns true if the token is expired or will expire within bufferSeconds.
func (a *CloudAccount) IsTokenExpired(bufferSeconds int64) bool {
	if a.Token.ExpiryTimestamp == 0 {
		return true
	}
	return time.Now().Unix() >= (a.Token.ExpiryTimestamp - bufferSeconds)
}

type accountAlias CloudAccount

func (a *CloudAccount) UnmarshalJSON(data []byte) error {
	type rawAccount struct {
		accountAlias
		RawCooldown json.RawMessage `json:"cooldown_until"`
	}
	var raw rawAccount
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*a = CloudAccount(raw.accountAlias)

	if len(raw.RawCooldown) > 0 && string(raw.RawCooldown) != "null" {
		var sec int64
		if err := json.Unmarshal(raw.RawCooldown, &sec); err == nil && sec > 0 {
			a.CooldownUntil = time.Unix(sec, 0)
		} else {
			var s string
			if err := json.Unmarshal(raw.RawCooldown, &s); err == nil && s != "" {
				if t, err := time.Parse(time.RFC3339, s); err == nil {
					a.CooldownUntil = t
				}
			}
		}
	}

	if a.ID == "" {
		a.ID = a.Email
	}
	if a.Provider == "" {
		a.Provider = "google"
	}
	if a.Status == "" {
		a.Status = "active"
	}
	return nil
}

func (a CloudAccount) MarshalJSON() ([]byte, error) {
	type accountAlias CloudAccount
	if a.CooldownUntil.IsZero() {
		return json.Marshal(&struct {
			accountAlias
			CooldownUntil *string `json:"cooldown_until,omitempty"`
		}{
			accountAlias:  accountAlias(a),
			CooldownUntil: nil,
		})
	}
	formatted := a.CooldownUntil.UTC().Format(time.RFC3339)
	return json.Marshal(&struct {
		accountAlias
		CooldownUntil string `json:"cooldown_until"`
	}{
		accountAlias:  accountAlias(a),
		CooldownUntil: formatted,
	})
}

func (t *CloudToken) UnmarshalJSON(data []byte) error {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	getString := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := raw[k]; ok {
				if s, ok := v.(string); ok {
					return s
				}
			}
		}
		return ""
	}
	getInt64 := func(keys ...string) int64 {
		for _, k := range keys {
			if v, ok := raw[k]; ok {
				switch n := v.(type) {
				case float64:
					return int64(n)
				case int64:
					return n
				case int:
					return int64(n)
				case json.Number:
					val, _ := n.Int64()
					return val
				}
			}
		}
		return 0
	}

	t.AccessToken = getString("access_token", "accessToken")
	t.RefreshToken = getString("refresh_token", "refreshToken")
	t.TokenType = getString("token_type", "tokenType")
	if t.TokenType == "" {
		t.TokenType = "Bearer"
	}
	t.ProjectID = getString("project_id", "projectId")
	t.ExpiresIn = getInt64("expires_in", "expiresIn")
	t.ExpiryTimestamp = getInt64("expiry_timestamp", "expiryTimestamp")

	return nil
}
