# Kimi Code Setup

**Kimi Code CLI** (`kimi-cli`) is Moonshot AI's terminal coding agent with shell execution, codebase search, and MCP support. Because **ADTY AI** exposes both OpenAI-compatible and Anthropic-compatible endpoints under a single API key, you can connect Kimi Code CLI to `kimi-k2.5` or any other model enabled on your account.

::: warning Match the Base URL to Your Chosen Protocol
- **OpenAI-compatible provider**: `https://api.adtyai.com/v1`
- **Anthropic-compatible provider**: `https://api.adtyai.com` *(no `/v1`)*
- Never point Kimi Code to the web console domain `https://www.adtyai.com` (returns **HTTP 403**).
:::

## Prerequisites

1. **Python 3.12+** or **uv** (recommended for isolated CLI installation).
2. An **ADTY AI API Key** (`sk-...`) created at [https://www.adtyai.com](https://www.adtyai.com) under **API Tokens**.
3. Verify the exact live model IDs available in your token's group on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) (for example `kimi-k2.5` or other supported coding models).

## 1. Install Kimi Code CLI

### Option A: Using `uv` (Recommended)

Install `uv` first if you do not already have it:

```bash
# macOS / Linux
curl -LsSf https://astral.sh/uv/install.sh | sh

# Windows (PowerShell)
powershell -ExecutionPolicy ByPass -c "irm https://astral.sh/uv/install.ps1 | iex"
```

Then install `kimi-cli` as an isolated global tool:

```bash
uv tool install --python 3.13 kimi-cli
```

### Option B: Using `pip` / `pipx`

```bash
pipx install kimi-cli
# Or inside a dedicated virtual environment:
pip install --upgrade kimi-cli
```

Verify the command is on your `PATH`:

```bash
kimi --version
```

## 2. Configure `~/.kimi/config.toml`

Kimi Code stores provider and model definitions in:
- **macOS / Linux**: `~/.kimi/config.toml`
- **Windows**: `%USERPROFILE%\.kimi\config.toml`

### OpenAI-Compatible Configuration (Recommended for Kimi & Multi-Model Routing)

Create or update `~/.kimi/config.toml` with an **ADTY AI** provider pointing to `https://api.adtyai.com/v1`:

```toml
default_model = "adty-kimi"

[providers.adty_openai]
type = "openai_legacy"
base_url = "https://api.adtyai.com/v1"
api_key = "sk-your-api-key"

[models.adty-kimi]
provider = "adty_openai"
model = "kimi-k2.5"
max_context_size = 262144
```

::: tip Adding Extra Models
You can declare additional `[models.<alias>]` entries referencing the same `[providers.adty_openai]` block—or an Anthropic provider block below—and switch between them anytime with `kimi --model <alias>`. Always confirm exact model IDs at [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing).
:::

### Anthropic-Compatible Configuration (Alternative)

If you prefer Kimi Code to speak the Anthropic Messages protocol (for example, when calling Claude models or Anthropic-compatible routes on **ADTY AI**), point `base_url` to `https://api.adtyai.com` **without** `/v1`:

```toml
default_model = "adty-claude"

[providers.adty_anthropic]
type = "anthropic"
base_url = "https://api.adtyai.com"
api_key = "sk-your-api-key"

[models.adty-claude]
provider = "adty_anthropic"
model = "claude-sonnet-4-6"
max_context_size = 200000
```

### Environment Variable Override (Quick Testing)

For quick shell-level testing without editing `config.toml` (or when using Kimi models through Anthropic-compatible mode):

**macOS / Linux**:

```bash
export KIMI_BASE_URL="https://api.adtyai.com/v1"
export KIMI_API_KEY="sk-your-api-key"
export KIMI_MODEL_NAME="kimi-k2.5"
```

**Windows (PowerShell)**:

```powershell
$env:KIMI_BASE_URL="https://api.adtyai.com/v1"
$env:KIMI_API_KEY="sk-your-api-key"
$env:KIMI_MODEL_NAME="kimi-k2.5"
```

If you prefer launching via Claude Code / Anthropic environment variables, export `ANTHROPIC_BASE_URL="https://api.adtyai.com"` and `ANTHROPIC_AUTH_TOKEN="sk-your-api-key"`.

## 3. Quick Verification

Start Kimi Code in your workspace directory:

```bash
cd /path/to/your/project
kimi
```

Or run a single non-interactive prompt to confirm connectivity:

```bash
kimi --print -p "Reply with OK"
```

Inside an interactive session, run `/setup` or `/model` if you want to inspect active model configuration, and check **Usage Logs** in [https://www.adtyai.com](https://www.adtyai.com) to confirm traffic is billed to the right token and model.

## Troubleshooting

- **Model not found / channel unavailable**: Confirm `kimi-k2.5` (or your chosen model ID) is listed on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) and enabled for the token group you selected.
- **HTTP 403**: Verify `base_url` uses `https://api.adtyai.com` (not `https://www.adtyai.com`).
- **HTTP 404**: Check that `type = "openai_legacy"` / OpenAI providers include `/v1`, while `type = "anthropic"` does **not** include `/v1`.
- Need more help? See the [General FAQ](/faq/) or [Environment Preparation](/integrations/cli/environment).
