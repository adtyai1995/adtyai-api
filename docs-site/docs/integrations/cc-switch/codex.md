# Configuring Codex CLI in CC Switch

This guide explains how to configure **OpenAI Codex CLI** to route through **ADTY AI** using **CC Switch**, including the exact `config.toml` and `auth.json` settings required by the Responses API.

## Prerequisites

- **Codex CLI** installed locally (see [Codex CLI](/integrations/cli/codex) for installation steps).
- **CC Switch** installed (see [CC Switch Overview](/integrations/cc-switch/)).
- An **ADTY AI** API key (`sk-...`) from [https://www.adtyai.com](https://www.adtyai.com) with access to Codex-compatible models.
- No conflicting `OPENAI_API_KEY` or `OPENAI_BASE_URL` variables in your shell (see [Common Configuration](/integrations/cc-switch/common)).

## Step 1: Add an ADTY AI Provider in CC Switch

1. Open **CC Switch** and switch to the **Codex** tab.
2. Click **Add Provider** (`+`) and select **Custom**.
3. Fill in the provider parameters:

| Field | Value | Notes |
| --- | --- | --- |
| **Provider Name** | `ADTY AI` | Display name in CC Switch |
| **API Base URL** (`base_url`) | `https://api.adtyai.com/v1` | **Must include `/v1`** |
| **API Key** (`OPENAI_API_KEY`) | `sk-your-api-key` | Paste your ADTY AI API key |
| **Wire API** (`wire_api`) | `responses` | Required by modern Codex CLI builds |
| **Model** (`model`) | `gpt-5.4` | Or another Codex model listed on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) |

::: warning Keep `/v1` and `wire_api = "responses"`
Unlike Claude Code, Codex CLI expects `/v1` in `base_url` (`https://api.adtyai.com/v1`) and communicates via the OpenAI Responses protocol (`wire_api = "responses"`). Omitting `/v1` or setting `wire_api = "chat"` causes endpoint or schema errors.
:::

## Step 2: Preview Generated `config.toml` and `auth.json`

CC Switch manages both Codex configuration files together. Before saving, review the generated content in CC Switch's preview pane.

### `~/.codex/config.toml`

```toml
model_provider = "adty"
model = "gpt-5.4"
model_reasoning_effort = "high"

[model_providers.adty]
name = "ADTY AI"
base_url = "https://api.adtyai.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

### `~/.codex/auth.json`

```json
{
  "OPENAI_API_KEY": "sk-your-api-key"
}
```

File locations written by CC Switch:

- **macOS / Linux**: `~/.codex/config.toml` and `~/.codex/auth.json`
- **Windows**: `%USERPROFILE%\.codex\config.toml` and `%USERPROFILE%\.codex\auth.json`

::: tip Choosing a Model
Always verify exact model names available to your API key group on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing). You can also create multiple CC Switch profiles—for example, one pinned to a flagship reasoning model and another pinned to a faster model—and toggle between them in one click.
:::

## Step 3: Activate the Profile

1. Click **Save** in CC Switch.
2. Select the `ADTY AI` profile under the **Codex** tab and click **Enable** / **Switch**.
3. Verify the files on disk:

**macOS / Linux:**

```bash
cat ~/.codex/config.toml
```

**Windows PowerShell:**

```powershell
Get-Content "$env:USERPROFILE\.codex\config.toml"
```

## Step 4: Verify Codex CLI in Terminal

Open a new terminal in your repository and launch Codex CLI:

```bash
codex
```

Or run a quick non-interactive smoke test:

```bash
codex exec "Reply with OK and confirm the current directory name."
```

If everything is configured properly:

- Codex CLI starts immediately without asking you to sign in via a browser.
- The response completes cleanly using your selected model.
- The request appears in [https://www.adtyai.com/usage-logs](https://www.adtyai.com/usage-logs) under your API key.

## Common Issues

| Symptom | Likely Cause | Fix |
| --- | --- | --- |
| `401 Unauthorized` | Wrong key, expired key, or an old `OPENAI_API_KEY` env var overriding `auth.json` | Clear shell `OPENAI_API_KEY` and re-activate your profile in CC Switch |
| `403 Forbidden` | Entered `https://www.adtyai.com/v1` instead of `https://api.adtyai.com/v1` | Switch `base_url` to the dedicated API domain `https://api.adtyai.com/v1` |
| `404 Not Found` | Missing `/v1` suffix in `base_url` | Set `base_url = "https://api.adtyai.com/v1"` |
| Model not found / group mismatch | Selected model is not enabled in the key's group | Confirm the model ID on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) |

For deeper troubleshooting—including reasoning-effort tuning and WSL path quirks—see the [Codex FAQ](/faq/codex). If you also use the graphical Codex client, continue to [Codex Desktop App](/integrations/cc-switch/codex-app).
