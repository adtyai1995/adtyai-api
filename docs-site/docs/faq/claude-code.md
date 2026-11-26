# Claude Code FAQ

Common setup and runtime issues when running Anthropic's **Claude Code** CLI against ADTY AI, along with proven fixes. For fresh installation steps, see the [Claude Code Integration Guide](/integrations/cli/claude-code).

## 1. HTTP `404` because `ANTHROPIC_BASE_URL` ends with `/v1`

This is the single most common Claude Code mistake. Claude Code automatically appends `/v1/messages` to `ANTHROPIC_BASE_URL`.

- **Wrong**: `https://api.adtyai.com/v1` → sends requests to `/v1/v1/messages` (`404 Not Found`)
- **Wrong**: `https://www.adtyai.com` → rejected with `403 api_domain_required`
- **Correct**: `https://api.adtyai.com` (no trailing `/v1` and no trailing slash)

```bash
export ANTHROPIC_BASE_URL="https://api.adtyai.com"
export ANTHROPIC_AUTH_TOKEN="<YOUR_API_KEY>"
```

## 2. `ANTHROPIC_AUTH_TOKEN` vs. `ANTHROPIC_API_KEY`

Claude Code supports both environment variables, but mixing them or leaving stale values in shell profiles (`~/.zshrc`, `~/.bashrc`, or Windows System Environment Variables) causes confusing `401` errors or prompts asking whether to use a detected API key.

- **Recommended**: Set `ANTHROPIC_AUTH_TOKEN` (matching our [Interactive Setup Wizard](/integrations/cli/)) and ensure no conflicting old `ANTHROPIC_API_KEY` is exported in the same shell.
- If you configure credentials inside `~/.claude/settings.json` under the `env` block, make sure your shell environment does not override those values with an older key.
- Verify what your active shell actually sees:

```bash
# macOS / Linux
env | grep -E "ANTHROPIC_|CLAUDE_"
```

```powershell
# Windows PowerShell
Get-ChildItem Env: | Where-Object { $_.Name -match "^(ANTHROPIC_|CLAUDE_)" }
```

## 3. OAuth Login Prompt Keeps Appearing

If launching `claude` opens a browser login flow to `claude.ai` instead of using your ADTY AI API key:

1. Confirm both `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN` are set in the exact terminal session (or inside `~/.claude/settings.json`'s `env` object).
2. Sign out of any cached OAuth session inside Claude Code by running `/logout` or clearing stale OAuth tokens from `~/.claude.json` / system keychain if you previously logged in with a consumer subscription.
3. Ensure `~/.claude.json` includes `"hasCompletedOnboarding": true` if Claude Code stalls on the first-run login wizard in headless or CI environments.

## 4. Windows PowerShell Execution Policy & Git Bash Issues

### Script Execution Blocked (`PSSecurityException`)

If PowerShell blocks `claude` or `npm` shim scripts with `running scripts is disabled on this system`:

```powershell
Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned
```

Restart PowerShell and test `claude --version`.

### Git Bash Not Found on Windows

Claude Code relies on Git Bash on native Windows. If startup fails because `bash.exe` cannot be located:

1. Install [Git for Windows](https://git-scm.com/downloads/win) if not already installed.
2. Point Claude Code directly to your `bash.exe` path:

```powershell
$env:CLAUDE_CODE_GIT_BASH_PATH="C:\Program Files\Git\bin\bash.exe"
```

To persist it across terminals, add `CLAUDE_CODE_GIT_BASH_PATH` in Windows Environment Variables (or your PowerShell `$PROFILE`).

## 5. Prompt Cache Hit Rate Drops Unexpectedly

Claude Code sends large system prompts, tool definitions, and conversation prefixes on every turn. If prompt caching stops hitting (`cache_read_input_tokens` drops to `0` across back-to-back turns), latency rises and token costs increase dramatically.

Common causes include client version quirks, changing model/beta headers mid-session, or mismatched tool definitions. Follow our dedicated step-by-step guide in [Claude Code Cache Fix](/integrations/cli/cache-fix) to diagnose and restore high cache hit rates.

## 6. Corporate Proxies (`HTTPS_PROXY` / `HTTP_PROXY`)

If your network requires an HTTP/SOCKS proxy to reach `https://api.adtyai.com`:

```bash
export HTTPS_PROXY="http://127.0.0.1:7890"
export HTTP_PROXY="http://127.0.0.1:7890"
export NO_PROXY="localhost,127.0.0.1"
```

- If you see `UNABLE_TO_VERIFY_LEAF_SIGNATURE` or TLS handshake errors behind a corporate SSL-inspecting proxy, supply your organization's root CA bundle via `NODE_EXTRA_CA_CERTS="/path/to/corp-ca.pem"`.
- If you previously configured a dead proxy port and forgot about it, unset `HTTP_PROXY`, `HTTPS_PROXY`, and `ALL_PROXY` before retrying.

## 7. Extended Thinking & Effort Parameters

When using Claude models with extended thinking or adaptive effort controls:

- Ensure `max_tokens` is always **strictly greater** than the configured thinking token budget; otherwise the API returns `400 Bad Request`.
- If you override `MAX_THINKING_TOKENS` in your environment or `settings.json`, keep it proportionate to the task—very high budgets increase latency before the first visible response token appears.
- Verify that the model ID configured in Claude Code (including any Haiku/Sonnet/Opus aliases) is enabled inside the concrete model group bound to your ADTY AI API key.
