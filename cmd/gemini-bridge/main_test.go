package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeListenAddr(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "127.0.0.1:8045"},
		{"8045", "127.0.0.1:8045"},
		{":8045", "127.0.0.1:8045"},
		{"0.0.0.0:8045", "0.0.0.0:8045"},
		{"127.0.0.1:9000", "127.0.0.1:9000"},
	}

	for _, tc := range tests {
		got := normalizeListenAddr(tc.input)
		if got != tc.expected {
			t.Errorf("normalizeListenAddr(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestNormalizeAdminListenAddr(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "127.0.0.1:8046"},
		{"8046", "127.0.0.1:8046"},
		{"127.0.0.1:8046", "127.0.0.1:8046"},
		{":8046", ":8046"},
		{"0.0.0.0:8046", "0.0.0.0:8046"},
	}

	for _, tc := range tests {
		got := normalizeAdminListenAddr(tc.input)
		if got != tc.expected {
			t.Errorf("normalizeAdminListenAddr(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestResolveConfigDir(t *testing.T) {
	// Existing directory test
	tempDir := t.TempDir()
	got := resolveConfigDir(tempDir)
	if got != tempDir {
		t.Errorf("resolveConfigDir(%q) = %q, want %q", tempDir, got, tempDir)
	}

	// Default fallback when /root/gemini-bridge doesn't exist
	defaultFallback := resolveConfigDir("/root/gemini-bridge")
	cwd, _ := os.Getwd()
	if defaultFallback != cwd && defaultFallback != "." && defaultFallback != "/root/gemini-bridge" && defaultFallback != "/root/agy-bridge2" {
		t.Errorf("resolveConfigDir(/root/gemini-bridge) unexpected fallback: %q", defaultFallback)
	}

	// Custom non-existent path clean
	customNonExistent := filepath.Join(tempDir, "sub", "..", "sub")
	gotCustom := resolveConfigDir(customNonExistent)
	if gotCustom != filepath.Clean(customNonExistent) {
		t.Errorf("resolveConfigDir(%q) = %q, want %q", customNonExistent, gotCustom, filepath.Clean(customNonExistent))
	}
}

func TestMaskKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"short", "***"},
		{"12345678", "***"},
		{"sk-antigravity", "sk-...vity"},
		{"sk-1234567890abcdef", "sk-...cdef"},
	}

	for _, tc := range tests {
		got := maskKey(tc.input)
		if got != tc.expected {
			t.Errorf("maskKey(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestGetEnv(t *testing.T) {
	key := "TEST_AGY_ENV_VAR"
	_ = os.Unsetenv(key)

	if got := getEnv(key, "default_val"); got != "default_val" {
		t.Errorf("expected default_val, got %q", got)
	}

	_ = os.Setenv(key, "custom_val")
	defer os.Unsetenv(key)

	if got := getEnv(key, "default_val"); got != "custom_val" {
		t.Errorf("expected custom_val, got %q", got)
	}
}

func TestGetProxyEnv(t *testing.T) {
	origHTTPS := os.Getenv("HTTPS_PROXY")
	origHTTP := os.Getenv("HTTP_PROXY")
	defer func() {
		_ = os.Setenv("HTTPS_PROXY", origHTTPS)
		_ = os.Setenv("HTTP_PROXY", origHTTP)
	}()

	_ = os.Unsetenv("HTTPS_PROXY")
	_ = os.Unsetenv("HTTP_PROXY")
	_ = os.Unsetenv("https_proxy")
	_ = os.Unsetenv("http_proxy")

	if got := getProxyEnv(); got != "" {
		t.Errorf("expected empty proxy env, got %q", got)
	}

	_ = os.Setenv("HTTPS_PROXY", "http://proxy.internal:8080")
	if got := getProxyEnv(); got != "http://proxy.internal:8080" {
		t.Errorf("expected http://proxy.internal:8080, got %q", got)
	}
}

func TestServeFlagSet_PinnedAccount(t *testing.T) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	pinnedAccountFlag := fs.String("pinned-account", getEnv("PINNED_ACCOUNT", ""), "Pin to specific account ID or email")

	args := []string{"-pinned-account", "override@example.com"}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("failed to parse flags: %v", err)
	}

	if *pinnedAccountFlag != "override@example.com" {
		t.Errorf("expected -pinned-account 'override@example.com', got %q", *pinnedAccountFlag)
	}
}

