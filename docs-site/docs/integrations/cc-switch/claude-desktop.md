# Configuring Claude Desktop in CC Switch

When you use **Claude Desktop** for local coding workflows (such as its integrated **Claude Code** sessions or local desktop agent runs), **CC Switch** lets you manage the underlying **ADTY AI** provider profile cleanly—without juggling environment scripts or manually editing configuration JSON files.

::: info Scope of Desktop Configuration
Claude Desktop's hosted web-chat tab uses your consumer account directly, whereas local Claude Code / desktop coding sessions rely on local Anthropic environment configuration (`~/.claude/settings.json`). CC Switch manages that local provider layer so every Claude Code session launched from your terminal or desktop environment routes cleanly through `https://api.adtyai.com`. For standalone desktop integration patterns (including MCP and proxy notes), also see [Claude Desktop](/integrations/advanced/claude-desktop).
:::

## Prerequisites

- **Claude Desktop** and **CC Switch** installed on macOS or Windows.
- An **ADTY AI** API key (`sk-...`) from [https://www.adtyai.com](https://www.adtyai.com) with Claude models enabled.
- Conflicting system environment variables cleared (see [Common Configuration](/integrations/cc-switch/common)).

## Step 1: Create the ADTY AI Profile in CC Switch

1. Open **CC Switch** and select the **Claude Code** provider view.
2. Add or edit your **ADTY AI** profile with the exact Anthropic-compatible parameters:

| Field | Value | Notes |
| --- | --- | --- |
| **Provider Name** | `ADTY AI` | E.g., `ADTY AI - Desktop` |
| **API Base URL** | `https://api.adtyai.com` | **No `/v1` suffix**; never use `https://www.adtyai.com` |
| **API Key** (`ANTHROPIC_AUTH_TOKEN`) | `sk-your-api-key` | Paste your API key from ADTY AI |
| **Default Model** | `claude-sonnet-4-6` | Check active IDs on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) |

3. Review the JSON configuration preview:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-your-api-key",
    "ANTHROPIC_MODEL": "claude-sonnet-4-6",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  }
}
```

4. Save and click **Enable** / **Switch** so CC Switch writes the active settings to `~/.claude/settings.json` (or `%USERPROFILE%\.claude\settings.json` on Windows).

## Step 2: Fully Restart Claude Desktop

Desktop Electron apps read local environment and settings files when spawning new worker processes. After activating or switching a profile in CC Switch, perform a **complete** restart of Claude Desktop:

### macOS

1. Focus **Claude Desktop** and press `Cmd + Q` (clicking the red close button only hides the window).
2. Check the macOS menu bar at the top right; if the Claude icon is still running, click it and choose **Quit**.
3. Reopen **Claude Desktop** from **Applications** or Spotlight.

### Windows

1. Close the Claude Desktop window.
2. Look in the Windows **System Tray** (bottom-right taskbar overflow area), right-click the **Claude** icon, and choose **Quit**.
3. If needed, open **Task Manager** and verify no `Claude.exe` processes remain before launching Claude Desktop again.

## Step 3: Verify Clean Routing

1. Start a new local coding session in Claude Desktop (or open a fresh session after restart).
2. Send a lightweight prompt to confirm a normal reply.
3. Open [https://www.adtyai.com/logs](https://www.adtyai.com/logs) and verify the request appears with your expected API key and model name.

## Switching Profiles Cleanly

If you alternate between multiple ADTY AI profiles (for example, a personal workspace key and a team shared-wallet key):

1. Switch the active profile in CC Switch first.
2. Start a **new** session (or fully restart Claude Desktop if an existing long-lived session stays bound to the prior key).
3. Avoid editing `~/.claude/settings.json` by hand while CC Switch is open so CC Switch remains the single source of truth.

## Resolving Environment & Certificate Issues

### Conflicting System Environment Variables

GUI desktop apps inherit OS-level environment variables that can override `~/.claude/settings.json`:

- **Windows**: Open **Environment Variables** from Windows Search and remove old `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY`, or `ANTHROPIC_AUTH_TOKEN` entries from both User and System scopes, then fully restart Claude Desktop.
- **macOS**: Verify that `launchctl getenv ANTHROPIC_BASE_URL` and `launchctl getenv ANTHROPIC_AUTH_TOKEN` return empty results. Clear any old entry with `launchctl unsetenv <VAR_NAME>`.

### TLS / Certificate Errors Behind Corporate Proxies

If requests fail with `UNABLE_TO_VERIFY_LEAF_SIGNATURE`, `SELF_SIGNED_CERT_IN_CHAIN`, or TLS handshake errors:

1. Confirm your system clock and time zone are accurate.
2. If you are behind a corporate SSL-inspecting proxy, export your company's root CA bundle in PEM format and set `NODE_EXTRA_CA_CERTS` to its file path in your OS environment, then restart Claude Desktop.
3. Avoid setting `NODE_TLS_REJECT_UNAUTHORIZED=0` globally—disabling certificate verification weakens security across all Node-based desktop apps.
4. Test TLS reachability outside the app with:

   ```bash
   curl -v https://api.adtyai.com
   ```

### Still Running Into Issues?

- For Anthropic protocol errors, login loops, or model selection errors, check the [Claude Code FAQ](/faq/claude-code).
- For standalone desktop configuration details and MCP workflow notes, see [Claude Desktop](/integrations/advanced/claude-desktop).
