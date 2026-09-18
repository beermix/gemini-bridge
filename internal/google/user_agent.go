package google

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultAntigravityVersion is the fallback Antigravity client version.
	DefaultAntigravityVersion = "2.8.0"
	// DefaultCL is the changelist/build number of the reference Antigravity client.
	DefaultCL = "963137146"
	// DefaultAntigravityManifestURL is the official update manifest used by Antigravity Hub on macOS.
	DefaultAntigravityManifestURL = "https://antigravity-hub-auto-updater-974169037036.us-central1.run.app/manifest/latest-arm64-mac.yml"
	// ClientMetadataHeader is sent to identify as a Gemini/Cloud Code IDE plugin.
	ClientMetadataHeader = "ideType=IDE_UNSPECIFIED,platform=PLATFORM_UNSPECIFIED,pluginType=GEMINI"
)

var manifestVersionRegex = regexp.MustCompile(`(?m)^\s*version\s*:\s*["']?([^"'\s#]+)["']?`)

// FormatAntigravityUserAgent constructs the full User-Agent string mirroring the official Antigravity Hub client.
func FormatAntigravityUserAgent(version, cl string) string {
	if cl == "" {
		cl = DefaultCL
	}
	return fmt.Sprintf("antigravity/hub/%s (aidev_client; os_type=darwin; arch=arm64; cl=%s)", version, cl)
}

// ParseAntigravityManifestVersion extracts the version value from an electron-builder YAML update manifest.
func ParseAntigravityManifestVersion(yamlText string) string {
	matches := manifestVersionRegex.FindStringSubmatch(yamlText)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// UserAgentManager manages dynamic discovery and caching of the Antigravity User-Agent.
type UserAgentManager struct {
	mu          sync.RWMutex
	currentUA   string
	version     string
	manifestURL string
	httpClient  *http.Client
}

var (
	defaultUAManager     *UserAgentManager
	defaultUAManagerOnce sync.Once
)

// NewUserAgentManager creates a new UserAgentManager initialized with the default pinned version.
func NewUserAgentManager() *UserAgentManager {
	initialVersion := DefaultAntigravityVersion
	if envVer := os.Getenv("PI_AI_ANTIGRAVITY_VERSION"); envVer != "" {
		initialVersion = envVer
	}
	return &UserAgentManager{
		version:     initialVersion,
		currentUA:   FormatAntigravityUserAgent(initialVersion, DefaultCL),
		manifestURL: DefaultAntigravityManifestURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// DefaultUserAgentMgr returns the singleton default UserAgentManager.
func DefaultUserAgentMgr() *UserAgentManager {
	defaultUAManagerOnce.Do(func() {
		defaultUAManager = NewUserAgentManager()
	})
	return defaultUAManager
}

// Get returns the active User-Agent string, checking environment variables first.
func (m *UserAgentManager) Get() string {
	if envUA := os.Getenv("ANTIGRAVITY_USER_AGENT"); envUA != "" {
		return envUA
	}
	if envUA := os.Getenv("GEMINI_BRIDGE_USER_AGENT"); envUA != "" {
		return envUA
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentUA
}

// FetchLatestVersion queries the Antigravity update manifest and updates the active User-Agent if a new version is found.
func (m *UserAgentManager) FetchLatestVersion(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.manifestURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create manifest request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch antigravity update manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("manifest request returned status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("failed to read manifest body: %w", err)
	}

	version := ParseAntigravityManifestVersion(string(body))
	if version == "" {
		return fmt.Errorf("no valid version found in manifest")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if version != m.version {
		m.version = version
		m.currentUA = FormatAntigravityUserAgent(version, DefaultCL)
		log.Printf("[UserAgent] Updated Antigravity User-Agent to version %s: %s", version, m.currentUA)
	}
	return nil
}

// StartAutoUpdater runs a periodic background loop checking for Antigravity client updates.
func (m *UserAgentManager) StartAutoUpdater(ctx context.Context, interval time.Duration) {
	// Run initial check non-blocking
	go func() {
		fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := m.FetchLatestVersion(fetchCtx); err != nil {
			log.Printf("[UserAgent] Initial manifest fetch skipped/failed (using fallback %s): %v", m.version, err)
		}
		cancel()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tickCtx, tickCancel := context.WithTimeout(ctx, 5*time.Second)
				_ = m.FetchLatestVersion(tickCtx)
				tickCancel()
			}
		}
	}()
}

// GetAntigravityUserAgent returns the active Antigravity User-Agent from the default manager.
func GetAntigravityUserAgent() string {
	return DefaultUserAgentMgr().Get()
}
