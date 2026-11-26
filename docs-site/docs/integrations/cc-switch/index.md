# CC Switch Overview & Installation

**CC Switch** is a cross-platform desktop application and command-line utility built to manage API provider configurations for AI coding clients—including **Claude Code**, **Codex CLI**, **Codex Desktop App**, **Claude Desktop**, and **Gemini CLI**—from a single place.

Instead of manually editing JSON, TOML, or shell profile files every time you change models, rotate keys, or move between personal and team workspaces, CC Switch writes clean client configurations for you with one click.

## Why Pair CC Switch with ADTY AI

Using CC Switch alongside **ADTY AI** streamlines everyday development workflows:

- **One-click profile switching**: Move seamlessly between personal API keys, team shared-wallet keys, or keys scoped to different model groups without hand-editing `~/.claude/settings.json` or `~/.codex/config.toml`.
- **Fewer formatting mistakes**: CC Switch validates JSON/TOML structure and keeps your API Base URLs consistent (`https://api.adtyai.com` for Anthropic/Claude vs. `https://api.adtyai.com/v1` for OpenAI/Codex).
- **Safe backups and portability**: Export your provider profiles before migrating machines or setting up a remote Linux devbox with the [CC Switch CLI](/integrations/cc-switch/cli).
- **Built-in usage visibility**: Configure live quota and remaining-balance queries against ADTY AI right inside CC Switch.

::: tip Before You Start
Make sure you have an active **ADTY AI** account at [https://www.adtyai.com](https://www.adtyai.com) and at least one API key (`sk-...`). If you are new to ADTY AI's dual-domain architecture, read [Quick Start & Dual Domains](/guides/overview) first.
:::

## Supported Clients

| Client | Protocol | ADTY AI Base URL | Setup Guide |
| --- | --- | --- | --- |
| Claude Code | Anthropic Messages | `https://api.adtyai.com` (no `/v1`) | [Claude Code Setup](/integrations/cc-switch/claude-code) |
| Codex CLI | OpenAI Responses | `https://api.adtyai.com/v1` | [Codex CLI Setup](/integrations/cc-switch/codex) |
| Codex Desktop App | OpenAI Responses | `https://api.adtyai.com/v1` | [Codex Desktop App](/integrations/cc-switch/codex-app) |
| Claude Desktop | Anthropic Messages | `https://api.adtyai.com` (no `/v1`) | [Claude Desktop Setup](/integrations/cc-switch/claude-desktop) |
| Gemini CLI | Gemini Native | `https://api.adtyai.com` (no `/v1`) | [Common Configuration](/integrations/cc-switch/common) |

## Downloading & Installing CC Switch

Download the latest release for your operating system from the official CC Switch GitHub Releases page (open-source desktop app by the CC Switch maintainers).

### macOS

1. Download the `.dmg` installer matching your processor:
   - **Apple Silicon (M1 / M2 / M3 / M4)**: `aarch64` / `arm64` build
   - **Intel Mac**: `x86_64` build
2. Open the `.dmg` file and drag **CC Switch** into the **Applications** folder.
3. Launch CC Switch from **Applications**.

::: info macOS Gatekeeper Prompt
If macOS reports that the app cannot be verified on first launch, open **System Settings → Privacy & Security**, scroll down, and click **Open Anyway**, or remove the quarantine attribute in Terminal:

```bash
xattr -cr "/Applications/CC Switch.app"
```
:::

### Windows

1. Download the Windows installer (`.msi` or `-setup.exe`, `x64` or `arm64` depending on your CPU).
2. Run the installer and follow the setup prompts.
3. Launch **CC Switch** from the Start Menu.

If Windows SmartScreen displays a publisher warning for a newly released build, click **More info → Run anyway** after verifying you downloaded from the official release page.

### Linux

Choose the package format that fits your distribution:

- **AppImage** (universal):

  ```bash
  chmod +x CC-Switch-*.AppImage
  ./CC-Switch-*.AppImage
  ```

- **Debian / Ubuntu (`.deb`)**:

  ```bash
  sudo apt install ./cc-switch_*.deb
  ```

- **Fedora / RHEL (`.rpm`)**:

  ```bash
  sudo dnf install ./cc-switch-*.rpm
  ```

Working on a headless server, SSH session, or WSL without a desktop GUI? Skip the desktop installer and follow the [CC Switch CLI](/integrations/cc-switch/cli) guide instead.

## Documentation Roadmap

Follow these guides in order to get the cleanest setup with ADTY AI:

1. [Common Configuration](/integrations/cc-switch/common) — Dual-domain URL rules, organizing multiple ADTY AI profiles, backups, proxy settings, and clearing conflicting shell environment variables.
2. [Claude Code Setup](/integrations/cc-switch/claude-code) — Add ADTY AI to CC Switch for Claude Code, inspect the generated `~/.claude/settings.json`, and verify in terminal.
3. [Codex CLI Setup](/integrations/cc-switch/codex) — Configure `https://api.adtyai.com/v1`, `wire_api = "responses"`, and `~/.codex/auth.json` cleanly.
4. [Codex Desktop App](/integrations/cc-switch/codex-app) — Share `~/.codex/` between Codex CLI and Codex Desktop App, bypass consumer login prompts, and fix endpoint overrides.
5. [Claude Desktop Setup](/integrations/cc-switch/claude-desktop) — Configure Claude Desktop workflows via CC Switch, handle full restarts on macOS/Windows, and solve TLS or environment conflicts.
6. [Usage & Balance Query](/integrations/cc-switch/usage-query) — Track real-time token spend in ADTY AI's console and enable in-app balance/quota checks in CC Switch.
7. [CC Switch CLI](/integrations/cc-switch/cli) — Switch providers, inspect active settings, and import/export profiles from headless servers, SSH, or WSL.

## Prefer Manual Configuration?

CC Switch is completely optional. If you prefer configuring clients directly without a switcher app, see the [Interactive Setup Wizard](/integrations/cli/) or our standalone guides for [Claude Code](/integrations/cli/claude-code), [Codex CLI](/integrations/cli/codex), and [Claude Desktop](/integrations/advanced/claude-desktop).
