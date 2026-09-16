# Gemini Bridge - Agent Instructions

Gemini Bridge is a lightweight, high-performance Go service and interactive TUI proxy that translates OpenAI (`/v1/chat/completions`) and Anthropic (`/v1/messages`) requests into Google Cloud Code internal requests (`cloudcode-pa.googleapis.com/v1internal`), automatically rotating OAuth accounts and handling rate limits / failover.

## Context Repositories

When developing, debugging, or extending this project, always use the following two companion repositories for context:

1. **Hermes Agent** (`e:\Projects\hermes-agent` / `https://github.com/nousresearch/hermes-agent`):
   - Primary consumer of this proxy.
   - Reference for client configurations, model routing (`gpt-4o`, `claude-3-7-sonnet`, `gemini-*`), tool calling format, and streaming behavior.

2. **Antigravity Manager** (`e:\Projects\AntigravityManager` / `https://github.com/Draculabo/AntigravityManager`):
   - Reference for Google Cloud OAuth credentials, account token structures (`cloud-accounts-export-*.json`), and proxy integration.

## Build and VPS Deployment

- **VPS Host**: `95.85.242.73` (`root@95.85.242.73`)
- **Remote Directory**: `/root/gemini-bridge`
- **Binary Path**: `/root/gemini-bridge/gemini-bridge` (symlink: `/usr/local/bin/gemini-bridge`)
- **Systemd Service**: `gemini-bridge.service`
- **Build Linux AMD64**:
  ```powershell
  $env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o bin/gemini-bridge-linux-amd64 ./cmd/gemini-bridge
  ```
- **Deploy**:
  ```powershell
  scp bin/gemini-bridge-linux-amd64 root@95.85.242.73:/root/gemini-bridge/gemini-bridge.new
  ssh root@95.85.242.73 "chmod +x /root/gemini-bridge/gemini-bridge.new && mv /root/gemini-bridge/gemini-bridge.new /root/gemini-bridge/gemini-bridge && systemctl restart gemini-bridge"
  ```

## GitHub Integration (github-mcp-server)

- For all GitHub operations (issues, pull requests, branches, releases, commits, reviews, repository search/management), **can and should use `github-mcp-server`**.
- Call tools via `call_mcp_tool` with `ServerName: "github-mcp-server"`.
- Target repository: `beermix/gemini-bridge` (owner: `beermix`, repo: `gemini-bridge`).

