# OpenClaw

OpenClaw is an extensible local AI agent platform that supports custom model providers via `~/.openclaw/openclaw.json`. You can wire **ADTY AI** directly into OpenClaw using either the OpenAI Completions protocol (`openai-completions`) or the Anthropic Messages protocol (`anthropic-messages`).

## 1. Install OpenClaw

Ensure Node.js 22+ is installed (see [Environment Preparation](/integrations/cli/environment) if needed), then install OpenClaw globally:

```bash
npm install -g openclaw@latest
openclaw --version
```

## 2. Configure `~/.openclaw/openclaw.json`

Create or edit `~/.openclaw/openclaw.json` (on Windows, `%USERPROFILE%\.openclaw\openclaw.json`) and declare a custom provider under `models.providers`.

### Option A: OpenAI-Compatible Provider (`openai-completions`)

Use `https://api.adtyai.com/v1` with `api: "openai-completions"` for GPT, DeepSeek, Gemini, and general OpenAI-compatible models:

```json
{
  "models": {
    "mode": "merge",
    "providers": {
      "gateway": {
        "baseUrl": "https://api.adtyai.com/v1",
        "apiKey": "<YOUR_API_KEY>",
        "api": "openai-completions",
        "models": [
          {
            "id": "gpt-5.4",
            "name": "GPT-5.4",
            "reasoning": true,
            "input": ["text", "image"],
            "contextWindow": 272000,
            "maxTokens": 32768
          },
          {
            "id": "deepseek-chat",
            "name": "DeepSeek Chat",
            "reasoning": false,
            "input": ["text"],
            "contextWindow": 128000,
            "maxTokens": 8192
          }
        ]
      }
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "gateway/gpt-5.4",
        "fallbacks": ["gateway/deepseek-chat"]
      }
    }
  }
}
```

### Option B: Anthropic Messages Provider (`anthropic-messages`)

To use native Anthropic Messages routing for Claude models, add a provider with `api: "anthropic-messages"`:

```json
{
  "models": {
    "mode": "merge",
    "providers": {
      "gateway-claude": {
        "baseUrl": "https://api.adtyai.com/v1",
        "apiKey": "<YOUR_API_KEY>",
        "api": "anthropic-messages",
        "models": [
          {
            "id": "claude-sonnet-4-6",
            "name": "Claude Sonnet 4.6",
            "reasoning": true,
            "input": ["text", "image"],
            "contextWindow": 200000,
            "maxTokens": 16384
          },
          {
            "id": "claude-opus-4-6",
            "name": "Claude Opus 4.6",
            "reasoning": true,
            "input": ["text", "image"],
            "contextWindow": 200000,
            "maxTokens": 16384
          }
        ]
      }
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "gateway-claude/claude-sonnet-4-6"
      }
    }
  }
}
```

::: info Path Note for `anthropic-messages`
Depending on your OpenClaw build's underlying Anthropic HTTP client, `anthropic-messages` may append `/messages` directly (requiring `https://api.adtyai.com/v1`) or `/v1/messages` (requiring `https://api.adtyai.com`). If a test call returns `404 Not Found`, switch `baseUrl` between `https://api.adtyai.com/v1` and `https://api.adtyai.com` and re-test.
:::

::: tip Keeping Keys Out of Plain Config Files
OpenClaw supports environment variable references (for example `"${GATEWAY_API_KEY}"`) in `apiKey` when loaded via `~/.openclaw/.env` or your shell environment. Check [Account Security & PAT](/guides/security) for recommended key scoping practices.
:::

## 3. Model List Declaration & Default Selection

- **Exact IDs matter**: Every `id` inside the `models` array must match an active model ID available to your API key's group on [ADTY AI Pricing](https://www.adtyai.com/pricing).
- **Provider prefix**: When selecting a default model in `agents.defaults.model.primary` or switching via CLI, prefix the model ID with your provider name (`gateway/<model-id>` or `gateway-claude/<model-id>`).
- **Switching from CLI**:

  ```bash
  openclaw models list
  openclaw models set gateway/gpt-5.4
  ```

## 4. Test a Conversation

Validate your configuration and run a local test turn:

```bash
openclaw doctor
openclaw agent --message "Hello! Reply with a short sentence confirming we are connected."
```

Or open the interactive terminal interface:

```bash
openclaw tui
```

If OpenClaw runs as a background gateway service on your machine, restart the gateway after editing `~/.openclaw/openclaw.json` so the new provider settings take effect.

## Troubleshooting

- **Provider not recognized**: Ensure `models.mode` is set to `"merge"` and your JSON syntax has no trailing commas.
- **Repeated `401` errors**: Confirm the API key in [https://www.adtyai.com](https://www.adtyai.com) is active, has remaining balance, and has no leading or trailing spaces.
- **Model unavailable in group**: Verify your key's assigned group supports the declared model IDs on [Channel Availability](/guides/channel-status) and [ADTY AI Pricing](https://www.adtyai.com/pricing).
- **Further diagnostics**: See the [API Reference](/api/) and [General FAQ](/faq/).
