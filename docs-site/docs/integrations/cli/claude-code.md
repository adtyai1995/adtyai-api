# Claude Code Setup

**Claude Code** is Anthropic's official agentic coding CLI and IDE extension. **ADTY AI** provides full native Anthropic Messages API compatibility—including prompt caching, streaming, tool use, and extended thinking—so Claude Code works seamlessly out of the box.

::: danger Do NOT Add `/v1` to `ANTHROPIC_BASE_URL`
Claude Code automatically appends `/v1/messages` to `ANTHROPIC_BASE_URL`.
- **Correct**: `https://api.adtyai.com`
- **Wrong**: `https://api.adtyai.com/v1` (produces `/v1/v1/messages` and fails with 404)
- **Wrong**: `https://www.adtyai.com` (web console domain returns **HTTP 403**)
:::

## Prerequisites

1. **Node.js 20+ LTS** (and Git Bash on Windows). See [Environment Preparation](/integrations/cli/environment) if you have not configured your environment yet.
2. An **ADTY AI API Key** (`sk-...`) created at [https://www.adtyai.com](https://www.adtyai.com) under **API Tokens**, assigned to a group that supports Claude models. Check available Claude models and group availability at [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) and [Channel Availability](/guides/channel-status).

## 1. Install Claude Code

Install the official npm package globally:

```bash
npm install -g @anthropic-ai/claude-code
```

Verify the installation:

```bash
claude --version
```

## 2. Configure ADTY AI Credentials

You can configure Claude Code via persistent `settings.json` (recommended because it applies consistently to CLI runs, VS Code, and JetBrains plugins) or via shell environment variables.

### Method A: Persistent `~/.claude/settings.json` (Recommended)

Create or edit the Claude Code settings file:
- **macOS / Linux**: `~/.claude/settings.json`
- **Windows**: `%USERPROFILE%\.claude\settings.json` (or `$HOME\.claude\settings.json` in PowerShell)

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-your-api-key",
    "API_TIMEOUT_MS": "600000",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  }
}
```

Key settings explained:

| Key | Purpose |
| --- | --- |
| `ANTHROPIC_BASE_URL` | Points Claude Code to **ADTY AI** (`https://api.adtyai.com`, with **no** trailing `/v1`). |
| `ANTHROPIC_AUTH_TOKEN` | Your **ADTY AI** API key (`sk-...`). |
| `API_TIMEOUT_MS` | Extends request timeout to 600,000 ms (10 minutes) so large code generation or extended-thinking turns do not abort prematurely. |
| `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` | Optional (`"1"`). Disables telemetry, error reporting, and background auto-update checks that are unnecessary when routing through an API relay. |

::: warning Avoid Conflicting Auth Variables
Do not set both `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` to different values, and remove any stale Anthropic OAuth login (`claude /logout`) if Claude Code tries to authenticate against `claude.ai` instead of **ADTY AI**.
:::

### Method B: Shell Environment Variables

If you prefer shell exports (or want to test a key temporarily):

**macOS / Linux (`~/.zshrc` or `~/.bashrc`)**:

```bash
export ANTHROPIC_BASE_URL="https://api.adtyai.com"
export ANTHROPIC_AUTH_TOKEN="sk-your-api-key"
export API_TIMEOUT_MS="600000"
```

**Windows (PowerShell — current session)**:

```powershell
$env:ANTHROPIC_BASE_URL="https://api.adtyai.com"
$env:ANTHROPIC_AUTH_TOKEN="sk-your-api-key"
$env:API_TIMEOUT_MS="600000"
```

**Windows (PowerShell — persistent for current user)**:

```powershell
[Environment]::SetEnvironmentVariable("ANTHROPIC_BASE_URL", "https://api.adtyai.com", "User")
[Environment]::SetEnvironmentVariable("ANTHROPIC_AUTH_TOKEN", "sk-your-api-key", "User")
[Environment]::SetEnvironmentVariable("API_TIMEOUT_MS", "600000", "User")
```

::: tip Prefer a GUI Profile Manager?
You can also configure and switch Claude Code profiles automatically using [CC Switch — Claude Code Setup](/integrations/cc-switch/claude-code).
:::

## 3. Selecting Claude Models (Opus / Sonnet / Haiku)

Claude Code supports switching models interactively inside a session or locking default aliases in `~/.claude/settings.json`.

- **Switch inside a session**: Run `/model` inside Claude Code and choose between Sonnet (balanced daily coding), Opus (complex architecture and deep reasoning), or Haiku (fast lightweight tasks).
- **Specify on launch**:

```bash
claude --model claude-sonnet-4-6
```

- **Pin model mappings in `~/.claude/settings.json`**:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-your-api-key",
    "API_TIMEOUT_MS": "600000",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4-6",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-6",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-haiku-4-5-20251001"
  }
}
```

Always check [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) for the exact live model IDs enabled under your token's group.

## 4. VS Code & JetBrains Extension Setup

The official **Claude Code** extensions for VS Code (as well as Cursor / Windsurf) and JetBrains IDEs read `~/.claude/settings.json` automatically.

1. Make sure `~/.claude/settings.json` contains your `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN` in the `"env"` block (Method A above).
2. Install the official **Claude Code** extension from the VS Code Marketplace or JetBrains Marketplace.
3. **Restart your IDE completely** so it reloads both `~/.claude/settings.json` and any user-level environment variables.
4. Open the Claude Code panel in your IDE. If prompted to sign in via browser OAuth, skip OAuth and verify that `~/.claude/settings.json` is valid JSON with no trailing commas.

::: info Running Claude Code in WSL or Remote SSH
When using VS Code Remote-SSH or WSL, Claude Code executes on the **remote / WSL host**. Place `~/.claude/settings.json` inside that remote/WSL user home directory rather than only on your local Windows desktop.
:::

## 5. Verification Commands

Navigate to a project directory and run a non-interactive smoke test first:

```bash
cd /path/to/your/project
claude -p "Reply with the single word: OK"
```

If that prints `OK`, launch the interactive agent and inspect active status:

```bash
claude
```

Inside the interactive prompt, run `/status` to verify:
- The active API Base URL resolves to `https://api.adtyai.com`
- The expected Claude model is selected
- Authentication is using your API token rather than a personal claude.ai subscription

You can also confirm billed requests and prompt cache hit rates directly in **Usage Logs** at [https://www.adtyai.com](https://www.adtyai.com).

## Troubleshooting & Further Reading

- **Low prompt cache hit rate or unexpected cost**: Read [Claude Code Prompt Cache Hit Rate Diagnostics & Fix](/integrations/cli/cache-fix).
- **401 / 403 / 404 / streaming errors**: Check [Claude Code FAQ](/faq/claude-code).
- **Using non-Claude models inside Claude Code**: See [ChatGPT via Claude Code](/integrations/advanced/chatgpt-claude-code) or [DeepSeek via Claude Code](/integrations/advanced/deepseek-claude-code).
