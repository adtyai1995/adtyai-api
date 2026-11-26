# 基础环境准备

在安装 [Claude Code](/zh-cn/integrations/cli/claude-code)、[Codex CLI](/zh-cn/integrations/cli/codex)、[Kimi Code](/zh-cn/integrations/cli/kimi-code) 或 [Grok Build CLI](/zh-cn/integrations/cli/grok-build) 等终端编程工具前，请先确保本地运行时、全局命令 `PATH` 以及到 **ADTY AI** 接口域名的网络连通性正常。

::: warning 区分 API 域名与网页控制台域名
进行连通性测试或配置环境变量时，接口地址必须指向 `https://api.adtyai.com`。向控制台域名 `https://www.adtyai.com` 发起 API 请求会直接返回 **HTTP 403**。
:::

## 1. 安装 Node.js LTS（v20 及以上）

主流 AI CLI 工具大多通过 npm 分发，要求本地具备 **Node.js 20 LTS**（推荐使用 22 LTS）。先检查当前环境版本：

```bash
node -v
npm -v
```

若未安装或版本低于 `v20.0.0`，请按系统安装最新 LTS 版本：

### macOS

使用 Homebrew 或 `nvm`：

```bash
# 方式一：Homebrew
brew install node@22

# 方式二：nvm（推荐，便于多版本切换）
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash
nvm install --lts
nvm use --lts
```

### Linux（Ubuntu / Debian）

```bash
# 方式一：NodeSource LTS 仓库
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt-get install -y nodejs

# 方式二：nvm（全局安装 npm 包无需 sudo）
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash
nvm install --lts
```

### Windows

