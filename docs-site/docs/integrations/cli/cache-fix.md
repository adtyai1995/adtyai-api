# Claude Code Prompt Cache Hit Rate Diagnostics & Fix

**Anthropic Prompt Caching** is the single largest lever for lowering cost and speeding up time-to-first-token (TTFT) in long **Claude Code** sessions. Because Claude Code sends a large prefix on every turn—system instructions, tool schemas, MCP definitions, `CLAUDE.md`, and conversation history—a healthy session should serve **80%–95%+ of input tokens from cache** (`cache_read_input_tokens`) instead of paying full input or cache-write rates every turn.

If your **ADTY AI** Usage Logs show repeatedly high `cache_creation_input_tokens` and near-zero `cache_read_input_tokens`, use this diagnostic guide to find and eliminate the root cause.

::: info Remember the API Domain Rule
Make sure Claude Code points directly to `https://api.adtyai.com` (never `https://www.adtyai.com`, which returns HTTP 403, and avoid unnecessary extra proxy hops in between that rewrite request payloads).
:::

## 1. Why Prompt Caching Matters

Anthropic's KV cache matches the **exact byte-for-byte prefix** of each Messages API request in order:

```text
[1. Tools / MCP schemas] → [2. System prompt & CLAUDE.md] → [3. Conversation messages up to cache breakpoint]
```

- **Cache Hit (`cache_read_input_tokens`)**: Reuses the cached KV state at a steep discount (typically ~90% cheaper than standard input tokens) and dramatically reduces TTFT on large codebases.
- **Cache Creation (`cache_creation_input_tokens`)**: Paid once when a new prefix is stored (slight write surcharge compared to base input).
- **Cache Miss Cascade**: If **any byte** changes early in the prefix (for example, a tool definition order changes, a timestamp is injected into the system prompt, or you switch models), every token *after* that byte misses the cache and triggers a brand-new cache write.

## 2. How to Inspect Cache Metrics in ADTY AI Usage Logs

