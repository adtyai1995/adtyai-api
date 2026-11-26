# Configuring Claude Code in CC Switch

This guide walks you through adding **ADTY AI** as a **Claude Code** provider in **CC Switch**, verifying the generated `~/.claude/settings.json` file, and confirming live requests in your terminal.

## Prerequisites

- **Claude Code** installed locally (see [Environment Preparation](/integrations/cli/environment) and [Claude Code](/integrations/cli/claude-code) if you have not installed it yet).
- **CC Switch** installed (see [CC Switch Overview](/integrations/cc-switch/)).
- An **ADTY AI** API key (`sk-...`) created at [https://www.adtyai.com](https://www.adtyai.com) with access to Claude models.
- No conflicting `ANTHROPIC_*` shell environment variables (see [Common Configuration](/integrations/cc-switch/common)).

## Step 1: Add an ADTY AI Provider for Claude Code

1. Launch **CC Switch** and select the **Claude Code** tab in the top navigation bar.
2. Click **Add Provider** (`+`) and choose **Custom** (or the Anthropic-compatible template).
3. Enter the following settings:

| Field | Value | Notes |
| --- | --- | --- |
| **Provider Name** | `ADTY AI` | Or a specific label such as `ADTY AI - Claude` |
| **API Base URL** (`ANTHROPIC_BASE_URL`) | `https://api.adtyai.com` | **Do NOT add `/v1`** at the end |
| **API Key** (`ANTHROPIC_AUTH_TOKEN`) | `sk-your-api-key` | Paste your full API key from ADTY AI |
| **Default Model** (`ANTHROPIC_MODEL`) | `claude-sonnet-4-6` | Check active model IDs at [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) |
| **Fast / Small Model** (`ANTHROPIC_SMALL_FAST_MODEL`) | `claude-haiku-4-5-20251001` | Optional; used for quick background tasks |

::: warning Never Append `/v1` to the Claude Base URL
Claude Code automatically appends `/v1/messages` to `ANTHROPIC_BASE_URL`. Setting the URL to `https://api.adtyai.com/v1` produces `/v1/v1/messages` and fails with HTTP `404`. Always use `https://api.adtyai.com`.
:::

## Step 2: Review the Generated `settings.json`

CC Switch provides a JSON preview editor before saving. Your ADTY AI Claude Code profile should render cleanly to a configuration equivalent to the following:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-your-api-key",
    "ANTHROPIC_MODEL": "claude-sonnet-4-6",
    "ANTHROPIC_SMALL_FAST_MODEL": "claude-haiku-4-5-20251001",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  }
}
```

When activated, CC Switch writes this JSON to Claude Code's user configuration path:

- **macOS / Linux**: `~/.claude/settings.json`
- **Windows**: `%USERPROFILE%\.claude\settings.json`

::: tip Optional Model Overrides
You can leave `ANTHROPIC_MODEL` and `ANTHROPIC_SMALL_FAST_MODEL` unset if you prefer switching models interactively inside Claude Code using `/model`, or pin specific model IDs per CC Switch profile (for example, one profile pinned to `claude-opus-4-6` for architecture reviews and another pinned to `claude-sonnet-4-6` for daily coding).
:::

## Step 3: Activate the Profile

1. Click **Save** to store the `ADTY AI` provider in CC Switch.
2. Click **Enable** / **Switch** on the `ADTY AI` card so it becomes the active Claude Code provider.
3. Confirm the file on disk was updated:

**macOS / Linux:**

```bash
cat ~/.claude/settings.json
```

**Windows PowerShell:**

```powershell
Get-Content "$env:USERPROFILE\.claude\settings.json"
```

## Step 4: Test in Your Terminal

Open a **new** terminal window inside any project directory and start Claude Code:

```bash
claude
```

Inside the interactive session:

1. Run `/status` to verify Claude Code sees `https://api.adtyai.com` as its API base URL and reports no authentication warnings.
2. Send a short test prompt (for example: `Reply with OK and the model you are running.`).
3. Visit [https://www.adtyai.com/logs](https://www.adtyai.com/logs) to confirm the request logged under your API key with token usage details.

## Switching Between Multiple Profiles

If you maintain separate ADTY AI profiles (such as personal vs. team keys or different channel groups):

1. Click the target profile in CC Switch's **Claude Code** tab.
2. Exit any running `claude` session (`/exit`) and relaunch `claude` so the process loads the updated `~/.claude/settings.json`.

## Troubleshooting & Further Optimization

- **Claude Code still prompts for an Anthropic account login**: Check whether stale `ANTHROPIC_API_KEY` or `ANTHROPIC_BASE_URL` environment variables exist in your shell, or verify `~/.claude.json` onboarding state. See [Claude Code FAQ](/faq/claude-code).
- **HTTP `403` error**: You likely entered `https://www.adtyai.com` instead of `https://api.adtyai.com`.
- **HTTP `404` error**: Check that `ANTHROPIC_BASE_URL` does **not** end with `/v1`.
- **Low prompt cache hit rate**: Review [Claude Code Cache Fix](/integrations/cli/cache-fix) to maximize prompt caching efficiency on long coding sessions.
- **Want to route ChatGPT or DeepSeek models through Claude Code?** See [ChatGPT via Claude Code](/integrations/advanced/chatgpt-claude-code) and [DeepSeek via Claude Code](/integrations/advanced/deepseek-claude-code).
