# Common Configuration

Before adding individual profiles for Claude Code, Codex, or desktop apps in **CC Switch**, review these shared configuration rules. Taking two minutes to set up clean URL conventions, profile organization, and shell hygiene prevents almost every routing or authentication error later.

## API Base URL Rules (Dual-Domain Policy)

**ADTY AI** separates its web dashboard from its model relay gateway:

- **Web Console**: `https://www.adtyai.com` — for managing wallets, API keys, teams, and logs only. **Never** put this URL into CC Switch as an API endpoint (relay requests to the web domain return HTTP `403`).
- **API Gateway**: `https://api.adtyai.com` — the dedicated endpoint for all model requests.

Different client protocols expect slightly different path suffixes on `https://api.adtyai.com`:

| Target Client in CC Switch | Protocol Family | Correct Base URL | Common Mistake |
| --- | --- | --- | --- |
| Claude Code | Anthropic Messages | `https://api.adtyai.com` | Adding `/v1` causes `/v1/v1/messages` (`404`) |
| Claude Desktop | Anthropic Messages | `https://api.adtyai.com` | Adding `/v1` or using `https://www.adtyai.com` |
| Codex CLI | OpenAI Responses | `https://api.adtyai.com/v1` | Omitting `/v1` causes `404` on `/responses` |
| Codex Desktop App | OpenAI Responses | `https://api.adtyai.com/v1` | Omitting `/v1` or leaving old OAuth cache |
| Gemini CLI | Gemini Native | `https://api.adtyai.com` | Adding `/v1` breaks `/v1beta/models/...` paths |

::: warning Double-Check Trailing Slashes
Avoid trailing slashes (for example, write `https://api.adtyai.com` rather than `https://api.adtyai.com/`). Some CLI tools concatenate paths literally and fail on double slashes.
:::

## Organizing Multiple ADTY AI Profiles

One of the biggest advantages of CC Switch is maintaining multiple ADTY AI profiles side by side. Rather than reusing a single unlimited key everywhere, create dedicated API keys in the ADTY AI console ([https://www.adtyai.com](https://www.adtyai.com)) and label your CC Switch profiles clearly:

- **`ADTY AI - Personal`**: Bound to your personal wallet with a modest quota cap for day-to-day coding.
- **`ADTY AI - Team`**: Uses a team-scoped API key billed to a shared team wallet (see [Team Collaboration](/guides/teams)).
- **`ADTY AI - Coding Group`** / **`ADTY AI - Budget Group`**: Separate API keys pinned to specific channel groups based on your speed, stability, and cost preferences (see [Channel Availability](/guides/channel-status)).
- **`ADTY AI - Client Project`**: Isolated key with its own quota limit and expiration date for clean billing reconciliation (see [Account Security & PAT](/guides/security)).

With clear names, switching workspaces or billing groups in CC Switch becomes a single click instead of hunting through terminal history for old API keys.

## Avoiding Shell Environment Variable Conflicts

CC Switch works by updating each tool's native configuration file (such as `~/.claude/settings.json` or `~/.codex/config.toml` and `~/.codex/auth.json`).

**However, shell environment variables take precedence over config files in almost every CLI tool.** If you previously exported `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`, `OPENAI_BASE_URL`, or `OPENAI_API_KEY` in your shell startup script or Windows system environment, switching profiles in CC Switch will appear to do nothing because the shell variables silently override the config files.

### Step 1: Check for Conflicting Variables

**macOS / Linux (Bash / Zsh):**

```bash
env | grep -E '^(ANTHROPIC|OPENAI|GEMINI|CLAUDE)_'
```

**Windows PowerShell:**

```powershell
Get-ChildItem Env: | Where-Object { $_.Name -match '^(ANTHROPIC|OPENAI|GEMINI|CLAUDE)_' }
```

### Step 2: Remove Persistent Overrides

- **macOS / Linux**: Open `~/.zshrc`, `~/.bashrc`, `~/.bash_profile`, or `~/.profile`, remove or comment out old `export ANTHROPIC_*` and `export OPENAI_*` lines, then reload your shell (or open a new terminal window).
- **Windows**: Press `Win + S`, search for **Edit environment variables for your account** (and **Edit the system environment variables** if needed), delete stale `ANTHROPIC_*` / `OPENAI_*` entries, and restart all terminal and IDE windows.
- **IDE integrated terminals**: VS Code, Cursor, and JetBrains IDEs cache environment variables at launch. After deleting system variables, restart the IDE completely.

::: tip Rule of Thumb
Let **either** CC Switch **or** manual shell exports own a client's credentials—never both at the same time.
:::

## Backup, Export & Machine Migration

Before upgrading a client, resetting a workstation, or syncing profiles to a second laptop:

1. Open **Settings** in CC Switch and choose **Export Configuration** (or **Backup**) to save a portable snapshot of your provider profiles.
2. Store the exported file in an encrypted password manager or private local vault.
3. On a new machine, install CC Switch and choose **Import Configuration**, then click your desired ADTY AI profile to regenerate the client config files automatically.

::: danger Protect Exported Files
CC Switch exports contain your plaintext API keys (`sk-...`). **Never** commit exported backups to Git, drop them into unencrypted cloud folders shared with others, or attach them to public support threads. If a backup file is ever exposed, revoke the affected keys immediately in [https://www.adtyai.com](https://www.adtyai.com).
:::

## Network & Proxy Settings

Most users connect directly to `https://api.adtyai.com` without extra proxy configuration. If your network requires a local proxy (for example, corporate HTTP/SOCKS5 proxies or VPN split tunneling):

- Configure the proxy address inside CC Switch's network/general settings (e.g., `http://127.0.0.1:7890` or `socks5://127.0.0.1:1080`) only if your local environment cannot reach `https://api.adtyai.com` directly.
- Avoid stacking conflicting proxy tools (such as system-level TUN mode + manual `HTTPS_PROXY` variables pointing to a closed port).
- Verify direct reachability from your terminal at any time:

  ```bash
  curl -I https://api.adtyai.com
  ```

  Receiving an HTTP response header (rather than a DNS timeout or TLS handshake failure) confirms that your network path to ADTY AI is healthy.

## Next Steps

- Ready to configure Claude Code? Continue to [Claude Code Setup](/integrations/cc-switch/claude-code).
- Using OpenAI Codex tools? Jump to [Codex CLI Setup](/integrations/cc-switch/codex) or [Codex Desktop App](/integrations/cc-switch/codex-app).
- Want live quota visibility inside CC Switch? See [Usage & Balance Query](/integrations/cc-switch/usage-query).
