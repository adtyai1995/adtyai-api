# Claude Desktop

Claude Desktop combines a conversational desktop interface with **Claude Code Desktop mode** and local **MCP (Model Context Protocol)** server integration. This guide explains how to connect Claude Desktop's API-driven coding workflows to **ADTY AI** (`https://api.adtyai.com`).

::: info Account Chat vs. Claude Code Desktop / API Mode
The built-in consumer chat tab in Claude Desktop authenticates against your consumer web login directly. Custom API routing (`ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN`) applies to **Claude Code Desktop mode** and local Claude Code sessions managed inside the desktop app.
:::

## Option 1: Configure via CC Switch (Recommended)

The fastest and least error-prone way to connect Claude Desktop to ADTY AI—and switch between model groups or providers without manual file edits—is **CC Switch**.

Follow the dedicated step-by-step guide in [CC Switch — Claude Desktop Setup](/integrations/cc-switch/claude-desktop).

## Option 2: Manual Configuration

If you prefer managing configuration files directly, configure both `~/.claude/settings.json` and your desktop environment variables.

### 1. Configure `~/.claude/settings.json`

Edit `~/.claude/settings.json` (on Windows, `%USERPROFILE%\.claude\settings.json`) and set `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>"
  }
}
```

::: warning Do Not Append `/v1`
Claude Desktop and Claude Code automatically append `/v1/messages` to `ANTHROPIC_BASE_URL`. Always set it to `https://api.adtyai.com` with no trailing `/v1`.
:::

### 2. Expose Environment Variables to GUI Applications

Desktop apps launched from macOS Finder, Dock, or the Windows Start Menu do not always inherit variables defined only in `~/.zshrc` or `~/.bashrc`.

::: code-group

```bash [macOS (GUI Session)]
launchctl setenv ANTHROPIC_BASE_URL "https://api.adtyai.com"
launchctl setenv ANTHROPIC_AUTH_TOKEN "<YOUR_API_KEY>"
```

```powershell [Windows (User Environment)]
[Environment]::SetEnvironmentVariable("ANTHROPIC_BASE_URL", "https://api.adtyai.com", "User")
[Environment]::SetEnvironmentVariable("ANTHROPIC_AUTH_TOKEN", "<YOUR_API_KEY>", "User")
```

:::

Fully quit Claude Desktop (not just closing the window—exit from the macOS menu bar or Windows system tray) and reopen it so the app picks up the updated environment and `~/.claude/settings.json`.

## 3. MCP Server Compatibility

Local MCP servers configured in Claude Desktop continue to work normally when routing Claude Code traffic through ADTY AI—MCP servers execute locally on your machine while model inference is served by `https://api.adtyai.com`.

Claude Desktop reads MCP definitions from:

- **macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

Example MCP server entry:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/workspace"]
    }
  }
}
```

::: tip GUI `PATH` for `npx` / `uvx`
If an MCP server starts in your terminal but fails inside Claude Desktop, provide the absolute binary path (for example `/opt/homebrew/bin/npx` or `C:\Program Files\nodejs\npx.cmd`) in `command`.
:::

## 4. Troubleshooting Proxy & TLS Issues

- **TLS / Corporate Certificate Errors (`UNABLE_TO_VERIFY_LEAF_SIGNATURE`, `CERT_HAS_EXPIRED`)**: If your network uses a corporate HTTPS inspection proxy, export `NODE_EXTRA_CA_CERTS` pointing to your organization's PEM certificate bundle and restart Claude Desktop. Do not disable TLS verification in production environments.
- **System Proxy Conflicts**: If you run a local proxy tool, confirm it is not intercepting `https://api.adtyai.com` with an invalid certificate or forcing an unreachable `HTTPS_PROXY` address. Try bypassing `api.adtyai.com` in your proxy rules to isolate the cause.
- **Auth Conflicts**: Ensure you do not have both an outdated `ANTHROPIC_API_KEY` and a different `ANTHROPIC_AUTH_TOKEN` set simultaneously across shell profiles and `~/.claude/settings.json`.
- **Prompt Cache Misses**: If you notice lower-than-expected prompt cache hits in Claude Code sessions, review [Claude Code Cache Fix](/integrations/cli/cache-fix) and the [Claude Code FAQ](/faq/claude-code).