1. Sign in to the console at [https://www.adtyai.com](https://www.adtyai.com) and open **Usage Logs**.
2. Filter by your Claude Code API key and inspect a sequence of 5–10 consecutive turns from the same session.
3. Look at the token breakdown for each turn:
   - **Turn 1 (session start)**: Expect high `cache_creation` and `0` `cache_read`. This is normal warm-up.
   - **Turns 2–N (within 5 minutes of the previous turn)**: Expect **large `cache_read`** (tens of thousands of tokens) and **small `cache_creation`** (only the delta from the latest tool result or user message).
   - **Warning sign**: If Turn 2, Turn 3, and Turn 4 each write 30,000–100,000 `cache_creation` tokens while `cache_read` stays `0` or tiny, your cache prefix is being invalidated on every turn.

You can calculate effective cache reuse roughly as:

```text
Cache Hit Ratio = cache_read / (input_tokens + cache_creation + cache_read)
```

In an active multi-turn coding session, a healthy ratio is typically **> 80%**.

## 3. Common Causes of Prompt Cache Misses

### Cause 1: TTL Expiry After 5 Minutes of Idle Time
Standard Anthropic ephemeral prompt cache entries expire after **5 minutes** of inactivity.
- **Symptom**: Cache hits stay high while actively iterating, then drop to `0` right after you step away for 6+ minutes to test or review code.
- **Fix**: This is normal upstream TTL behavior. Batch related follow-up questions while the cache is warm, or start a fresh focused session with `/clear` if you are moving to a new task after a long pause.

### Cause 2: Dynamic System Prompts, Unstable Tool Ordering, or Noisy MCP Servers
Because tools and system instructions sit at the **very beginning** of the cached prefix, any change there invalidates the entire conversation cache.
- **Common culprits**:
  - MCP servers that register tools in non-deterministic order or toggle tools on/off mid-session.
  - Custom hooks, wrappers, or third-party local proxies that inject timestamps, random request IDs, or dynamic headers/system blocks into the payload.
  - Editing `CLAUDE.md` repeatedly in the middle of an active session.
- **Fix**:
  - Keep `CLAUDE.md` concise and stable during active coding sessions (edit it between tasks, not every turn).
  - Disable unused MCP servers (`claude mcp list`) so tool schemas remain compact and deterministic.
  - Point Claude Code **directly** to `https://api.adtyai.com` instead of chaining local rewrite proxies that alter `system`, `tools`, or `cache_control` blocks.

### Cause 3: Conflicting `ANTHROPIC_BASE_URL` / Environment Settings
Having stale environment variables in `~/.zshrc`, Windows User Env, `~/.claude/settings.json`, or a project-level `.claude/settings.local.json` can cause CLI runs, subagents, or IDE tabs to route through inconsistent settings or trigger retries/fallbacks.
- **Fix**: Audit all configuration sources and consolidate into a single clean `~/.claude/settings.json`:

```bash
# macOS / Linux: check for conflicting shell exports
env | grep -iE "ANTHROPIC|CLAUDE"
```

```powershell
# Windows PowerShell: inspect current env vars
Get-ChildItem Env: | Where-Object { $_.Name -match "ANTHROPIC|CLAUDE" }
```

Ensure `ANTHROPIC_BASE_URL` is uniformly `https://api.adtyai.com` (no trailing `/v1`) and `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` is set to `"1"` if you want minimal background noise.

### Cause 4: Switching Models Mid-Session
Prompt caches are strictly scoped to the **exact model ID** (for example, `claude-sonnet-4-6` cannot read cache created by `claude-opus-4-6`).
- **Symptom**: Every time you run `/model` to flip between Opus and Sonnet in the same long thread, the next turn pays a full cache creation penalty for the entire conversation history.
- **Fix**: Pick one model for the current task before accumulating deep context. If you want a second opinion from another model, use `/compact` or open a separate targeted session.

### Cause 5: Oversized `CLAUDE.md` or Bloated Context History
Very large `CLAUDE.md` files (or pasting huge log files early in a thread) amplify the cost of *every* cache miss and cause frequent auto-compactions, which rewrite the conversation prefix and reset the cache.
- **Fix**:
  - Keep root `CLAUDE.md` under ~150–200 lines of high-signal rules; link to module-specific docs only when needed.
  - Run `/clear` between unrelated tasks instead of dragging one 150k-token thread across multiple features.
  - Use `/compact` proactively when a task finishes a milestone rather than waiting for emergency auto-compaction mid-edit.

### Cause 6: Repeated Session Restarts or Parallel Process Churn
Closing and relaunching `claude` without resuming (`claude --continue` / `claude --resume`) creates a new session context, and running rapid one-off `claude -p` scripts from cold shells cannot reuse previous conversation history.
- **Fix**: Resume active threads with `claude -c` (`--continue`) when re-entering within the 5-minute TTL window.

### Cause 7: Token Group & Sticky Upstream Routing
Prompt caches live on the upstream provider backend. If a token group routes across heterogeneous upstreams or experiences upstream failover during high load, a request landing on a different backend naturally warms a fresh cache.
- **Fix**: Check [Channel Availability](/guides/channel-status) and select a dedicated, stable Claude group matching your workload (for example, consistent native Claude channels rather than mixing incompatible providers). Keep a dedicated API key per tool so you can observe cache metrics cleanly in Usage Logs.

## 4. Recommended Stable `~/.claude/settings.json`

Use a minimal, deterministic configuration that avoids conflicting env overrides and keeps long turns from timing out and retrying mid-stream:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-your-api-key",
    "API_TIMEOUT_MS": "600000",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  }
}
```

## 5. Step-by-Step Verification Test

Verify your cache hit rate in under 60 seconds:

1. Close conflicting proxies and remove duplicate shell env vars so only `~/.claude/settings.json` sets `ANTHROPIC_BASE_URL="https://api.adtyai.com"`.
2. Open a terminal in a small project and run `claude`.
3. Send **Turn 1**: `"Read README.md and summarize it in 2 bullets."`
4. Within 30 seconds, send **Turn 2**: `"Now list 3 concrete improvements based only on what you just read."`
5. Within 30 seconds, send **Turn 3**: `"Turn improvement #1 into a concise checklist."`
6. Open **Usage Logs** at [https://www.adtyai.com](https://www.adtyai.com):
   - Turn 1 should show cache creation.
   - Turns 2 and 3 should show **high `cache_read`** covering the system prompt, tools, and earlier turns, with only a small delta in `cache_creation`.

If Turns 2 and 3 still show `0` cache reads even with a clean configuration, grab the **Request IDs** from Usage Logs and open a ticket via the [Support Center](/guides/support) so we can inspect the exact channel trace.

## Related Docs

- [Claude Code Setup](/integrations/cli/claude-code)
- [Claude Code FAQ](/faq/claude-code)
- [Channel Availability](/guides/channel-status)
