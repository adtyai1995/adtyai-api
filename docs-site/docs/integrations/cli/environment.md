# Environment Preparation

Before installing terminal coding agents such as [Claude Code](/integrations/cli/claude-code), [Codex CLI](/integrations/cli/codex), [Kimi Code](/integrations/cli/kimi-code), or [Grok Build CLI](/integrations/cli/grok-build), verify that your local toolchain, shell `PATH`, and network connectivity to **ADTY AI** are properly configured.

::: warning API Domain vs. Web Console Domain
When running connectivity tests or setting environment variables, always target the API domain `https://api.adtyai.com`. Sending API requests to the web console domain `https://www.adtyai.com` returns **HTTP 403**.
:::

## 1. Install Node.js LTS (v20 or Newer)

Most modern AI CLI tools are distributed via npm and require **Node.js 20 LTS** (or Node.js 22 LTS). Check your current version first:

```bash
node -v
npm -v
```

If `node` is missing or older than `v20.0.0`, install a current LTS build:

### macOS

Using Homebrew or `nvm`:

```bash
# Option A: Homebrew
brew install node@22

# Option B: nvm (recommended if you manage multiple Node versions)
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash
nvm install --lts
nvm use --lts
```

### Linux (Ubuntu / Debian)

```bash
# Option A: NodeSource LTS repository
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt-get install -y nodejs

# Option B: nvm (no root required for global npm installs)
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash
nvm install --lts
```

### Windows

Install via `winget` in PowerShell, or download the official `.msi` LTS installer from [nodejs.org](https://nodejs.org/):

```powershell
winget install OpenJS.NodeJS.LTS
```

Restart your terminal after installation so the updated `PATH` takes effect.

## 2. Verify Global Package Manager `PATH`

If installing a CLI with `npm install -g`, `pnpm add -g`, or `bun add -g` succeeds but running the binary reports `command not found`, your shell `PATH` is missing the package manager's global `bin` directory.

Check where your package manager puts global executables:

```bash
# npm global prefix (binaries live under <prefix>/bin on macOS/Linux, or <prefix> on Windows)
npm prefix -g

# pnpm global bin directory
pnpm bin -g

# bun global bin directory
bun pm bin -g
```

### Fixing `PATH` on macOS / Linux

If you installed Node via system packages and want to avoid `sudo npm install -g`, configure a user-writable npm prefix and export it in `~/.zshrc` (macOS default) or `~/.bashrc` (Linux):

```bash
mkdir -p ~/.npm-global
npm config set prefix "$HOME/.npm-global"
```

Add the bin path to your shell profile:

```bash
export PATH="$HOME/.npm-global/bin:$PATH"
```

Reload your profile:

```bash
source ~/.zshrc   # or: source ~/.bashrc
```

### Fixing `PATH` on Windows

Ensure `%APPDATA%\npm` (and `%USERPROFILE%\.bun\bin` if using Bun) is present in your User `Path` environment variable. In PowerShell:

```powershell
$npmBin = (npm prefix -g).Trim()
$currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($currentPath -notlike "*$npmBin*") {
    [Environment]::SetEnvironmentVariable("Path", "$currentPath;$npmBin", "User")
}
```

Open a new PowerShell window to pick up the change.

## 3. Windows-Specific Prerequisites

### Install Git and Git Bash

Tools like Claude Code rely on Git and a POSIX-compatible shell (Git Bash) on Windows for file diffs, repository inspection, and bash tool execution.

```powershell
winget install Git.Git
```

Verify both `git` and `bash.exe` are available:

```powershell
git --version
Test-Path "C:\Program Files\Git\bin\bash.exe"
```

If Git is installed in a custom location and Claude Code cannot find `bash.exe`, set `CLAUDE_CODE_GIT_BASH_PATH` to the exact path of `bash.exe` (for example, `D:\Git\bin\bash.exe`).

### PowerShell Execution Policy

By default, Windows PowerShell may block `.ps1` shim scripts created by `npm install -g` (such as `claude.ps1` or `codex.ps1`) with an error like *running scripts is disabled on this system*.

Allow locally created and signed scripts for your user account only:

```powershell
Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned
```

Confirm the effective policy:

```powershell
Get-ExecutionPolicy -List
```

## 4. Persistent Shell Environment Variables

Exporting variables directly in a terminal session only lasts until that window closes. To persist your **ADTY AI** credentials across sessions:

### macOS (`~/.zshrc`) & Linux (`~/.bashrc`)

Append your exports to the end of `~/.zshrc` (Zsh) or `~/.bashrc` (Bash):

```bash
# ADTY AI credentials (example for Anthropic-protocol CLI tools)
export ANTHROPIC_BASE_URL="https://api.adtyai.com"
export ANTHROPIC_AUTH_TOKEN="sk-your-api-key"
```

Apply immediately without restarting the terminal:

```bash
source ~/.zshrc   # Bash users: source ~/.bashrc
```

### Windows (PowerShell User Environment Variables)

Use `[Environment]::SetEnvironmentVariable` with `"User"` scope so variables survive terminal restarts and are inherited by VS Code and JetBrains IDEs:

```powershell
[Environment]::SetEnvironmentVariable("ANTHROPIC_BASE_URL", "https://api.adtyai.com", "User")
[Environment]::SetEnvironmentVariable("ANTHROPIC_AUTH_TOKEN", "sk-your-api-key", "User")
```

To verify what is currently stored at the User level:

```powershell
[Environment]::GetEnvironmentVariable("ANTHROPIC_BASE_URL", "User")
```

::: tip Restart Open Terminals & IDEs
On Windows, changing User environment variables does not retroactively update already-open PowerShell tabs or editor processes. Close and reopen your terminal (or IDE) after calling `SetEnvironmentVariable`.
:::

## 5. Verify Connectivity to ADTY AI

Before launching any CLI client, confirm your machine can reach `https://api.adtyai.com/v1/models` and authenticate with your API key (create one at [https://www.adtyai.com](https://www.adtyai.com) under **API Tokens**).

### macOS / Linux (`curl`)

```bash
curl -sS https://api.adtyai.com/v1/models \
  -H "Authorization: Bearer sk-your-api-key" | head -c 500
```

### Windows (PowerShell)

```powershell
$headers = @{ Authorization = "Bearer sk-your-api-key" }
(Invoke-RestMethod -Uri "https://api.adtyai.com/v1/models" -Headers $headers).data | Select-Object -First 10 id
```

### Interpreting the Response

- **HTTP 200 with a JSON list of model IDs**: Your network path, TLS certificates, and API key are working properly.
- **HTTP 401 Unauthorized**: Double-check that you copied the complete `sk-...` key with no leading/trailing spaces or quotes, and that the token is enabled in [https://www.adtyai.com](https://www.adtyai.com).
- **HTTP 403 Forbidden**: Confirm the URL starts with `https://api.adtyai.com`, **not** `https://www.adtyai.com`.
- **Connection timeout / TLS error**: Check corporate proxies (`HTTP_PROXY` / `HTTPS_PROXY`), VPN routing, or local firewall rules.

## Next Steps

- [Claude Code Setup](/integrations/cli/claude-code)
- [Codex CLI Setup](/integrations/cli/codex)
- [Kimi Code Setup](/integrations/cli/kimi-code)
- [Grok Build CLI Setup](/integrations/cli/grok-build)
- [Account Security & PAT](/guides/security)
