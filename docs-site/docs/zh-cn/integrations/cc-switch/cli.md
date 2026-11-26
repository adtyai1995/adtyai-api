# CC Switch CLI 工具

如果你在无图形界面的远程 Linux 服务器、SSH 开发机、Docker 容器、CI 环境或 **WSL2** 中开发，可以直接使用 **`cc-switch`** 命令行工具管理 **ADTY AI** 的供应商配置，无需依赖桌面端窗口。

## 适用场景

- **远程 Linux 服务器与 SSH 开发**：通过终端一键切换 Claude Code 或 Codex 的供应商配置，不必手动修改 `~/.claude/settings.json` 或 `~/.codex/config.toml`。
- **WSL2 子系统**：在 Windows 内的 Linux 环境中独立管理 CLI 工具配置，互不干扰。
- **新开发机快速初始化**：将本地桌面端导出的 ADTY AI 档案包一键导入到新服务器。
- **脚本与自动化流程**：在批处理或自动化任务前通过命令切换指定 Key 或模型分组。

## 安装方式

前往 CC Switch CLI 官方发布渠道获取对应架构的可执行程序：

### 预编译二进制安装（Linux / macOS / WSL）

1. 下载对应操作系统与 CPU 架构的压缩包（如 `linux-x86_64`、`linux-aarch64`、`darwin-arm64`、`darwin-x86_64`）。
2. 解压并将可执行文件放入 `PATH` 目录（例如 `~/.local/bin` 或 `/usr/local/bin`）：

```bash
chmod +x cc-switch
mkdir -p ~/.local/bin
mv cc-switch ~/.local/bin/cc-switch
```

3. 验证安装是否成功：

```bash
cc-switch --version
cc-switch --help
```

### 包管理器安装

在已配置对应软件源的 macOS / Linux 环境中，也可通过包管理器安装：

```bash
# 示例：使用 Homebrew 安装（视官方 Tap/Formula 发布情况而定）
brew install cc-switch

# 安装后查看当前版本支持的子命令
cc-switch --help
```

::: tip 不同版本的子命令参数
不同版本的 CC Switch CLI 参数名可能略有调整，执行 `cc-switch --help` 或 `cc-switch <subcommand> --help` 即可查看当前安装版本的准确用法。
:::

## 配合 ADTY AI 的常用命令

### 1. 查看已配置的供应商档案

```bash
# 列出 Claude Code 档案
cc-switch list --app claude

# 列出 Codex CLI 档案
cc-switch list --app codex
```

### 2. 新增 ADTY AI 供应商档案

添加时请务必遵循各客户端对应的 ADTY AI Base URL 规范：

- **Claude Code**（Anthropic Messages 协议 —— **不加 `/v1`**）：
  - Base URL：`https://api.adtyai.com`
  - Auth Token：`sk-your-api-key`
- **Codex CLI**（OpenAI Responses 协议 —— **必须加 `/v1`**）：
  - Base URL：`https://api.adtyai.com/v1`
  - API Key：`sk-your-api-key`
  - Wire API：`responses`

```bash
# 推荐使用交互式向导添加，避免 API Key 留存在 Shell 历史记录中
cc-switch add --app claude
cc-switch add --app codex
```

::: warning 避免将 API Key 留在命令历史中
尽量使用交互式输入或文件导入方式配置密钥，避免直接把 `sk-...` 明文作为命令行参数传入。如果确实在 Bash/Zsh 命令行中传入了密钥，请记得及时清理对应的命令历史记录。
:::

### 3. 切换当前生效的供应商

将 `<profile-name>` 替换为你保存的档案名称：

```bash
# 为 Claude Code 启用指定的 ADTY AI 档案
cc-switch use --app claude "<profile-name>"

# 为 Codex CLI 启用指定的 ADTY AI 档案
cc-switch use --app codex "<profile-name>"
```

### 4. 查看当前生效状态与配置文件

```bash
# 查看各客户端当前激活的供应商
cc-switch status

# 直接核对写入磁盘的原生配置文件
cat ~/.claude/settings.json
cat ~/.codex/config.toml
```

### 5. 跨机器导出与导入档案

你可以在本地电脑整理好 ADTY AI 档案后，安全同步到远程服务器或 WSL2：

```bash
# 1. 在源设备导出档案包
cc-switch export -o cc-switch-profiles.json

# 2. 通过 SSH 安全传输到目标服务器
scp cc-switch-profiles.json <user>@<remote-host>:~/

# 3. 在目标服务器收紧权限、导入档案，并立即删除临时传输文件
chmod 600 ~/cc-switch-profiles.json
cc-switch import ~/cc-switch-profiles.json
rm -f ~/cc-switch-profiles.json
```

导入并激活后，建议同时确保本机凭据文件仅当前用户可读：

```bash
chmod 600 ~/.claude/settings.json ~/.codex/auth.json 2>/dev/null || true
```

## 排查远程主机与 WSL 中的 Shell 环境变量冲突

在云主机、容器镜像、`tmux` 会话或 VS Code Remote 环境中，环境变量经常从 dotfiles、镜像构建参数或宿主机继承而来。一旦 Shell 中存在 `ANTHROPIC_*` 或 `OPENAI_*` 变量，它们会直接覆盖 `cc-switch` 写入的配置文件。

如果切换档案后发现客户端没有走新的 Key 或地址，请运行以下排查命令：

```bash
env | grep -E '^(ANTHROPIC|OPENAI|GEMINI|CLAUDE)_'
```

若输出中存在冲突条目：

1. 定位它们定义在哪个初始化脚本中：

   ```bash
   grep -rnE '(ANTHROPIC|OPENAI|GEMINI)_(BASE_URL|API_KEY|AUTH_TOKEN)' \
     ~/.bashrc ~/.zshrc ~/.profile ~/.bash_profile /etc/environment 2>/dev/null
   ```

2. 删除或注释掉旧的 `export` 配置。
3. 在当前终端会话立即取消覆盖：

   ```bash
   unset ANTHROPIC_BASE_URL ANTHROPIC_AUTH_TOKEN ANTHROPIC_API_KEY
   unset OPENAI_BASE_URL OPENAI_API_KEY
   ```

4. 如果正在使用 `tmux` 或 `screen`，已有窗格会保留旧环境变量，清理后请新建会话或在 `tmux` 中刷新环境。

## 在无界面环境中验证连通性

切换完成后，可快速测试网络和客户端状态：

```bash
# 检查到 ADTY AI API 网关的网络连通性
curl -I https://api.adtyai.com

# 确认客户端可执行
claude --version
codex --version
```

发送一条测试指令后，前往 [https://www.adtyai.com/logs](https://www.adtyai.com/logs) 确认请求已正常落入对应 API Key 的调用日志。

## 相关文档

- [通用基础配置](/zh-cn/integrations/cc-switch/common) — 双域名规范、代理设置与多档案管理建议。
- [配置 Claude Code](/zh-cn/integrations/cc-switch/claude-code) — Claude Code `settings.json` 完整说明。
- [配置 Codex CLI](/zh-cn/integrations/cc-switch/codex) — Codex `config.toml` 与 `auth.json` 完整说明。
- [额度与余额查询](/zh-cn/integrations/cc-switch/usage-query) — 通过控制台与查询接口实时掌控额度消耗。
