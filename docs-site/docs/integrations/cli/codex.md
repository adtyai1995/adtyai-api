# Codex CLI Setup

**OpenAI Codex CLI** is OpenAI's lightweight terminal coding agent built around the OpenAI **Responses API** (`/v1/responses`). **ADTY AI** supports the Responses API natively, letting you run Codex CLI with full tool execution, streaming reasoning, and custom reasoning effort levels.

::: warning Endpoint Must Include `/v1` and Use the API Domain
For Codex CLI, set `base_url = "https://api.adtyai.com/v1"`.
- Always use `https://api.adtyai.com/v1` (with `/v1`).
- Never use the web console domain `https://www.adtyai.com`, which returns **HTTP 403**.
:::

## Prerequisites

1. **Node.js 20+ LTS** (see [Environment Preparation](/integrations/cli/environment)).
2. An **ADTY AI API Key** (`sk-...`) created at [https://www.adtyai.com](https://www.adtyai.com) under **API Tokens**, assigned to a group that supports OpenAI / Codex models. Verify live model IDs at [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing).

## 1. Install Codex CLI

Install the official package globally via npm:

```bash
npm install -g @openai/codex
```

Verify the binary is available:

```bash
codex --version
```

## 2. Create `config.toml` and `auth.json`

Codex CLI reads two files from your user `.codex` directory:

- **macOS / Linux**: `~/.codex/config.toml` and `~/.codex/auth.json`
- **Windows**: `$HOME\.codex\config.toml` and `$HOME\.codex\auth.json` (typically `C:\Users\<YourUsername>\.codex\`)

### Step A: Write `config.toml`

Create `~/.codex/config.toml` (or `$HOME\.codex\config.toml` on Windows):

```toml
model_provider = "gateway"
model = "gpt-5.4"
model_reasoning_effort = "medium"
disable_response_storage = true

[model_providers.gateway]
name = "ADTY AI"
base_url = "https://api.adtyai.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

Key options explained:

| Option | Description |
| --- | --- |
| `model_provider` | Activates the `[model_providers.gateway]` provider section below. |
| `model` | Default model ID. Confirm available OpenAI / Codex models on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing). |
| `model_reasoning_effort` | Controls reasoning depth (`"low"`, `"medium"`, `"high"`, or `"xhigh"` where supported by the model). Higher effort improves complex debugging at higher latency and token cost. |
| `disable_response_storage` | Avoids stateful server-side response storage dependencies across relay upstreams. |
| `base_url` | Must be `https://api.adtyai.com/v1`. |
| `wire_api` | `"responses"` enables the native OpenAI Responses API used by modern Codex releases. |
| `requires_openai_auth` | Tells Codex CLI to load `OPENAI_API_KEY` from `auth.json` (or the `OPENAI_API_KEY` environment variable). |

### Step B: Write `auth.json`

Create `~/.codex/auth.json` (or `$HOME\.codex\auth.json` on Windows):

```json
{
  "OPENAI_API_KEY": "sk-your-api-key"
}
```

### Quick Setup Commands

**macOS / Linux**:

```bash
mkdir -p ~/.codex

cat << 'EOF' > ~/.codex/config.toml
model_provider = "gateway"
model = "gpt-5.4"
model_reasoning_effort = "medium"
disable_response_storage = true

[model_providers.gateway]
name = "ADTY AI"
base_url = "https://api.adtyai.com/v1"
wire_api = "responses"
requires_openai_auth = true
EOF

cat << 'EOF' > ~/.codex/auth.json
{
  "OPENAI_API_KEY": "sk-your-api-key"
}
EOF

chmod 600 ~/.codex/auth.json
```

**Windows (PowerShell)**:

```powershell
$codexDir = Join-Path $HOME ".codex"
New-Item -ItemType Directory -Force -Path $codexDir | Out-Null

@'
model_provider = "gateway"
model = "gpt-5.4"
model_reasoning_effort = "medium"
disable_response_storage = true

[model_providers.gateway]
name = "ADTY AI"
base_url = "https://api.adtyai.com/v1"
wire_api = "responses"
requires_openai_auth = true
'@ | Set-Content -Path (Join-Path $codexDir "config.toml") -Encoding UTF8

@'
{
  "OPENAI_API_KEY": "sk-your-api-key"
}
'@ | Set-Content -Path (Join-Path $codexDir "auth.json") -Encoding UTF8
```

::: tip Prefer Managing Codex Visually?
You can configure and switch Codex CLI and Codex Desktop App providers with one click using [CC Switch — Codex CLI Setup](/integrations/cc-switch/codex) and [CC Switch — Codex Desktop App](/integrations/cc-switch/codex-app).
:::

## 3. Verify with `codex`

Move into a Git repository or scratch folder and test a non-interactive execution first:

```bash
cd /path/to/your/project
codex exec "Reply with OK and nothing else"
```

Then start an interactive session:

```bash
codex
```

You can also override the model or reasoning effort per invocation without editing `config.toml`:

```bash
codex -m gpt-5.4 -c model_reasoning_effort="high"
```

After running a test prompt, open **Usage Logs** at [https://www.adtyai.com](https://www.adtyai.com) to confirm requests appear under your token.

## Related Guides & Troubleshooting

- **Codex errors (401, 403, wire_api mismatch, model unavailable)**: See the [Codex FAQ](/faq/codex).
- **GUI profile management**: See [CC Switch — Codex CLI Setup](/integrations/cc-switch/codex).
- **Running DeepSeek models in Codex CLI**: See [DeepSeek via Codex](/integrations/advanced/deepseek-codex).
- **General troubleshooting**: See [General FAQ](/faq/).
