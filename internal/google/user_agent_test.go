package google

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestParseAntigravityManifestVersion(t *testing.T) {
	tests := []struct {
		name     string
		yamlText string
		expected string
	}{
		{
			name:     "standard unquoted version",
			yamlText: "version: 2.8.0\nfiles:\n  - url: antigravity-mac.zip",
			expected: "2.8.0",
		},
		{
			name:     "double-quoted version",
			yamlText: "version: \"2.9.1\"\npath: something.dmg",
			expected: "2.9.1",
		},
		{
			name:     "single-quoted version with comments",
			yamlText: "version: '3.0.0-beta.1' # latest release\n",
			expected: "3.0.0-beta.1",
		},
		{
			name:     "missing version",
			yamlText: "path: something.dmg\nsha512: abcdef",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ParseAntigravityManifestVersion(tt.yamlText)
			if actual != tt.expected {
				t.Errorf("ParseAntigravityManifestVersion() = %q, want %q", actual, tt.expected)
			}
		})
	}
}

func TestUserAgentManager_EnvOverride(t *testing.T) {
	mgr := NewUserAgentManager()

	customUA := "custom-test-agent/9.9.9"
	_ = os.Setenv("ANTIGRAVITY_USER_AGENT", customUA)
	defer os.Unsetenv("ANTIGRAVITY_USER_AGENT")

	if got := mgr.Get(); got != customUA {
		t.Errorf("expected env override %q, got %q", customUA, got)
	}
}

func TestUserAgentManager_FetchLatestVersion(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte("version: 3.1.5\n"))
	}))
	defer mockServer.Close()

	mgr := NewUserAgentManager()
	mgr.manifestURL = mockServer.URL

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := mgr.FetchLatestVersion(ctx)
	if err != nil {
		t.Fatalf("unexpected error fetching version: %v", err)
	}

	expectedVersion := "3.1.5"
	expectedUA := FormatAntigravityUserAgent(expectedVersion, DefaultCL)
	if got := mgr.Get(); got != expectedUA {
		t.Errorf("expected UA %q, got %q", expectedUA, got)
	}
}
