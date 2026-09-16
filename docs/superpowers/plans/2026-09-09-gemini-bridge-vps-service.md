# Gemini-Bridge VPS Service Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a lightweight, high-performance Go service for Linux VPS that acts as an API proxy (OpenAI `/v1/chat/completions` and Anthropic `/v1/messages`) compatible with AntigravityManager and hermes-agent, reading accounts from `/root/gemini-bridge/cloud-accounts-export-*.json`, auto-refreshing Google OAuth tokens, providing automatic failover on 429, and featuring an interactive TUI for account monitoring and selection.

**Architecture:** A unified binary supporting background daemon (`serve`) and interactive terminal client (`tui`). The proxy transforms incoming OpenAI/Anthropic requests directly into Google Cloud Code internal requests (`cloudcode-pa.googleapis.com/v1internal`), streams SSE chunks with reasoning and tool calling, rotates accounts with Round-Robin / Cooldown on 429, and exposes an internal Admin IPC for the TUI.

**Tech Stack:** Go 1.27 (standard library `net/http`), Charm Bubbletea & Lipgloss for TUI, Google Cloud Code v1internal API, Google OAuth2.

**Spec:** `docs/superpowers/specs/2026-09-09-gemini-bridge-vps-service-design.md`

## Global Constraints
- Target platform: Linux (x86_64 and arm64), cross-compilable from Windows.
- Default config directory: `/root/gemini-bridge` (configurable via `-config-dir` or `CONFIG_DIR`, defaulting to current working directory if `/root/gemini-bridge` does not exist).
- Default proxy port: `8045`, default admin IPC port: `8046`.
- Account source files: `cloud-accounts-export-*.json` and `accounts.json`.
- OAuth credentials: Antigravity default Google OAuth client credentials (stored encoded with env overrides).
- Upstream endpoint: `https://cloudcode-pa.googleapis.com/v1internal` with failover to `https://daily-cloudcode-pa.googleapis.com/v1internal`.

---

### Task 1: Account Models, File Loader & Token Persistence

**Files:**
- Create: `internal/account/model.go`
- Create: `internal/account/loader.go`
- Test: `internal/account/loader_test.go`

**Interfaces:**
- Produces:
  - `type CloudAccount struct { ID, Provider, Email, Name string; Token CloudToken; Status string; CooldownUntil time.Time }`
  - `type CloudToken struct { AccessToken, RefreshToken, TokenType, ProjectID string; ExpiresIn int64; ExpiryTimestamp int64 }`
  - `type AccountLoader interface { LoadAccounts(dir string) ([]*CloudAccount, error); SaveAccountsCache(dir string, accounts []*CloudAccount) error }`

- [ ] **Step 1: Write the failing test**
  Create `internal/account/loader_test.go` testing loading from a sample JSON export file and saving updated cache.
- [ ] **Step 2: Run test to verify it fails**
  Run: `go test -v ./internal/account/...`
  Expected: compilation failure (undefined types and functions).
- [ ] **Step 3: Implement minimal code**
  Implement `model.go` and `loader.go` to scan directory for `cloud-accounts-export-*.json`, parse JSON, and atomic cache save.
- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/account/...`
  Expected: PASS.
- [ ] **Step 5: Commit**
  Run: `git add internal/account/ && git commit -m "feat(account): add models, file loader and persistence"`

---

### Task 2: Google OAuth Token Refresh & Project Discovery

**Files:**
- Create: `internal/account/oauth.go`
- Create: `internal/account/project.go`
- Test: `internal/account/oauth_test.go`

**Interfaces:**
- Consumes: `CloudAccount`, `CloudToken` from `internal/account/model.go`
- Produces:
  - `func RefreshAccessToken(account *CloudAccount, proxyURL string) (*CloudToken, error)`
  - `func FetchProjectID(account *CloudAccount, proxyURL string) (string, error)`

- [ ] **Step 1: Write the failing test**
  Create `internal/account/oauth_test.go` with mock HTTP servers for `oauth2.googleapis.com/token` and `loadCodeAssist`.
- [ ] **Step 2: Run test to verify it fails**
  Run: `go test -v ./internal/account/... -run TestOAuth`
  Expected: compilation failure (functions not defined).
- [ ] **Step 3: Implement minimal code**
  Implement `oauth.go` sending POST request to `https://oauth2.googleapis.com/token` with `refresh_token`, and `project.go` calling `https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist`.
- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/account/... -run TestOAuth`
  Expected: PASS.
- [ ] **Step 5: Commit**
  Run: `git add internal/account/oauth.go internal/account/project.go internal/account/oauth_test.go && git commit -m "feat(account): add oauth refresh and project discovery"`

---

### Task 3: Account Pool, Selection, Failover & Cooldown

**Files:**
- Create: `internal/account/pool.go`
- Test: `internal/account/pool_test.go`

**Interfaces:**
- Consumes: `CloudAccount`, `AccountLoader`, `RefreshAccessToken`, `FetchProjectID`
- Produces:
  - `type Pool struct`:
    - `func NewPool(loader AccountLoader, configDir string) (*Pool, error)`
    - `func (p *Pool) LeaseAccount(model string) (*CloudAccount, error)`
    - `func (p *Pool) MarkCooldown(accountID string, duration time.Duration)`
    - `func (p *Pool) PinAccount(accountID string)`
    - `func (p *Pool) Unpin()`
    - `func (p *Pool) GetAccounts() []*CloudAccount`
    - `func (p *Pool) Reload() error`

- [ ] **Step 1: Write the failing test**
  Create `internal/account/pool_test.go` verifying Round-Robin leasing, pinning, 429 cooldown handling, and token expiry refresh.
- [ ] **Step 2: Run test to verify it fails**
  Run: `go test -v ./internal/account/... -run TestPool`
  Expected: compilation failure.
- [ ] **Step 3: Implement minimal code**
  Implement `pool.go` with mutex protection, index tracking, automatic cooldown checks, and token refresh.
- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/account/... -run TestPool`
  Expected: PASS.
- [ ] **Step 5: Commit**
  Run: `git add internal/account/pool.go internal/account/pool_test.go && git commit -m "feat(account): add pool with round-robin, pinning and 429 cooldown"`

---

### Task 4: Google Upstream Client & Gemini Internal Types

**Files:**
- Create: `internal/google/types.go`
- Create: `internal/google/client.go`
- Test: `internal/google/client_test.go`

**Interfaces:**
- Produces:
  - `type GeminiInternalRequest struct { Project, RequestID, Model, UserAgent, RequestType string; Request GeminiRequest }`
  - `type GeminiResponse struct { Candidates []Candidate; UsageMetadata UsageMetadata }`
  - `type Client struct`:
    - `func NewClient(timeout time.Duration) *Client`
    - `func (c *Client) StreamGenerateContent(ctx context.Context, account *account.CloudAccount, body *GeminiInternalRequest) (io.ReadCloser, error)`
    - `func (c *Client) GenerateContent(ctx context.Context, account *account.CloudAccount, body *GeminiInternalRequest) (*GeminiResponse, error)`

- [ ] **Step 1: Write the failing test**
  Create `internal/google/client_test.go` testing requests with auth header `Bearer <token>` and `x-goog-user-project`, plus fallback endpoint on error.
- [ ] **Step 2: Run test to verify it fails**
  Run: `go test -v ./internal/google/...`
  Expected: compilation failure.
- [ ] **Step 3: Implement minimal code**
  Implement `types.go` and `client.go` with `http.Client`, proxy support from account, and primary/secondary baseUrl fallback.
- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/google/...`
  Expected: PASS.
- [ ] **Step 5: Commit**
  Run: `git add internal/google/ && git commit -m "feat(google): add upstream client and gemini types"`

---

### Task 5: Protocol Mappers (OpenAI $\leftrightarrow$ Gemini & Anthropic $\leftrightarrow$ Gemini)

**Files:**
- Create: `internal/proxy/model_routing.go`
- Create: `internal/proxy/openai_mapper.go`
- Create: `internal/proxy/anthropic_mapper.go`
- Test: `internal/proxy/mapper_test.go`

**Interfaces:**
- Produces:
  - `func ResolveModel(requestedModel string) string`
  - `func MapOpenAIToGemini(req *OpenAIChatRequest, projectID string) *google.GeminiInternalRequest`
  - `func MapAnthropicToGemini(req *AnthropicMessagesRequest, projectID string) *google.GeminiInternalRequest`
  - `func ParseGeminiChunk(data []byte) (*google.GeminiResponse, error)`
  - `func FormatOpenAIChunk(streamID, model string, cand *google.Candidate, usage *google.UsageMetadata) []byte`
  - `func FormatAnthropicEvents(msgID, model string, cand *google.Candidate, state *AnthropicStreamState) [][]byte`

- [ ] **Step 1: Write the failing test**
  Create `internal/proxy/mapper_test.go` testing:
  - Model routing (`claude-3-7-sonnet` -> `claude-sonnet-4-6-thinking`, `gpt-4o` -> `gemini-3-flash`)
  - OpenAI request conversion with messages, system prompt, and tools
  - Anthropic request conversion with content blocks and tool use
  - Gemini SSE chunk parsing and conversion to OpenAI delta chunk (`content`, `reasoning_content`, `tool_calls`)
  - Anthropic SSE event emission (`content_block_delta`, `thinking_delta`, `message_delta`)
- [ ] **Step 2: Run test to verify it fails**
  Run: `go test -v ./internal/proxy/... -run TestMapper`
  Expected: compilation failure.
- [ ] **Step 3: Implement minimal code**
  Implement `model_routing.go`, `openai_mapper.go`, and `anthropic_mapper.go`.
- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/proxy/... -run TestMapper`
  Expected: PASS.
- [ ] **Step 5: Commit**
  Run: `git add internal/proxy/model_routing.go internal/proxy/openai_mapper.go internal/proxy/anthropic_mapper.go internal/proxy/mapper_test.go && git commit -m "feat(proxy): add openai and anthropic protocol mappers"`

---

### Task 6: HTTP Proxy Server & Endpoints

**Files:**
- Create: `internal/proxy/auth.go`
- Create: `internal/proxy/openai_handler.go`
- Create: `internal/proxy/anthropic_handler.go`
- Create: `internal/proxy/server.go`
- Test: `internal/proxy/server_test.go`

**Interfaces:**
- Consumes: `internal/account/pool.go`, `internal/google/client.go`, mappers
- Produces:
  - `type Server struct`:
    - `func NewServer(cfg ServerConfig, pool *account.Pool, client *google.Client) *Server`
    - `func (s *Server) Start(addr string) error`
    - `func (s *Server) Shutdown(ctx context.Context) error`

- [ ] **Step 1: Write the failing test**
  Create `internal/proxy/server_test.go` with mock Google upstream:
  - Testing `POST /v1/chat/completions` (streaming & non-streaming)
  - Testing `POST /v1/chat/completions` with Tool Calling (verify tool_calls structure)
  - Testing `POST /v1/messages` (Anthropic streaming)
  - Testing `GET /v1/models` and `GET /health`
  - Testing auth header validation (`Bearer <key>` check)
  - Testing 429 auto-retry on next account
- [ ] **Step 2: Run test to verify it fails**
  Run: `go test -v ./internal/proxy/... -run TestServer`
  Expected: compilation failure.
- [ ] **Step 3: Implement minimal code**
  Implement `auth.go`, `openai_handler.go`, `anthropic_handler.go`, and `server.go` with `http.Handler`, `http.Flusher` streaming, retry logic on 429.
- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/proxy/... -run TestServer`
  Expected: PASS.
- [ ] **Step 5: Commit**
  Run: `git add internal/proxy/ && git commit -m "feat(proxy): add proxy server with openai, anthropic and health endpoints"`

---

### Task 7: Admin IPC Server & Client

**Files:**
- Create: `internal/admin/server.go`
- Create: `internal/admin/client.go`
- Test: `internal/admin/admin_test.go`

**Interfaces:**
- Consumes: `internal/account/pool.go`, request statistics from proxy
- Produces:
  - `type AdminServer struct`:
    - `func NewAdminServer(pool *account.Pool, statsProvider StatsProvider) *AdminServer`
    - `func (s *AdminServer) Start(addr string) error`
  - `type AdminClient struct`:
    - `func NewAdminClient(baseURL string) *AdminClient`
    - `func (c *AdminClient) GetStatus() (*StatusResponse, error)`
    - `func (c *AdminClient) PinAccount(accountID string) error`
    - `func (c *AdminClient) Unpin() error`
    - `func (c *AdminClient) Reload() error`
    - `func (c *AdminClient) RefreshToken(accountID string) error`

- [x] **Step 1: Write the failing test**
  Create `internal/admin/admin_test.go` testing client-server communication over localhost.
- [x] **Step 2: Run test to verify it fails**
  Run: `go test -v ./internal/admin/...`
  Expected: compilation failure.
- [x] **Step 3: Implement minimal code**
  Implement `server.go` and `client.go` with JSON endpoints on `127.0.0.1:8046`.
- [x] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/admin/...`
  Expected: PASS.
- [x] **Step 5: Commit**
  Run: `git add internal/admin/ && git commit -m "feat(admin): add admin ipc server and client"`

---

### Task 8: Interactive Terminal User Interface (TUI)

**Files:**
- Create: `internal/tui/styles.go`
- Create: `internal/tui/view.go`
- Create: `internal/tui/app.go`
- Test: `internal/tui/app_test.go`

**Interfaces:**
- Consumes: `internal/admin/client.go`
- Produces:
  - `func RunTUI(adminURL string) error`

- [ ] **Step 1: Write the failing test**
  Create `internal/tui/app_test.go` testing Bubbletea model initialization, key message handling (`Enter`, `p`, `r`, `R`, `q`), and status rendering.
- [ ] **Step 2: Run test to verify it fails**
  Run: `go test -v ./internal/tui/...`
  Expected: compilation failure.
- [ ] **Step 3: Implement minimal code**
  Implement `styles.go` with Lipgloss colors/badges, `view.go` with table layout, and `app.go` with Bubbletea event loop.
- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/tui/...`
  Expected: PASS.
- [ ] **Step 5: Commit**
  Run: `git add internal/tui/ && git commit -m "feat(tui): add interactive bubbletea terminal user interface"`

---

### Task 9: CLI Entrypoint, Build Scripts, Service Unit & hermes-agent Documentation

**Files:**
- Create: `cmd/gemini-bridge/main.go`
- Create: `gemini-bridge.service`
- Create: `build.sh`
- Create: `build.bat`
- Create: `README.md`

- [x] **Step 1: Write `cmd/gemini-bridge/main.go`**
  Add CLI parser with subcommands `serve` (starts proxy + admin server in background) and `tui` (launches interactive TUI client). If run without arguments in terminal, attaches TUI to running daemon or starts all-in-one.
- [x] **Step 2: Create `gemini-bridge.service`**
  Systemd service definition targeting `/root/gemini-bridge` and `/usr/local/bin/gemini-bridge`.
- [x] **Step 3: Create build scripts**
  `build.sh` and `build.bat` with `GOOS=linux GOARCH=amd64` cross-compilation flags and `-ldflags="-s -w"`.
- [x] **Step 4: Create `README.md`**
  Complete documentation: quick start, configuration for hermes-agent (OpenAI and Anthropic configs), TUI usage, systemd setup.
- [x] **Step 5: Execute complete test suite and build verification**
  Run: `go test -v ./...`
  Run: `go build -o gemini-bridge.exe ./cmd/gemini-bridge`
  Run: `$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o gemini-bridge ./cmd/gemini-bridge`
  Verify both binaries compile cleanly without errors.
- [x] **Step 6: Commit**
  Run: `git add cmd/ gemini-bridge.service build.sh build.bat README.md && git commit -m "feat: complete cli entrypoint, systemd service and build scripts"`