在 PowerShell 中通过 `winget` 安装，或前往 [nodejs.org](https://nodejs.org/) 下载官方 `.msi` LTS 安装包：

```powershell
winget install OpenJS.NodeJS.LTS
```

安装完成后请**重新打开终端**，以加载新的环境变量。

## 2. 检查包管理器全局 `PATH`

如果执行 `npm install -g`、`pnpm add -g` 或 `bun add -g` 显示成功，但运行命令仍提示 `command not found` 或“不是内部或外部命令”，通常是因为全局可执行目录未加入 `PATH`。

查看各包管理器的全局可执行路径：

```bash
# npm 全局前缀（macOS/Linux 可执行文件位于 <prefix>/bin，Windows 位于 <prefix> 根目录）
npm prefix -g

# pnpm 全局 bin 目录
pnpm bin -g

# bun 全局 bin 目录
bun pm bin -g
```

### macOS / Linux 配置用户级 `PATH`

为了避免全局安装包时频繁使用 `sudo`，可将 npm 全局前缀切到用户目录并加入 `~/.zshrc`（macOS 默认）或 `~/.bashrc`（Linux）：

```bash
mkdir -p ~/.npm-global
npm config set prefix "$HOME/.npm-global"
```

在 Shell 配置文件末尾添加：

```bash
export PATH="$HOME/.npm-global/bin:$PATH"
```

使配置立即生效：

```bash
source ~/.zshrc   # Bash 用户执行：source ~/.bashrc
```

### Windows 检查并补齐 `PATH`

确认 `%APPDATA%\npm`（如使用 Bun 还需 `%USERPROFILE%\.bun\bin`）已存在于当前用户的 `Path` 环境变量中。可在 PowerShell 中一键检查并追加：

```powershell
$npmBin = (npm prefix -g).Trim()
$currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($currentPath -notlike "*$npmBin*") {
    [Environment]::SetEnvironmentVariable("Path", "$currentPath;$npmBin", "User")
}
```

执行后重新打开 PowerShell 窗口生效。

## 3. Windows 专项前置配置

### 安装 Git 与 Git Bash

Claude Code 等工具在 Windows 上依赖 Git 与 Git Bash 来执行 Shell 命令与代码差异检查：

```powershell
winget install Git.Git
```

验证 `git` 与 `bash.exe` 是否就绪：

```powershell
git --version
Test-Path "C:\Program Files\Git\bin\bash.exe"
```

若 Git 安装在非默认盘符且客户端提示找不到 `bash.exe`，可将环境变量 `CLAUDE_CODE_GIT_BASH_PATH` 设为实际的 `bash.exe` 绝对路径（例如 `D:\Git\bin\bash.exe`）。

### 配置 PowerShell 执行策略

Windows PowerShell 默认可能会拦截 npm 全局安装生成的 `.ps1` 启动脚本（如 `claude.ps1`、`codex.ps1`），并报错 *在此系统上禁止运行脚本*。

为当前用户放行本地与受信任脚本即可：

```powershell
Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned
```

检查当前策略是否生效：

```powershell
Get-ExecutionPolicy -List
```

## 4. 持久化配置环境变量

在终端里临时执行 `export` 或 `$env:...` 仅对当前窗口有效。如需长期生效，请写入系统或 Shell 配置：

### macOS（`~/.zshrc`）与 Linux（`~/.bashrc`）

将配置追加至 `~/.zshrc`（Zsh）或 `~/.bashrc`（Bash）末尾：

```bash
# ADTY AI 凭证（以 Anthropic 协议类 CLI 为例）
export ANTHROPIC_BASE_URL="https://api.adtyai.com"
export ANTHROPIC_AUTH_TOKEN="sk-your-api-key"
```

立即刷新当前终端：

```bash
source ~/.zshrc   # Bash 用户执行：source ~/.bashrc
```

### Windows（PowerShell 用户级环境变量）

通过 `[Environment]::SetEnvironmentVariable` 写入 `"User"` 作用域，终端重启及 VS Code / JetBrains IDE 均可自动继承：

```powershell
[Environment]::SetEnvironmentVariable("ANTHROPIC_BASE_URL", "https://api.adtyai.com", "User")
[Environment]::SetEnvironmentVariable("ANTHROPIC_AUTH_TOKEN", "sk-your-api-key", "User")
```

查看已保存的值：

```powershell
[Environment]::GetEnvironmentVariable("ANTHROPIC_BASE_URL", "User")
```

::: tip 记得重启终端与编辑器
Windows 修改用户级环境变量后，已打开的 PowerShell 标签页或 IDE 进程不会自动热加载，请关闭并重新打开窗口。
:::

## 5. 验证与 ADTY AI 的连通性

正式启动 CLI 前，先确认本机能够正常访问 `https://api.adtyai.com/v1/models` 且 API Key 鉴权有效（请前往 [https://www.adtyai.com](https://www.adtyai.com) 的 **令牌（API Tokens）** 页面获取密钥）。

### macOS / Linux（`curl`）

```bash
curl -sS https://api.adtyai.com/v1/models \
  -H "Authorization: Bearer sk-your-api-key" | head -c 500
```

### Windows（PowerShell）

```powershell
$headers = @{ Authorization = "Bearer sk-your-api-key" }
(Invoke-RestMethod -Uri "https://api.adtyai.com/v1/models" -Headers $headers).data | Select-Object -First 10 id
```

### 常见返回结果说明

- **HTTP 200 并返回 JSON 模型列表**：网络链路、TLS 证书与 API Key 均正常。
- **HTTP 401 Unauthorized**：检查 `sk-...` 密钥是否复制完整、前后有无多余空格或引号，并确认该令牌在 [https://www.adtyai.com](https://www.adtyai.com) 中处于启用状态。
- **HTTP 403 Forbidden**：核对地址是否误填为网页控制台域名 `https://www.adtyai.com`，必须使用 `https://api.adtyai.com`。
- **连接超时或 TLS 报错**：检查本机代理设置（`HTTP_PROXY` / `HTTPS_PROXY`）、VPN 路由规则或防火墙策略。

## 下一步

- [Claude Code 接入教程](/zh-cn/integrations/cli/claude-code)
- [Codex CLI 接入教程](/zh-cn/integrations/cli/codex)
- [Kimi Code 接入教程](/zh-cn/integrations/cli/kimi-code)
- [Grok Build CLI 接入教程](/zh-cn/integrations/cli/grok-build)
- [账号安全与访问令牌](/zh-cn/guides/security)
