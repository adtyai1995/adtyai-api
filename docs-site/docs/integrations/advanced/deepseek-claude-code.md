# DeepSeek via Claude Code

You can pair **Claude Code**'s agentic coding interface with **DeepSeek** models through **ADTY AI**'s Anthropic Messages compatibility layer (`https://api.adtyai.com`). This gives you a cost-effective workflow for large codebases, refactoring, and daily engineering tasks.

## 1. Prerequisites

1. Install Claude Code (see [Claude Code Setup](/integrations/cli/claude-code)).
2. Check [ADTY AI Pricing](https://www.adtyai.com/pricing) to verify the exact DeepSeek `<model-id>` enabled in your target group (typically `deepseek-chat` or `deepseek-reasoner`, or a specific versioned ID listed on the Pricing page).
3. Create an API key in the [ADTY AI Console](https://www.adtyai.com) bound to a group that includes DeepSeek.

## 2. Configure `~/.claude/settings.json`

Edit `~/.claude/settings.json` (on Windows, `%USERPROFILE%\.claude\settings.json`) and configure the ADTY AI endpoint, key, DeepSeek model IDs, timeout, and output token cap:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>",
    "ANTHROPIC_MODEL": "deepseek-chat",
    "ANTHROPIC_SMALL_FAST_MODEL": "deepseek-chat",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-chat",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "deepseek-reasoner",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "deepseek-chat",
    "API_TIMEOUT_MS": "600000",
    "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "8192",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  }
}
```

### Key Settings Explained

| Variable | Recommended Value | Why It Matters |
| --- | --- | --- |
| `ANTHROPIC_BASE_URL` | `https://api.adtyai.com` | Native Anthropic path without `/v1` |
| `ANTHROPIC_MODEL` | `deepseek-chat` or `deepseek-reasoner` | Primary model for interactive coding sessions (confirm exact ID on Pricing) |
| `ANTHROPIC_SMALL_FAST_MODEL` | `deepseek-chat` | Prevents Claude Code's background helper calls from requesting a Claude Haiku model unavailable on a DeepSeek group |
| `API_TIMEOUT_MS` | `600000` (10 min) | Gives `deepseek-reasoner` ample time during multi-step chain-of-thought reasoning without aborting mid-stream |
| `CLAUDE_CODE_MAX_OUTPUT_TOKENS` | `8192` | Prevents `400 Bad Request` errors when Claude Code's default `max_tokens` exceeds the DeepSeek model's output ceiling |

::: tip Choosing Between `deepseek-chat` and `deepseek-reasoner`
- Use **`deepseek-chat`** as your everyday default: lower latency and consistent multi-turn tool calling for file reads, edits, and terminal commands.
- Switch to **`deepseek-reasoner`** (via `/model deepseek-reasoner`) for complex debugging or architectural planning when deep reasoning matters most. Verify on [ADTY AI Pricing](https://www.adtyai.com/pricing) whether the active reasoner revision in your group supports tool calling during reasoning turns.
:::

## 3. Verify Tool Calls

Launch Claude Code in a test workspace:

```bash
claude
```

Run two quick checks inside the session:

1. Enter `/status` and verify the base URL is `https://api.adtyai.com` and the active model is your configured DeepSeek `<model-id>`.
2. Test real tool execution with a small prompt:

   ```text
   List the files in this directory, read package.json (or README.md), and summarize the project in two sentences without modifying any files.
   ```

Confirm that Claude Code invokes its directory/file tools cleanly, renders the tool results, and finishes without malformed JSON or timeout errors.

## 4. Switching Profiles Easily

If you use native Claude models on critical tasks and DeepSeek for high-volume editing, keep both configurations as separate profiles in [CC Switch — Claude Code Setup](/integrations/cc-switch/claude-code) so you can toggle without editing `~/.claude/settings.json` by hand.

## Troubleshooting

- **`400 Bad Request` mentioning `max_tokens`**: Lower `CLAUDE_CODE_MAX_OUTPUT_TOKENS` (for example to `8192` or `4096`) to stay within the chosen DeepSeek model's maximum completion limit.
- **Errors mentioning `claude-haiku-...` not found**: Set `ANTHROPIC_SMALL_FAST_MODEL` and `ANTHROPIC_DEFAULT_HAIKU_MODEL` to `deepseek-chat` (or your enabled DeepSeek ID).
- **Stream aborts during long reasoning**: Increase `API_TIMEOUT_MS` to `"600000"` and confirm no local proxy enforces a short 60-second idle timeout.
- **More diagnostics**: Check [Channel Availability](/guides/channel-status) and the [Claude Code FAQ](/faq/claude-code).
