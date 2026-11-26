# ChatGPT / GPT via Claude Code

**ADTY AI** supports cross-protocol Anthropic Messages routing on eligible channel groups. That means you can keep **Claude Code**'s terminal workflow—file editing, bash tool execution, diff review, and slash commands—while routing inference to OpenAI GPT models through `https://api.adtyai.com`.

## 1. Prerequisites

1. Install Claude Code if you have not already (see [Claude Code Setup](/integrations/cli/claude-code)).
2. Open [ADTY AI Pricing](https://www.adtyai.com/pricing) and confirm:
   - Which exact GPT model IDs are active (for example `gpt-5.4`, `gpt-5.3-codex`, or `gpt-5-mini`).
   - That your chosen channel group enables those GPT models with Anthropic Messages compatibility.
3. Create an API key in the [ADTY AI Console](https://www.adtyai.com) bound to that group.

## 2. Configure `~/.claude/settings.json`

Edit `~/.claude/settings.json` (on Windows, `%USERPROFILE%\.claude\settings.json`) and point `ANTHROPIC_BASE_URL` to `https://api.adtyai.com` while overriding `ANTHROPIC_MODEL` and `ANTHROPIC_SMALL_FAST_MODEL`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>",
    "ANTHROPIC_MODEL": "gpt-5.4",
    "ANTHROPIC_SMALL_FAST_MODEL": "gpt-5-mini",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "gpt-5.4",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "gpt-5.4",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "gpt-5-mini"
  }
}
```

::: tip Why Set `ANTHROPIC_SMALL_FAST_MODEL` and Default Tier Overrides?
Claude Code dispatches auxiliary background tasks (summarization, commit messages, lightweight file queries) to its small/fast model slot. If you leave that slot at its default Claude Haiku ID while using a GPT-only API key group, background calls will fail with `model_not_found`. Setting `ANTHROPIC_SMALL_FAST_MODEL` (and `ANTHROPIC_DEFAULT_HAIKU_MODEL`) to an enabled GPT model prevents silent sub-request errors.
:::

### Switching Models Dynamically

You can also override the model per session or mid-session without editing `~/.claude/settings.json`:

```bash
# Launch with a specific GPT model ID
claude --model gpt-5.4
```

Or switch inside an active Claude Code session:

```text
/model gpt-5.4
```

::: tip Manage Claude & GPT Profiles Cleanly
If you switch frequently between native Claude models and GPT models inside Claude Code, use [CC Switch — Claude Code Setup](/integrations/cc-switch/claude-code) to toggle saved profiles in one click.
:::

## 3. Verify the Connection

1. Start Claude Code in any project folder:

   ```bash
   claude
   ```

2. Run `/status` to confirm the active base URL is `https://api.adtyai.com` and the active model matches your GPT `<model-id>`.
3. Ask Claude Code to read a local file (for example `Read README.md and summarize its sections in 3 bullets`) to verify both text streaming and tool calling work end to end.

## 4. Known Caveats & Differences

Because Claude Code was built for Anthropic's native Messages protocol and ADTY AI translates calls for OpenAI GPT models, keep these behavioral differences in mind:

- **Extended Thinking Parameters**: Anthropic's `thinking` budget settings do not map 1:1 to OpenAI's reasoning effort parameters. If a request fails with an unsupported parameter error, avoid forcing custom Anthropic thinking flags or use a standard GPT model ID.
- **Prompt Caching Reporting**: Anthropic uses explicit `cache_control` breakpoints, whereas OpenAI uses automatic prefix caching. Even when prefix caching saves tokens upstream, Claude Code's local cache-hit counter may display differently than on native Claude channels.
- **Anthropic-Only Beta Features**: Anthropic server-side beta tools or experimental `anthropic-beta` headers are ignored or unsupported on translated GPT routes.
- **When to Use Codex CLI Instead**: If you want OpenAI's native Responses API, reasoning effort controls, and zero protocol translation overhead, try [Codex CLI](/integrations/cli/codex).

## Troubleshooting

- **`model_not_found` or `403`**: Double-check the exact GPT `<model-id>` on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) and confirm your API key's group includes it.
- **Background compaction or title generation fails**: Make sure `ANTHROPIC_SMALL_FAST_MODEL` and `ANTHROPIC_DEFAULT_HAIKU_MODEL` point to an enabled GPT model ID.
- **More Claude Code diagnostics**: Visit [Claude Code FAQ](/faq/claude-code) and [Channel Availability](/guides/channel-status).
