package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"gemini-bridge/internal/account"
	"gemini-bridge/internal/admin"
	"gemini-bridge/internal/google"
	"gemini-bridge/internal/proxy"
	"gemini-bridge/internal/tui"

	"github.com/mattn/go-isatty"
)

const (
	Version = "1.0.1"

	defaultProxyPort = "8045"
	defaultAdminPort = "8046"
	defaultConfigDir = "/root/gemini-bridge"
	defaultAPIKey    = "sk-antigravity"
	defaultAdminURL  = "http://127.0.0.1:8046"
)

func main() {
	if len(os.Args) < 2 {
		handleDefaultOrUsage()
		return
	}

	subcommand := strings.ToLower(os.Args[1])
	switch subcommand {
	case "serve":
		runServe(os.Args[2:])
	case "tui":
		runTUI(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "version", "-v", "--version":
		printVersion()
	case "help", "-h", "--help":
		printUsage()
	default:
		// If an unknown argument starts with '-', check if it's help
		if strings.HasPrefix(subcommand, "-") {
			printUsage()
			return
		}
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func handleDefaultOrUsage() {
	// If stdout is an interactive terminal, check if admin server is alive
	if isTerminal(os.Stdout.Fd()) {
		adminURL := getEnv("ADMIN_URL", defaultAdminURL)
		client := admin.NewAdminClient(adminURL)
		client.SetHTTPClient(&http.Client{Timeout: 600 * time.Millisecond})

		if _, err := client.GetStatus(); err == nil {
			// Admin server is running, attach TUI
			if err := tui.RunTUI(adminURL); err != nil {
				fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	printUsage()
}

func isTerminal(fd uintptr) bool {
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)

	portFlag := fs.String("port", getEnv("PROXY_PORT", defaultProxyPort), "Proxy listener port or host:port")
	adminPortFlag := fs.String("admin-port", getEnv("ADMIN_PORT", defaultAdminPort), "Admin API listener port or host:port")
	configDirFlag := fs.String("config-dir", getEnv("CONFIG_DIR", defaultConfigDir), "Path to directory containing account configurations")
	apiKeyFlag := fs.String("api-key", getEnv("PROXY_API_KEY", defaultAPIKey), "Bearer API key required for client requests")
	proxyURLFlag := fs.String("proxy-url", getProxyEnv(), "Optional upstream HTTP/HTTPS outbound proxy URL")
	pinnedAccountFlag := fs.String("pinned-account", getEnv("PINNED_ACCOUNT", ""), "Pin to specific account ID or email (overrides config)")

	_ = fs.Parse(args)

	// Resolve config directory with fallback
	configDir := resolveConfigDir(*configDirFlag)

	// Ensure config directory exists or create it
	if err := os.MkdirAll(configDir, 0755); err != nil {
		log.Printf("Warning: unable to create config dir %q: %v", configDir, err)
	}

	// Setup outbound proxy environment variables if specified
	proxyURL := strings.TrimSpace(*proxyURLFlag)
	if proxyURL != "" {
		_ = os.Setenv("HTTP_PROXY", proxyURL)
		_ = os.Setenv("HTTPS_PROXY", proxyURL)
		_ = os.Setenv("http_proxy", proxyURL)
		_ = os.Setenv("https_proxy", proxyURL)
	}

	// Normalize listener addresses
	proxyAddr := normalizeListenAddr(*portFlag)
	adminAddr := normalizeAdminListenAddr(*adminPortFlag)

	log.Printf("=== Gemini-Bridge v%s starting ===", Version)
	log.Printf("Config Directory : %s", configDir)
	log.Printf("Proxy Address    : %s", proxyAddr)
	log.Printf("Admin Address    : %s", adminAddr)
	log.Printf("API Key          : %s", maskKey(*apiKeyFlag))
	if proxyURL != "" {
		log.Printf("Outbound Proxy   : %s", proxyURL)
	}

	// Initialize account loader and pool
	loader := account.NewFileLoader()
	pool, err := account.NewPool(loader, configDir)
	if err != nil {
		log.Fatalf("Failed to initialize account pool: %v", err)
	}

	// Apply command-line or env-var pinned account override if provided
	if *pinnedAccountFlag != "" {
		if err := pool.PinAccount(*pinnedAccountFlag); err != nil {
			log.Printf("Warning: failed to pin account %q from flag: %v", *pinnedAccountFlag, err)
		} else {
			log.Printf("Pinned account override applied: %s", *pinnedAccountFlag)
		}
	}

	// Propagate global outbound proxy to accounts lacking one
	if proxyURL != "" {
		for _, acc := range pool.GetAccounts() {
			if acc.ProxyURL == "" {
				acc.ProxyURL = proxyURL
			}
		}
	}

	accounts := pool.GetAccounts()
	log.Printf("Loaded %d account(s) from %s", len(accounts), configDir)
	for i, acc := range accounts {
		log.Printf("  [%d] %s (%s) [Project: %s]", i+1, acc.Email, acc.Provider, acc.Token.ProjectID)
	}

	if isPinned, pid := pool.IsPinned(); isPinned {
		log.Printf("Active Mode      : PINNED (Account: %s)", pid)
	} else {
		log.Printf("Active Mode      : ROUND-ROBIN")
	}

	// Initialize Google upstream client with 120s timeout
	googleClient := google.NewClient(120 * time.Second)

	// Initialize Proxy Server
	proxyCfg := proxy.ServerConfig{
		Addr:   proxyAddr,
		APIKey: *apiKeyFlag,
	}
	proxyServer := proxy.NewServer(proxyCfg, pool, googleClient)

	// Initialize Admin Server
	adminServer := admin.NewAdminServer(pool, proxyServer)

	// Start servers
	errChan := make(chan error, 2)

	go func() {
		log.Printf("[proxy] HTTP server listening on %s", proxyAddr)
		if err := proxyServer.Start(proxyAddr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("proxy server: %w", err)
		}
	}()

	go func() {
		log.Printf("[admin] IPC server listening on %s", adminAddr)
		if err := adminServer.Start(adminAddr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("admin server: %w", err)
		}
	}()

	// Signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		log.Printf("Received signal %v, shutting down gracefully...", sig)
	case err := <-errChan:
		log.Printf("Fatal error: %v", err)
	}

	// Graceful shutdown context with 5-second deadline
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := adminServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error shutting down admin server: %v", err)
	}
	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error shutting down proxy server: %v", err)
	}

	log.Println("Gemini-Bridge stopped successfully.")
}

func runTUI(args []string) {
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	adminURLFlag := fs.String("admin-url", getEnv("ADMIN_URL", defaultAdminURL), "Admin server URL to connect to")
	_ = fs.Parse(args)

	adminURL := strings.TrimSpace(*adminURLFlag)
	if err := tui.RunTUI(adminURL); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	adminURLFlag := fs.String("admin-url", getEnv("ADMIN_URL", defaultAdminURL), "Admin server URL")
	jsonFlag := fs.Bool("json", false, "Output status as JSON")
	_ = fs.Parse(args)

	adminURL := strings.TrimSpace(*adminURLFlag)
	client := admin.NewAdminClient(adminURL)

	status, err := client.GetStatus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to admin server at %s: %v\n", adminURL, err)
		os.Exit(1)
	}

	if *jsonFlag {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(status); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to serialize JSON: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Human-readable status output
	fmt.Println("================================================================")
	fmt.Printf(" Gemini-Bridge Service Status (%s)\n", adminURL)
	fmt.Println("================================================================")
	fmt.Printf("Mode     : %s\n", strings.ToUpper(status.Mode))
	if status.PinnedAccountID != "" {
		fmt.Printf("Pinned To: %s\n", status.PinnedAccountID)
	}
	fmt.Printf("Uptime   : %s\n", status.Uptime)
	fmt.Printf("Requests : Total: %d | Success: %d | 429 Cooldown: %d | Errors: %d\n",
		status.Stats.TotalRequests,
		status.Stats.SuccessRequests,
		status.Stats.RateLimitRequests,
		status.Stats.ErrorRequests,
	)
	fmt.Println("----------------------------------------------------------------")
	fmt.Printf("Accounts (%d):\n", len(status.Accounts))
	if len(status.Accounts) == 0 {
		fmt.Println("  (no accounts loaded - check your config-dir for cloud-accounts-export-*.json)")
	} else {
		fmt.Printf("  %-3s %-32s %-10s %-12s %s\n", "#", "EMAIL / ID", "STATUS", "COOLDOWN", "PROJECT ID")
		for i, acc := range status.Accounts {
			prefix := " "
			if status.PinnedAccountID != "" && (acc.ID == status.PinnedAccountID || acc.Email == status.PinnedAccountID) {
				prefix = "*"
			}
			cdStr := "none"
			if !acc.CooldownUntil.IsZero() && time.Now().Before(acc.CooldownUntil) {
				rem := time.Until(acc.CooldownUntil).Round(time.Second)
				cdStr = rem.String()
			}
			st := acc.Status
			if st == "" {
				st = "active"
			}
			proj := acc.Token.ProjectID
			if proj == "" {
				proj = "(pending)"
			}

			dispName := acc.Email
			if dispName == "" {
				dispName = acc.ID
			}
			if len(dispName) > 31 {
				dispName = dispName[:28] + "..."
			}

			fmt.Printf(" %s%-2d %-32s %-10s %-12s %s\n", prefix, i+1, dispName, st, cdStr, proj)
		}
	}
	fmt.Println("================================================================")
}

func printVersion() {
	fmt.Printf("gemini-bridge v%s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
}

func printUsage() {
	printVersion()
	fmt.Println(`
A high-performance VPS proxy bridge connecting AI agents (hermes-agent, OpenAI SDK, Anthropic SDK)
to Google Antigravity Cloud Code accounts with multi-account round-robin and auto-failover.

Usage:
  gemini-bridge [subcommand] [flags]

Subcommands:
  serve       Run proxy server and admin IPC daemon
  tui         Launch interactive terminal user interface (Bubbletea TUI)
  status      Display current bridge status and metrics (non-interactive)
  version     Show version information
  help        Show this help message

Flags for 'serve':
  -port           Proxy listening port or address (default: "8045", env: PROXY_PORT)
  -admin-port     Admin IPC listening port or address (default: "8046", env: ADMIN_PORT)
  -config-dir     Directory with cloud-accounts-export-*.json (default: /root/gemini-bridge, env: CONFIG_DIR)
  -api-key        Proxy authentication key (default: "sk-antigravity", env: PROXY_API_KEY)
  -proxy-url      Outbound proxy URL (env: HTTPS_PROXY or HTTP_PROXY)
  -pinned-account Pinned account ID or email (env: PINNED_ACCOUNT)

Flags for 'tui':
  -admin-url    Admin API URL (default: "http://127.0.0.1:8046", env: ADMIN_URL)

Flags for 'status':
  -admin-url    Admin API URL (default: "http://127.0.0.1:8046", env: ADMIN_URL)
  -json         Output raw JSON response

Default Behavior:
  If executed in an interactive terminal without arguments and the admin daemon is running,
  gemini-bridge automatically launches the interactive TUI. Otherwise, it prints this help.

Examples:
  # Start daemon on VPS:
  gemini-bridge serve -port 8045 -config-dir /root/gemini-bridge

  # Attach interactive TUI to running daemon:
  gemini-bridge tui

  # Check status via SSH in shell script:
  gemini-bridge status`)
}

// resolveConfigDir checks if specified dir exists. If default /root/gemini-bridge does not exist,
// checks legacy /root/agy-bridge2, and then falls back to the current working directory.
func resolveConfigDir(dir string) string {
	target := strings.TrimSpace(dir)
	if target == "" {
		target = defaultConfigDir
	}

	// If target exists, use it
	if fi, err := os.Stat(target); err == nil && fi.IsDir() {
		return target
	}

	// If default /root/gemini-bridge does not exist, check legacy /root/agy-bridge2
	if target == defaultConfigDir {
		legacyDir := "/root/agy-bridge2"
		if fi, err := os.Stat(legacyDir); err == nil && fi.IsDir() {
			return legacyDir
		}
		if cwd, err := os.Getwd(); err == nil && cwd != "" {
			return cwd
		}
		return "."
	}

	// Return clean path
	return filepath.Clean(target)
}

func normalizeListenAddr(addr string) string {
	s := strings.TrimSpace(addr)
	if s == "" {
		return "127.0.0.1:" + defaultProxyPort
	}
	if strings.HasPrefix(s, ":") {
		return "127.0.0.1" + s
	}
	if !strings.Contains(s, ":") {
		return "127.0.0.1:" + s
	}
	return s
}

func normalizeAdminListenAddr(addr string) string {
	s := strings.TrimSpace(addr)
	if s == "" {
		return "127.0.0.1:" + defaultAdminPort
	}
	if !strings.Contains(s, ":") {
		return "127.0.0.1:" + s
	}
	return s
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getProxyEnv() string {
	if val := os.Getenv("HTTPS_PROXY"); val != "" {
		return val
	}
	if val := os.Getenv("HTTP_PROXY"); val != "" {
		return val
	}
	if val := os.Getenv("https_proxy"); val != "" {
		return val
	}
	if val := os.Getenv("http_proxy"); val != "" {
		return val
	}
	return ""
}

func maskKey(k string) string {
	if len(k) <= 8 {
		return "***"
	}
	return k[:3] + "..." + k[len(k)-4:]
}
