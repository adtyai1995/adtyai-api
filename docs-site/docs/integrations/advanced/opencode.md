# OpenCode

[OpenCode](https://opencode.ai) is an open-source terminal coding agent built on the Vercel AI SDK. Because it supports custom provider definitions out of the box, you can connect **ADTY AI** through either the OpenAI-compatible protocol (`@ai-sdk/openai-compatible`) or the Anthropic Messages protocol (`@ai-sdk/anthropic`), or configure both side by side.

## 1. Install OpenCode

Install OpenCode using your preferred package manager:

::: code-group

```bash [Install Script (macOS / Linux)]
curl -fsSL https://opencode.ai/install | bash
```

```bash [npm (Cross-Platform)]
npm install -g opencode-ai
```

```bash [Homebrew (macOS / Linux)]
brew install anomalyco/tap/opencode
```

:::

Confirm the installation:

```bash
opencode --version
```

## 2. Store Your API Key in an Environment Variable

Avoid hardcoding secrets directly in JSON configuration files. Create an API key in the [ADTY AI Console](https://www.adtyai.com) and export it as `ADTYAI_API_KEY`:

::: code-group

```bash [macOS / Linux (zsh / bash)]
echo 'export ADTYAI_API_KEY="<YOUR_API_KEY>"' >> ~/.zshrc
source ~/.zshrc
```

```powershell [Windows (PowerShell)]
[Environment]::SetEnvironmentVariable("ADTYAI_API_KEY", "<YOUR_API_KEY>", "User")
```

:::

Open a new terminal window after setting a persistent Windows environment variable.

## 3. Configure `~/.config/opencode/opencode.json`

Create or edit `~/.config/opencode/opencode.json` (on Windows, `%USERPROFILE%\.config\opencode\opencode.json`). OpenCode expands `{env:ADTYAI_API_KEY}` at runtime.

::: tip Both Providers Use `https://api.adtyai.com/v1` in OpenCode
Unlike Anthropic's official Python/TypeScript SDKs, Vercel's `@ai-sdk/anthropic` package appends `/messages` directly onto `baseURL`. Therefore, **both** `@ai-sdk/openai-compatible` and `@ai-sdk/anthropic` in OpenCode should use `https://api.adtyai.com/v1`.
:::

### Option A: OpenAI-Compatible Provider (`@ai-sdk/openai-compatible`)

Best for GPT, DeepSeek, Gemini, and multi-model groups exposed over OpenAI Chat Completions:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "adtyai": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "ADTY AI",
      "options": {
        "baseURL": "https://api.adtyai.com/v1",
        "apiKey": "{env:ADTYAI_API_KEY}"
      },
      "models": {
        "gpt-5.4": {
          "name": "GPT-5.4",
          "limit": {
            "context": 272000,
            "output": 32768
          }
        },
        "deepseek-chat": {
          "name": "DeepSeek Chat",
          "limit": {
            "context": 128000,
            "output": 8192
          }
        }
      }
    }
  },
  "model": "adtyai/gpt-5.4"
}
```

### Option B: Anthropic Provider (`@ai-sdk/anthropic`)

Best for Claude models when you want native Anthropic Messages features (such as prompt caching and extended thinking):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "adtyai-anthropic": {
      "npm": "@ai-sdk/anthropic",
      "name": "ADTY AI (Anthropic)",
      "options": {
        "baseURL": "https://api.adtyai.com/v1",
        "apiKey": "{env:ADTYAI_API_KEY}"
      },
      "models": {
        "claude-sonnet-4-6": {
          "name": "Claude Sonnet 4.6",
          "limit": {
            "context": 200000,
            "output": 16384
          }
        },
        "claude-opus-4-6": {
          "name": "Claude Opus 4.6",
          "limit": {
            "context": 200000,
            "output": 16384
          }
        }
      }
    }
  },
  "model": "adtyai-anthropic/claude-sonnet-4-6"
}
```

### Combining Both Providers in One File

You can define both `adtyai` and `adtyai-anthropic` under `"provider"` in the same `opencode.json` file (even pointing each to a separate key variable such as `{env:ADTYAI_OPENAI_KEY}` and `{env:ADTYAI_CLAUDE_KEY}` if your keys target different channel groups). Always verify exact model IDs on [ADTY AI Pricing](https://www.adtyai.com/pricing).

## 4. Verify with `opencode`

1. Navigate to a project directory and start OpenCode:

   ```bash
   opencode
   ```

2. Run `/models` inside the TUI and select your `adtyai/<model-id>` or `adtyai-anthropic/<model-id>` entry.
3. Alternatively, run a quick one-shot connectivity check from your shell:

   ```bash
   opencode run "Reply with a single line confirming your connection."
   ```

## Troubleshooting

- **`404 Not Found` on Anthropic provider**: Verify `baseURL` is `https://api.adtyai.com/v1` (with `/v1`), not `https://api.adtyai.com`.
- **`401 Unauthorized` or empty key**: Check that `echo $ADTYAI_API_KEY` (or `$env:ADTYAI_API_KEY` in PowerShell) outputs your full `sk-...` key in the same shell where you launch `opencode`.
- **`model_not_found`**: Confirm the model key in `models` matches an exact ID enabled for your API key's group on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing).
- **Need more help?** Check [Channel Availability](/guides/channel-status) or the [General FAQ](/faq/).
