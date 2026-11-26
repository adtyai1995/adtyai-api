# Codex CLI FAQ

Troubleshooting tips for OpenAI's **Codex CLI** connected to ADTY AI. For full installation and config templates, see the [Codex CLI Integration Guide](/integrations/cli/codex).

## 1. Missing or Wrong `wire_api` Setting

Current Codex CLI releases and GPT-5 family models require the OpenAI **Responses API** (`POST /v1/responses`), not the legacy Chat Completions wire format.

Always include `wire_api = "responses"` in your provider block inside `~/.codex/config.toml`:

```toml
model_provider = "adty"
model = "gpt-5.4"

[model_providers.adty]
name = "ADTY"
base_url = "https://api.adtyai.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

If `wire_api` is omitted or set to `"chat"`, you may see `400`/`404` errors, missing reasoning summaries, or broken tool execution.

## 2. `base_url` Must End With `/v1`

Unlike Claude Code (which forbids `/v1`), Codex CLI expects `base_url` to include `/v1`:

- **Correct**: `base_url = "https://api.adtyai.com/v1"`
- **Wrong**: `base_url = "https://api.adtyai.com"` → requests hit `/responses` instead of `/v1/responses` (`404 Not Found`)
- **Wrong**: `base_url = "https://www.adtyai.com/v1"` → rejected with `403 api_domain_required`

## 3. `auth.json` vs. `OPENAI_API_KEY`

When `requires_openai_auth = true` is set in `config.toml`, Codex reads credentials from `~/.codex/auth.json` (on Windows: `$HOME\.codex\auth.json`):

```json
{
  "OPENAI_API_KEY": "<YOUR_API_KEY>"
}
```

Common authentication pitfalls:

- **Invalid JSON syntax**: Trailing commas or smart quotes in `auth.json` silently break parsing. Validate with `jq` or PowerShell `Get-Content $HOME\.codex\auth.json | ConvertFrom-Json`.
- **Stale environment variable**: An old `OPENAI_API_KEY` exported in your shell can conflict with or mask your intended credential. Check `env | grep OPENAI` (macOS/Linux) or `Get-ChildItem Env:OPENAI*` (PowerShell).
- **Cached ChatGPT OAuth tokens**: If you previously signed in with `codex login`, old OAuth fields inside `auth.json` may take precedence. Back up `auth.json` and replace it with a clean file containing only `"OPENAI_API_KEY"`.

## 4. Model Name Mismatches

If Codex returns `404` or `403` citing an unsupported or unauthorized model:

1. Check the `model = "..."` field in `~/.codex/config.toml` and any `--model` CLI flag you passed.
2. Verify the exact model IDs enabled for your API key's concrete model group by calling `GET https://api.adtyai.com/v1/models` (see [API Reference](/api/)) or inspecting the **Pricing** page in the console.
3. Make sure your API key does not have a model allowlist excluding the selected model.

## 5. Stream Disconnects & Long Agent Turns

During multi-file edits or heavy reasoning turns, long-lived SSE streams can occasionally drop:

- **Check proxy/VPN stability**: Ensure your terminal proxy does not prematurely close idle HTTP/2 or SSE connections.
- **Check route status**: Inspect [Channel Availability](/guides/channel-status) to confirm the target model group shows green 24-hour heartbeats.
- **Keep sessions focused**: Very large diffs or bloated contexts increase per-turn generation time; start a fresh Codex session when switching to an unrelated task.

## 6. Sandbox & Approval Modes

Codex CLI restricts filesystem writes and shell commands by default for safety. If Codex appears "stuck" or refuses to edit files or run tests:

- Check your configured sandbox mode (`read-only`, `workspace-write`, or `danger-full-access`) and approval policy (`untrusted`, `on-failure`, `on-request`, `never`).
- Inside a Git repository, `workspace-write` with `on-request` approvals is usually the best balance between speed and safety.
- Outside a Git repository, Codex may ask for explicit confirmation (`--skip-git-repo-check`) before allowing write operations.

## 7. Windows vs. WSL Path & Line-Ending Quirks

- **Separate config directories**: Native Windows reads `C:\Users\<YourUser>\.codex\config.toml`, whereas WSL reads `/home/<youruser>/.codex/config.toml`. Editing one does not update the other.
- **UTF-8 without BOM**: On Windows, Notepad or older PowerShell `Out-File` defaults can write UTF-16 or UTF-8 BOM headers that break TOML/JSON parsers. Always save `config.toml` and `auth.json` as plain UTF-8 without BOM.
- **WSL networking**: If your proxy runs on the Windows host, `127.0.0.1` inside WSL2 may not reach it unless mirrored networking is enabled. Verify connectivity inside WSL with `curl -I https://api.adtyai.com/v1/models`.
- **Path performance**: When running Codex inside WSL, store code repositories under the Linux filesystem (`~/projects/...`) rather than `/mnt/c/...` to avoid slow filesystem watching and git status stalls.
