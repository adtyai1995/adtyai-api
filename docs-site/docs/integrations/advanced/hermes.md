# Hermes Agent

Hermes Agent is an autonomous multi-step AI agent built for research, coding, and tool-assisted workflows. You can connect Hermes Agent to **ADTY AI** through either the OpenAI-compatible interface (`https://api.adtyai.com/v1`) or a native Anthropic endpoint (`https://api.adtyai.com`).

## 1. Install Hermes Agent

Install Hermes Agent via its official installer or Python environment (Python 3.11+ recommended):

```bash
curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash
```

Reload your shell and confirm the CLI is available:

```bash
hermes --version
```

## 2. Configure Credentials and Provider

Hermes stores credentials in `~/.hermes/.env` and agent settings in `~/.hermes/config.yaml`.

### Option A: OpenAI-Compatible Setup (Recommended)

This works with GPT, DeepSeek, Gemini, and Claude models exposed through ADTY AI's OpenAI-compatible gateway.

Add your credentials to `~/.hermes/.env`:

```dotenv
OPENAI_BASE_URL="https://api.adtyai.com/v1"
OPENAI_API_KEY="<YOUR_API_KEY>"
```

Then configure `~/.hermes/config.yaml`:

```yaml
model:
  provider: custom
  base_url: "https://api.adtyai.com/v1"
  default: "gpt-5.4"
```

### Option B: Anthropic Native Setup

If you prefer calling Claude models through the native Anthropic Messages endpoint, add the following to `~/.hermes/.env`:

```dotenv
ANTHROPIC_BASE_URL="https://api.adtyai.com"
ANTHROPIC_API_KEY="<YOUR_API_KEY>"
```

And select a Claude model in `~/.hermes/config.yaml`:

```yaml
model:
  provider: anthropic
  base_url: "https://api.adtyai.com"
  default: "claude-sonnet-4-6"
```

::: tip Interactive Setup
You can also run `hermes setup` or `hermes model` to review and update the active provider and model interactively after creating `~/.hermes/.env`.
:::

## 3. Choosing the Right Model

Hermes relies heavily on iterative tool calls (shell execution, file edits, search, and memory management). Check exact active model IDs on [ADTY AI Pricing](https://www.adtyai.com/pricing) and pick models tuned for structured function calling:

- **Complex coding & multi-step planning**: Claude Sonnet / Opus series or flagship GPT-5 models.
- **High-throughput / budget-friendly automation**: `deepseek-chat` or lightweight GPT / Gemini Flash models.
- **Deep architectural reasoning**: Reasoning-capable models paired with generous request timeouts.

## 4. Tool-Calling Compatibility Tips

- **Prefer native tool-calling models**: Ensure the model ID you choose supports structured JSON tool calls and parallel tool execution; models without reliable function-calling support can cause Hermes to emit raw JSON text instead of invoking tools.
- **Watch output token limits**: Multi-file patches and complex tool arguments need room. Avoid capping `max_tokens` too low in your Hermes config.
- **Reasoning parameter hygiene**: Some strict reasoning endpoints reject custom `temperature` or `top_p` overrides. If you encounter `400 Bad Request` on a reasoning model, remove custom sampling overrides from `~/.hermes/config.yaml`.
- **Keep key and group aligned**: Bind your Hermes API key in [https://www.adtyai.com](https://www.adtyai.com) to a group that reliably serves your chosen model family. See [Channel Availability](/guides/channel-status).

## 5. Verify Your Setup

Launch Hermes and test both a normal response and a basic tool invocation:

```bash
hermes
```

Try a quick prompt inside the session:

```text
Confirm our API connection, then list the files in the current directory using your file tool.
```

If Hermes responds cleanly and executes the tool call without schema errors, your setup is complete.

## Troubleshooting

- **Requests hit the official OpenAI endpoint instead of ADTY AI**: Double-check that `OPENAI_BASE_URL="https://api.adtyai.com/v1"` is present in `~/.hermes/.env` and not overridden by another `OPENAI_BASE_URL` exported in your shell.
- **`401 Unauthorized`**: Confirm your API key in [https://www.adtyai.com](https://www.adtyai.com) is enabled and has available balance.
- **Tool call parsing failures**: Switch to a model with strong function-calling accuracy (such as Claude Sonnet or GPT-5 series) and verify no proxy is stripping SSE streaming chunks.
- **Timeout on long agent loops**: Increase the HTTP timeout in `~/.hermes/config.yaml` and review [General FAQ](/faq/).
