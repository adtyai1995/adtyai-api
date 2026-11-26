# Interactive Setup Wizard & CLI Overview

Connect your terminal coding agents to **ADTY AI** in under two minutes. Use the interactive generator below to pick your CLI client and operating system, preview a ready-to-paste configuration with this site's dedicated API endpoint, and jump straight into the full setup guide.

<SetupWizard />

::: warning Use the Dedicated API Domain Only
Always configure CLI tools and SDKs with the API endpoint `https://api.adtyai.com` (or `https://api.adtyai.com/v1` for OpenAI-compatible protocols). Do **not** point clients to the web console domain `https://www.adtyai.com`—requests sent to the console domain are rejected with **HTTP 403**. See [Quick Start & Dual Domains](/guides/overview) for details.
:::

## Supported CLI Clients

Different CLI tools speak different upstream wire protocols. Pay close attention to whether a client expects the root API origin (`https://api.adtyai.com`) or the `/v1` suffix (`https://api.adtyai.com/v1`):

| Client | Upstream Protocol | Base URL Format | Primary Config Target | Guide |
| --- | --- | --- | --- | --- |
| **Claude Code** | Anthropic Messages (`/v1/messages`) | `https://api.adtyai.com` *(no `/v1`)* | `ANTHROPIC_BASE_URL` / `~/.claude/settings.json` | [Claude Code Guide](/integrations/cli/claude-code) |
| **Codex CLI** | OpenAI Responses (`/v1/responses`) | `https://api.adtyai.com/v1` | `~/.codex/config.toml` & `~/.codex/auth.json` | [Codex CLI Guide](/integrations/cli/codex) |
| **Kimi Code** | Anthropic Messages or OpenAI Chat | `https://api.adtyai.com` (Anthropic) or `https://api.adtyai.com/v1` (OpenAI) | `~/.kimi/config.toml` / env vars | [Kimi Code Guide](/integrations/cli/kimi-code) |
| **Grok Build CLI** | xAI / OpenAI-compatible | `https://api.adtyai.com/v1` | `XAI_BASE_URL` / CLI config | [Grok Build CLI Guide](/integrations/cli/grok-build) |

::: tip Manage Multiple CLI Profiles Visually
If you switch frequently between multiple API keys, token groups, or CLI tools (Claude Code, Codex CLI, Codex Desktop, Claude Desktop), consider using [CC Switch](/integrations/cc-switch/) to manage provider profiles from a single GUI or CLI without hand-editing config files.
:::

## Recommended Setup Path

1. **Create an API Key**: Sign in at [https://www.adtyai.com](https://www.adtyai.com), open **API Tokens**, create a token bound to the appropriate group for your models, and copy the `sk-...` key. Review [Channel Availability](/guides/channel-status) if you are unsure which token group to select.
2. **Prepare Your Local Environment**: Verify Node.js LTS (`v20+`), shell `PATH`, Git (including Git Bash on Windows), and basic connectivity to `https://api.adtyai.com/v1/models` using the [Environment Preparation](/integrations/cli/environment) checklist.
3. **Configure Your CLI Tool**: Follow the dedicated guide for your preferred terminal agent:
   - [Claude Code Setup](/integrations/cli/claude-code)
   - [Codex CLI Setup](/integrations/cli/codex)
   - [Kimi Code Setup](/integrations/cli/kimi-code)
   - [Grok Build CLI Setup](/integrations/cli/grok-build)
4. **Optimize Cost & Latency**: If you run long Claude Code sessions and notice low prompt-cache reuse or rising token spend, walk through [Claude Code Prompt Cache Hit Rate Diagnostics & Fix](/integrations/cli/cache-fix).

## Looking for Desktop Clients, Agents, or SDKs?

- Need GUI apps, autonomous agents, or official Python/TypeScript SDKs? Browse [Desktop, Agents & SDKs](/integrations/advanced/).
- Want to route non-Claude models through Claude Code or Codex? Check [ChatGPT via Claude Code](/integrations/advanced/chatgpt-claude-code), [DeepSeek via Claude Code](/integrations/advanced/deepseek-claude-code), and [DeepSeek via Codex](/integrations/advanced/deepseek-codex).
- Hitting connection, authentication, or model errors? Visit the [General FAQ](/faq/), [Claude Code FAQ](/faq/claude-code), or [Codex FAQ](/faq/codex).
