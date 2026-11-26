# DeepSeek via Codex CLI

You can run **DeepSeek** models (`deepseek-chat`, `deepseek-reasoner`, or exact channel-specific IDs from [ADTY AI Pricing](https://www.adtyai.com/pricing)) directly inside **Codex CLI** by pointing a custom provider in `~/.codex/config.toml` to **ADTY AI** (`https://api.adtyai.com/v1`).

## 1. Prerequisites

1. Install Codex CLI (see [Codex CLI Setup](/integrations/cli/codex)).
2. Confirm the exact DeepSeek `<model-id>` supported by your API key's group on [ADTY AI Pricing](https://www.adtyai.com/pricing).
3. Create an API key in the [ADTY AI Console](https://www.adtyai.com).

## 2. Configure `~/.codex/config.toml`

Create or edit `~/.codex/config.toml` (on Windows, `%USERPROFILE%\.codex\config.toml`):

```toml
model_provider = "adtyai_deepseek"
model = "deepseek-chat"

[model_providers.adtyai_deepseek]
name = "ADTY AI (DeepSeek)"
base_url = "https://api.adtyai.com/v1"
wire_api = "chat"
requires_openai_auth = true
```

To use DeepSeek's reasoning model instead, set:

```toml
model = "deepseek-reasoner"
```

### Keeping GPT as Default & Adding a DeepSeek Profile

If you already use Codex CLI with GPT models, define a named profile so you can invoke DeepSeek on demand with `codex --profile deepseek`:

```toml
[model_providers.adtyai_deepseek]
name = "ADTY AI (DeepSeek)"
base_url = "https://api.adtyai.com/v1"
wire_api = "chat"
env_key = "ADTYAI_DEEPSEEK_API_KEY"

[profiles.deepseek]
model_provider = "adtyai_deepseek"
model = "deepseek-chat"
```

## 3. Choosing `wire_api = "chat"` vs. `wire_api = "responses"`

Understanding `wire_api` is essential when pairing Codex CLI with DeepSeek:

| `wire_api` Value | Endpoint Called | When to Use |
| --- | --- | --- |
| `"chat"` **(Recommended default for DeepSeek)** | `https://api.adtyai.com/v1/chat/completions` | Highest compatibility across DeepSeek channels for streaming responses and structured tool calling |
| `"responses"` | `https://api.adtyai.com/v1/responses` | Use **only** when both your Codex CLI version requires the OpenAI Responses protocol **and** your selected ADTY AI DeepSeek channel explicitly supports `/v1/responses` translation |

::: warning Watch Out for `400` / `404` / `501` on `/v1/responses`
If Codex CLI returns an error indicating that `/v1/responses` is unsupported for the selected DeepSeek channel, switch `wire_api` to `"chat"`. Conversely, if you run a newer Codex CLI build that dropped Chat Completions support in favor of Responses-only mode, verify whether your target channel supports `/v1/responses` (check [Channel Availability](/guides/channel-status)) or use a Codex CLI release that retains `wire_api = "chat"` support.
:::

## 4. Configure `~/.codex/auth.json`

When `requires_openai_auth = true` is set in `~/.codex/config.toml`, place your ADTY AI API key in `~/.codex/auth.json` (on Windows, `%USERPROFILE%\.codex\auth.json`):

```json
{
  "OPENAI_API_KEY": "<YOUR_API_KEY>"
}
```

If you used `env_key = "ADTYAI_DEEPSEEK_API_KEY"` in a named profile instead, export that variable in your shell:

::: code-group

```bash [macOS / Linux]
export ADTYAI_DEEPSEEK_API_KEY="<YOUR_API_KEY>"
```

```powershell [Windows (PowerShell)]
[Environment]::SetEnvironmentVariable("ADTYAI_DEEPSEEK_API_KEY", "<YOUR_API_KEY>", "User")
```

:::

## 5. Verify Execution

Run a quick non-interactive test first to confirm authentication and model routing:

```bash
codex exec "Reply with OK and confirm which directory you are running in."
```

Or if using the named profile:

```bash
codex --profile deepseek exec "Reply with OK and list the top-level files."
```

Once verified, launch an interactive coding session with `codex` (or `codex --profile deepseek`) and confirm file inspection and patch generation work smoothly.

## Troubleshooting

- **Requests fail with `404` or `unsupported_endpoint` on `/v1/responses`**: Set `wire_api = "chat"` in `[model_providers.adtyai_deepseek]` and re-test.
- **`401 Unauthorized`**: Confirm `~/.codex/auth.json` contains valid JSON with `"OPENAI_API_KEY"` (or your configured `env_key` is exported in the active terminal).
- **`model_not_found`**: Verify that `deepseek-chat` or `deepseek-reasoner` matches an exact model ID enabled in your API key's group on [ADTY AI Pricing](https://www.adtyai.com/pricing).
- **Prefer GUI profile switching?** Manage multiple Codex profiles cleanly via [CC Switch — Codex CLI Setup](/integrations/cc-switch/codex), or consult the [Codex FAQ](/faq/codex).
