# Grok Build CLI Setup

**Grok Build CLI** (the terminal coding CLI for xAI Grok models) communicates over the OpenAI-compatible `/v1` API surface. By pointing `XAI_BASE_URL` (or the CLI's OpenAI-compatible provider config) to **ADTY AI**, you can run Grok coding workflows using your existing **ADTY AI** balance and API keys.

::: warning Always Include `/v1` on the API Domain
Point Grok Build CLI to `https://api.adtyai.com/v1`.
- **Correct**: `https://api.adtyai.com/v1`
- **Wrong**: `https://www.adtyai.com` (the web console domain returns **HTTP 403** for API traffic)
:::

## Prerequisites

1. **Node.js 20+ LTS** (see [Environment Preparation](/integrations/cli/environment)).
2. An **ADTY AI API Key** (`sk-...`) created at [https://www.adtyai.com](https://www.adtyai.com) under **API Tokens**, bound to a group that includes Grok models.
3. Check the exact Grok model IDs currently available on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) (for example, `grok-code-fast-1`, `grok-4`, or other live Grok listings).

## 1. Install Grok Build CLI

Install the CLI globally via npm (or the official xAI CLI installer if distributed for your platform):

```bash
npm install -g @vibe-kit/grok-cli
# Or, if using the official xAI Grok Build CLI package on your environment:
# npm install -g @xai/grok-cli
```

Confirm the command is available on your `PATH`:

```bash
grok --version
```

::: tip Verify Which Binary You Installed
Depending on the Grok CLI distribution you install, the executable may be `grok` and its config directory may be `~/.grok/`. Run `grok --help` to check the exact CLI flags (`--base-url`, `--api-key`, `--model`) supported by your installed version.
:::

## 2. Configure Environment Variables

Grok Build CLI accepts standard xAI environment variables (`XAI_BASE_URL` and `XAI_API_KEY`) as well as OpenAI-compatible overrides (`GROK_BASE_URL` / `GROK_API_KEY` depending on build).

### macOS / Linux (`~/.zshrc` or `~/.bashrc`)

```bash
export XAI_BASE_URL="https://api.adtyai.com/v1"
export XAI_API_KEY="sk-your-api-key"

# Optional fallback variables for OpenAI-compatible Grok CLI builds
export GROK_BASE_URL="https://api.adtyai.com/v1"
export GROK_API_KEY="sk-your-api-key"
```

Reload your shell profile:

```bash
source ~/.zshrc   # Bash users: source ~/.bashrc
```

### Windows (PowerShell)

**Current session only**:

```powershell
$env:XAI_BASE_URL="https://api.adtyai.com/v1"
$env:XAI_API_KEY="sk-your-api-key"
$env:GROK_BASE_URL="https://api.adtyai.com/v1"
$env:GROK_API_KEY="sk-your-api-key"
```

**Persistent for current user**:

```powershell
[Environment]::SetEnvironmentVariable("XAI_BASE_URL", "https://api.adtyai.com/v1", "User")
[Environment]::SetEnvironmentVariable("XAI_API_KEY", "sk-your-api-key", "User")
[Environment]::SetEnvironmentVariable("GROK_BASE_URL", "https://api.adtyai.com/v1", "User")
[Environment]::SetEnvironmentVariable("GROK_API_KEY", "sk-your-api-key", "User")
```

Open a new PowerShell window after setting persistent user variables.

## 3. Persistent Configuration File Setup

Instead of keeping credentials only in shell profiles, you can store a persistent settings file under `~/.grok/user-settings.json` (Windows: `%USERPROFILE%\.grok\user-settings.json`):

```json
{
  "apiKey": "sk-your-api-key",
  "baseURL": "https://api.adtyai.com/v1",
  "defaultModel": "grok-code-fast-1",
  "models": [
    "grok-code-fast-1",
    "grok-4"
  ]
}
```

Restrict file permissions on macOS / Linux so other local users cannot read your key:

```bash
chmod 600 ~/.grok/user-settings.json
```

## 4. Selecting Grok Models

Model availability and exact identifiers evolve as xAI releases new checkpoints. Always check [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) first and filter by `grok` to confirm:
- Exact model slug (e.g., `grok-code-fast-1`, `grok-4`, etc.)
- Which token group supports that model (see [Channel Availability](/guides/channel-status))

You can pass a specific model per command:

```bash
grok --base-url https://api.adtyai.com/v1 --model grok-code-fast-1
```

Or switch models interactively inside the session with `/models` (or the model flag documented in `grok --help`).

## 5. Verify Your Setup

Run a quick prompt inside a project directory to test authentication and streaming:

```bash
cd /path/to/your/project
grok -p "Reply with OK"
```

Then start an interactive coding session:

```bash
grok
```

Check **Usage Logs** at [https://www.adtyai.com](https://www.adtyai.com) to confirm requests are routed to your selected Grok model and billed to your token.

## Troubleshooting

- **HTTP 403 Forbidden**: Confirm `XAI_BASE_URL` / `baseURL` uses `https://api.adtyai.com/v1`, **not** `https://www.adtyai.com`.
- **HTTP 404 or Invalid URL**: Ensure the trailing `/v1` is present.
- **Model not found / no available channel**: Confirm the exact model name on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) and ensure your API key belongs to a group carrying that model.
- Want to use Grok in a multi-provider agent instead? Try [OpenCode](/integrations/advanced/opencode) or [Cherry Studio](/integrations/advanced/cherry-studio), or check the [General FAQ](/faq/).
