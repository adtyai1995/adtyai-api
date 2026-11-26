# CC Switch CLI Tool

When you work on remote Linux servers, SSH devboxes, Docker containers, CI runners, or **WSL2** without a graphical desktop, you can manage **ADTY AI** provider profiles straight from the terminal using the **`cc-switch`** command-line utility.

## When to Use the CLI

- **Headless Linux servers & SSH sessions**: Switch Claude Code or Codex providers over SSH without manually editing `~/.claude/settings.json` or `~/.codex/config.toml`.
- **WSL2 environments**: Manage Linux-side coding tools independently from Windows desktop installations.
- **Repeatable workstation setup**: Import a saved ADTY AI profile bundle on a new dev machine in seconds.
- **Automated workflows**: Script profile changes before running batch coding tasks or benchmark suites.

## Installation

Install the `cc-switch` CLI matching your platform from the official CC Switch CLI release channel:

### Prebuilt Binary (Linux / macOS / WSL)

1. Download the archive for your OS and architecture (`linux-x86_64`, `linux-aarch64`, `darwin-arm64`, or `darwin-x86_64`) from the official releases page.
2. Extract the binary into a directory on your `PATH` (for example, `~/.local/bin` or `/usr/local/bin`):

```bash
chmod +x cc-switch
mkdir -p ~/.local/bin
mv cc-switch ~/.local/bin/cc-switch
```

3. Verify the installation:

```bash
cc-switch --version
cc-switch --help
```

### Cargo / Package Managers

If you maintain a Rust toolchain or use a supported package manager (such as Homebrew on macOS/Linux):

```bash
# Example via Homebrew (when tap/formula is enabled)
brew install cc-switch

# Or inspect available subcommands immediately after install
cc-switch --help
```

::: tip Subcommand Names Across Versions
CLI flags can evolve slightly between CC Switch CLI releases. Run `cc-switch --help` and `cc-switch <subcommand> --help` to confirm the exact flag names on your installed version.
:::

## Core CLI Workflows with ADTY AI

### 1. List Available Profiles

Inspect saved providers for each supported client:

```bash
# List Claude Code profiles
cc-switch list --app claude

# List Codex CLI profiles
cc-switch list --app codex
```

### 2. Add ADTY AI Profiles

Register profiles with the exact ADTY AI Base URL for each client protocol:

- **Claude Code** (Anthropic Messages — **no `/v1`**):
  - Base URL: `https://api.adtyai.com`
  - Auth Token: `sk-your-api-key`
- **Codex CLI** (OpenAI Responses — **must include `/v1`**):
  - Base URL: `https://api.adtyai.com/v1`
  - API Key: `sk-your-api-key`
  - Wire API: `responses`

```bash
# Interactive add wizard (recommended so your API key is not saved in shell history)
cc-switch add --app claude
cc-switch add --app codex
```

::: warning Keep API Keys Out of Shell History
Prefer interactive prompts or file import over passing raw `sk-...` keys as command-line arguments. If you ever pass a key on the command line in Bash/Zsh, prefix the line with a leading space (when `HISTCONTROL=ignorespace` is active) or clear the history entry afterward.
:::

### 3. Switch the Active Provider

Activate a saved profile by name (`<profile-name>`):

```bash
# Activate ADTY AI for Claude Code
cc-switch use --app claude "<profile-name>"

# Activate ADTY AI for Codex CLI
cc-switch use --app codex "<profile-name>"
```

### 4. Inspect Active Configuration

Confirm which profile is active and verify the rendered native config files:

```bash
# Show current active providers
cc-switch status

# Inspect generated client files directly
cat ~/.claude/settings.json
cat ~/.codex/config.toml
```

### 5. Export & Import Profiles Across Machines

Export your curated ADTY AI profiles on your main laptop and import them onto a remote server or WSL environment:

```bash
# On your source machine: export profiles to a JSON bundle
cc-switch export -o cc-switch-profiles.json

# Transfer securely over SSH
scp cc-switch-profiles.json <user>@<remote-host>:~/

# On the remote server: restrict permissions, import, then delete the transfer file
chmod 600 ~/cc-switch-profiles.json
cc-switch import ~/cc-switch-profiles.json
rm -f ~/cc-switch-profiles.json
```

Also ensure the resulting credential files stay readable only by your user account:

```bash
chmod 600 ~/.claude/settings.json ~/.codex/auth.json 2>/dev/null || true
```

## Checking for Shell Environment Variable Conflicts

Remote servers, Docker images, `tmux` sessions, and VS Code Remote containers frequently inherit exported `ANTHROPIC_*` or `OPENAI_*` variables from dotfiles or container build args. Because environment variables override config files written by `cc-switch`, always run this quick check if switching profiles seems to have no effect:

```bash
env | grep -E '^(ANTHROPIC|OPENAI|GEMINI|CLAUDE)_'
```

If any variables appear:

1. Search where they are defined:

   ```bash
   grep -rnE '(ANTHROPIC|OPENAI|GEMINI)_(BASE_URL|API_KEY|AUTH_TOKEN)' \
     ~/.bashrc ~/.zshrc ~/.profile ~/.bash_profile /etc/environment 2>/dev/null
   ```

2. Remove or comment out the conflicting lines.
3. For the current session, clear them immediately with `unset`:

   ```bash
   unset ANTHROPIC_BASE_URL ANTHROPIC_AUTH_TOKEN ANTHROPIC_API_KEY
   unset OPENAI_BASE_URL OPENAI_API_KEY
   ```

4. If you work inside `tmux` or `screen`, remember that existing panes retain old environment variables until you open a fresh session or update the `tmux` global environment.

## Verifying Connectivity from Headless Hosts

After activating your ADTY AI profile via `cc-switch`:

```bash
# Quick network check against the ADTY AI API gateway
curl -I https://api.adtyai.com

# Test Claude Code or Codex CLI
claude --version
codex --version
```

Then run a short prompt and confirm the call shows up in [https://www.adtyai.com/logs](https://www.adtyai.com/logs).

## Related Guides

- [Common Configuration](/integrations/cc-switch/common) — URL conventions, proxy settings, and multi-profile hygiene.
- [Claude Code Setup](/integrations/cc-switch/claude-code) — Detailed Claude Code `settings.json` layout.
- [Codex CLI Setup](/integrations/cc-switch/codex) — Detailed Codex `config.toml` and `auth.json` layout.
- [Usage & Balance Query](/integrations/cc-switch/usage-query) — Checking remaining quota via API and console.
