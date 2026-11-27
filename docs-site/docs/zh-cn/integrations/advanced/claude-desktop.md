# Claude Desktop

Claude Desktop 将桌面对话界面与 **Claude Code 桌面模式** 以及本地 **MCP（Model Context Protocol）** 能力集成在一起。本文介绍如何将 Claude Desktop 中的 API 编程工作流接入 **ADTY AI**（`https://api.adtyai.com`）。

::: info 网页账号对话与 Claude Code 桌面模式的区别
Claude Desktop 中使用个人网页账号登录的常规聊天标签页会直接连接官方消费者后端；而通过 `ANTHROPIC_BASE_URL` 与 `ANTHROPIC_AUTH_TOKEN` 自定义的 API 路由，适用于 **Claude Code 桌面模式** 及桌面端托管的本地 Claude Code 会话。
:::

## 方式一：通过 CC Switch 一键配置（推荐）

如果你想免去手动修改配置文件与桌面环境变量的麻烦，并支持一键切换不同分组或供应商，推荐使用 **CC Switch** 完成配置。

请直接参考专题教程：[CC Switch — 配置 Claude Desktop](/zh-cn/integrations/cc-switch/claude-desktop)。

## 方式二：手动配置环境与设置文件

如需手动管理配置，请同时配置 `~/.claude/settings.json` 与桌面图形界面可见的环境变量。

### 1. 配置 `~/.claude/settings.json`

编辑 `~/.claude/settings.json`（Windows 路径为 `%USERPROFILE%\.claude\settings.json`），写入 `ANTHROPIC_BASE_URL` 与 `ANTHROPIC_AUTH_TOKEN`：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>"
  }
}
```

::: warning 不要在末尾添加 `/v1`
Claude Desktop 与 Claude Code 会自动在 `ANTHROPIC_BASE_URL` 后拼接 `/v1/messages`。请务必填写 `https://api.adtyai.com`，不要带 `/v1`。
:::

### 2. 为图形界面应用注入环境变量

从 macOS 访达 / 程序坞或 Windows 开始菜单启动的桌面应用，往往不会自动继承仅写在 `~/.zshrc` 或 `~/.bashrc` 中的环境变量：

::: code-group

```bash [macOS（图形会话环境变量）]
launchctl setenv ANTHROPIC_BASE_URL "https://api.adtyai.com"
launchctl setenv ANTHROPIC_AUTH_TOKEN "<YOUR_API_KEY>"
```

```powershell [Windows（用户级环境变量）]
[Environment]::SetEnvironmentVariable("ANTHROPIC_BASE_URL", "https://api.adtyai.com", "User")
[Environment]::SetEnvironmentVariable("ANTHROPIC_AUTH_TOKEN", "<YOUR_API_KEY>", "User")
```

:::

设置完成后，请**彻底退出** Claude Desktop（不能只关闭窗口，需从 macOS 顶部菜单栏或 Windows 系统托盘完全退出），再重新打开使其读取最新环境变量与 `~/.claude/settings.json`。

## 3. MCP 服务器兼容性

将模型推理切换至 **ADTY AI** 后，你在 Claude Desktop 中配置的本地 MCP 服务器仍会照常在本地运行，模型推理流量则通过 `https://api.adtyai.com` 转发。

Claude Desktop 的 MCP 配置文件位于：

- **macOS**：`~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows**：`%APPDATA%\Claude\claude_desktop_config.json`

示例配置：

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

::: tip 桌面端找不到 `npx` / `uvx` 怎么办？
如果某个 MCP 服务器在终端能正常运行，但在 Claude Desktop 内启动失败，请将 `command` 改为可执行文件的绝对路径（例如 macOS 下的 `/opt/homebrew/bin/npx` 或 Windows 下的 `C:\Program Files\nodejs\npx.cmd`）。
:::

## 4. 代理与 TLS 证书问题排查

- **企业网络证书校验报错（`UNABLE_TO_VERIFY_LEAF_SIGNATURE` / `CERT_HAS_EXPIRED`）**：若公司网络部署了 HTTPS 审计代理，请将企业根证书 PEM 文件路径配置到环境变量 `NODE_EXTRA_CA_CERTS` 并彻底重启 Claude Desktop；生产环境请勿关闭 TLS 证书校验。
- **本地代理工具冲突**：如本机运行了代理软件，确认它没有对 `https://api.adtyai.com` 进行异常 TLS 拦截，也没有残留失效的 `HTTPS_PROXY` 环境变量；可尝试在代理规则中将 `https://api.adtyai.com` 设为直连以排除干扰。
- **认证变量冲突**：检查系统环境变量、Shell 配置与 `~/.claude/settings.json` 中是否同时存在旧的 `ANTHROPIC_API_KEY` 与新的 `ANTHROPIC_AUTH_TOKEN`，避免鉴权覆盖。
- **提示词缓存命中异常**：如发现 Claude Code 会话缓存命中率偏低，请参考 [Claude Code 缓存命中修复](/zh-cn/integrations/cli/cache-fix) 及 [Claude Code 排查](/zh-cn/faq/claude-code)。
