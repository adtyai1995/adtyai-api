# Desktop, Agents, SDKs & Cross-Protocol Overview

Beyond official CLI coding tools, **ADTY AI** connects seamlessly with autonomous coding agents, desktop AI workspaces, official language SDKs, and cross-protocol workflows. Every integration uses the same unified API gateway (`https://api.adtyai.com`) and the API keys you create in the [ADTY AI Console](https://www.adtyai.com).

## Client & Integration Directory

Use the directory below to pick the right guide and confirm the exact Base URL format your client expects.

### Coding Agents

| Client | Protocol | Base URL | Guide |
| --- | --- | --- | --- |
| **OpenCode** | OpenAI-Compatible / Anthropic | `https://api.adtyai.com/v1` | [OpenCode](/integrations/advanced/opencode) |
| **OpenClaw** | OpenAI Completions / Anthropic Messages | `https://api.adtyai.com/v1` or `https://api.adtyai.com` | [OpenClaw](/integrations/advanced/openclaw) |
| **Hermes Agent** | OpenAI-Compatible / Anthropic | `https://api.adtyai.com/v1` or `https://api.adtyai.com` | [Hermes Agent](/integrations/advanced/hermes) |

### Desktop & Workspace Apps

| Client | Protocol | Base URL | Guide |
| --- | --- | --- | --- |
| **Cherry Studio** | OpenAI / Anthropic / Gemini | `https://api.adtyai.com` | [Cherry Studio](/integrations/advanced/cherry-studio) |
| **Claude Desktop** | Anthropic Messages | `https://api.adtyai.com` | [Claude Desktop](/integrations/advanced/claude-desktop) |
| **WorkBuddy** | OpenAI-Compatible / Anthropic | `https://api.adtyai.com/v1` or `https://api.adtyai.com` | [WorkBuddy](/integrations/advanced/workbuddy) |
| **AionUI** | OpenAI / Anthropic / Gemini + Local CLIs | `https://api.adtyai.com/v1` or `https://api.adtyai.com` | [AionUI](/integrations/advanced/aionui) |
| **All API Hub** | Console Account + API Gateway | Site: `https://www.adtyai.com` / API: `https://api.adtyai.com` | [All API Hub](/integrations/advanced/all-api-hub) |

### Official SDKs

| SDK | Protocol | Base URL | Guide |
| --- | --- | --- | --- |
| **OpenAI SDK** (Python / Node.js) | Chat Completions & Responses API | `https://api.adtyai.com/v1` | [Official SDKs](/integrations/advanced/sdks) |
| **Anthropic SDK** (Python / TypeScript) | Anthropic Messages API | `https://api.adtyai.com` | [Official SDKs](/integrations/advanced/sdks) |
| **Google GenAI SDK** (Python / TypeScript) | Gemini Native API | `https://api.adtyai.com` | [Official SDKs](/integrations/advanced/sdks) |

### Cross-Protocol Routing

| Workflow | Client Protocol | Base URL | Guide |
| --- | --- | --- | --- |
| **ChatGPT / GPT via Claude Code** | Anthropic Messages → OpenAI | `https://api.adtyai.com` | [ChatGPT via Claude Code](/integrations/advanced/chatgpt-claude-code) |
| **DeepSeek via Claude Code** | Anthropic Messages → DeepSeek | `https://api.adtyai.com` | [DeepSeek via Claude Code](/integrations/advanced/deepseek-claude-code) |
| **DeepSeek via Codex CLI** | OpenAI Chat Completions / Responses → DeepSeek | `https://api.adtyai.com/v1` | [DeepSeek via Codex](/integrations/advanced/deepseek-codex) |

## Choosing the Right Endpoint: `/v1` vs. Root URL

The single most common configuration error across third-party clients is doubling or omitting `/v1` in the Base URL. Follow this rule of thumb:

| Endpoint Form | When to Use | Example Request Built by Client |
| --- | --- | --- |
| `https://api.adtyai.com/v1` | OpenAI SDKs, Codex CLI, OpenCode (`@ai-sdk/openai-compatible` and `@ai-sdk/anthropic`), Hermes OpenAI mode, and clients that append `/chat/completions`, `/responses`, or `/messages` directly | `https://api.adtyai.com/v1/chat/completions` |
| `https://api.adtyai.com` | Native Anthropic SDK, Claude Code, Claude Desktop, Google GenAI SDK, and GUI clients (such as Cherry Studio) that automatically insert `/v1` or `/v1beta` | `https://api.adtyai.com/v1/messages` or `https://api.adtyai.com/v1beta/models/...` |

::: warning Never Mix the Console Domain with the API Domain
Always send model requests to `https://api.adtyai.com`, never to `https://www.adtyai.com`. The web domain `https://www.adtyai.com` is reserved for account management, billing, key creation, and pricing lookups. See [Quick Start & Dual Domains](/guides/overview) for details.
:::

## Before You Start

1. **Create an API key**: Sign in to [https://www.adtyai.com](https://www.adtyai.com), open **API Keys**, and create a dedicated key for each client or workflow. Follow [Account Security & PAT](/guides/security) to set quota limits and keep keys scoped.
2. **Verify model IDs and groups**: Check exact model names and group availability on [ADTY AI Pricing](https://www.adtyai.com/pricing) and monitor live channel health on [Channel Availability](/guides/channel-status).
3. **Prefer one-click profile switching for local CLIs?** If you frequently alternate between providers or model groups in Claude Code, Codex, or Claude Desktop, see [CC Switch Overview](/integrations/cc-switch/).

## Related Sections

- Looking for Claude Code, Codex CLI, Kimi Code, or Grok Build? Visit [CLI & Quick Setup](/integrations/cli/).
- Need raw HTTP endpoint details, headers, or error codes? See the [API Reference](/api/).
- Running into connection, auth, or streaming issues? Check the [General FAQ](/faq/).
