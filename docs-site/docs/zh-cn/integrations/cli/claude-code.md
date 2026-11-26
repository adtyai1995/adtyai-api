# Claude Code 接入指南

**Claude Code** 是 Anthropic 官方推出的终端与 IDE 智能编程助手。**ADTY AI** 完整兼容原生 Anthropic Messages 协议（含 Prompt Cache 提示词缓存、流式输出、工具调用与扩展思考），只需配置接口地址与密钥即可无缝使用。

::: danger `ANTHROPIC_BASE_URL` 切勿添加 `/v1` 后缀
Claude Code 会自动在 `ANTHROPIC_BASE_URL` 后拼接 `/v1/messages`：
- **正确**：`https://api.adtyai.com`
- **错误**：`https://api.adtyai.com/v1`（会被拼成 `/v1/v1/messages` 导致 404）
- **错误**：`https://www.adtyai.com`（网页控制台域名会直接返回 **HTTP 403**）
:::

## 前置准备

1. 已安装 **Node.js 20+ LTS**（Windows 需同时安装 Git Bash），可先完成 [基础环境准备](/zh-cn/integrations/cli/environment)。
2. 前往 [https://www.adtyai.com](https://www.adtyai.com) 的 **令牌（API Tokens）** 页面创建一把支持 Claude 模型分组的密钥（以 `sk-` 开头）。实时可用模型与分组说明请查看 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 及 [渠道可用性与分组选择](/zh-cn/guides/channel-status)。

## 1. 安装 Claude Code

通过 npm 全局安装官方包：

```bash
npm install -g @anthropic-ai/claude-code
```

检查是否安装成功：

```bash
claude --version
```

## 2. 配置 ADTY AI 接入参数

推荐使用持久化的 `~/.claude/settings.json` 配置文件（CLI 与 VS Code / JetBrains 插件均可统一读取），也支持通过系统环境变量配置。

### 方式一：持久化写入 `~/.claude/settings.json`（推荐）

创建或编辑配置文件：
- **macOS / Linux**：`~/.claude/settings.json`
- **Windows**：`%USERPROFILE%\.claude\settings.json`（PowerShell 中为 `$HOME\.claude\settings.json`）

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

字段说明：

| 参数 | 说明 |
| --- | --- |
| `ANTHROPIC_BASE_URL` | 指向 **ADTY AI** 专用 API 域名 `https://api.adtyai.com`（**不要**带 `/v1`）。 |
| `ANTHROPIC_AUTH_TOKEN` | 你的 **ADTY AI** API 密钥（`sk-...`）。 |
| `API_TIMEOUT_MS` | 将单次请求超时时间调大至 `600000` 毫秒（10 分钟），防止大型代码重构或深度思考中途断流。 |
| `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` | 可选（`"1"`），关闭遥测、错误上报及后台自动更新检查等非必要流量。 |

::: warning 避免鉴权变量冲突
不要同时把 `ANTHROPIC_API_KEY` 与 `ANTHROPIC_AUTH_TOKEN` 设成不同的密钥；如果此前登录过官方 OAuth 账号导致请求走 `claude.ai`，请先在 Claude Code 中执行 `/logout` 清除旧登录态。
:::

### 方式二：通过 Shell 环境变量配置

如需在终端配置或临时测试：

**macOS / Linux（`~/.zshrc` 或 `~/.bashrc`）**：

```bash
export ANTHROPIC_BASE_URL="https://api.adtyai.com"
export ANTHROPIC_AUTH_TOKEN="sk-your-api-key"
export API_TIMEOUT_MS="600000"
```

**Windows（PowerShell 当前窗口临时生效）**：

```powershell
$env:ANTHROPIC_BASE_URL="https://api.adtyai.com"
$env:ANTHROPIC_AUTH_TOKEN="sk-your-api-key"
$env:API_TIMEOUT_MS="600000"
```

**Windows（PowerShell 永久写入当前用户环境）**：

```powershell
[Environment]::SetEnvironmentVariable("ANTHROPIC_BASE_URL", "https://api.adtyai.com", "User")
[Environment]::SetEnvironmentVariable("ANTHROPIC_AUTH_TOKEN", "sk-your-api-key", "User")
[Environment]::SetEnvironmentVariable("API_TIMEOUT_MS", "600000", "User")
```

::: tip 喜欢图形化管理？
你也可以通过 [CC Switch — 配置 Claude Code](/zh-cn/integrations/cc-switch/claude-code) 一键写入并切换多套供应商配置。
:::

## 3. 选择 Claude 模型（Opus / Sonnet / Haiku）

Claude Code 支持在对话中动态切模型，也支持在 `~/.claude/settings.json` 中固定默认映射：

- **会话内切换**：启动后输入 `/model`，按场景在 Sonnet（日常开发主力）、Opus（复杂架构与深度推理）、Haiku（轻量快速响应）之间切换。
- **启动时指定模型**：

```bash
claude --model claude-sonnet-4-6
```

- **在 `~/.claude/settings.json` 中指定默认档位模型**：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-your-api-key",
    "API_TIMEOUT_MS": "600000",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4-6",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-6",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-haiku-4-5-20251001"
  }
}
```

请始终以 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 页面展示的实时模型名称和当前令牌分组权限为准。

## 4. VS Code 与 JetBrains 插件接入

VS Code（含 Cursor / Windsurf）以及 JetBrains 全家桶的官方 **Claude Code** 插件均会自动读取 `~/.claude/settings.json`。

1. 确保 `~/.claude/settings.json` 中的 `"env"` 已正确填写 `ANTHROPIC_BASE_URL` 与 `ANTHROPIC_AUTH_TOKEN`（推荐方式一）。
2. 在 VS Code 扩展商店或 JetBrains Marketplace 安装官方 **Claude Code** 插件。
3. **完全退出并重启 IDE**，确保插件重新加载最新配置文件与用户级环境变量。
4. 打开侧边栏的 Claude Code 面板即可开始使用。如果仍弹出浏览器 OAuth 登录框，请检查 `~/.claude/settings.json` 是否存在语法错误（如多余尾逗号）。

::: info WSL 与 Remote SSH 环境提示
在 VS Code Remote-SSH 或 WSL 中使用时，Claude Code 实际运行在**远端 / WSL 主机**侧，请将 `~/.claude/settings.json` 写入对应远端或 WSL 的用户主目录，而非仅写在本地 Windows。
:::

## 5. 验证配置是否生效

进入任意项目目录，先运行一条非交互式快速测试：

```bash
cd /path/to/your/project
claude -p "只回复两个字：正常"
```

返回 `正常` 后，启动交互式会话并检查状态：

```bash
claude
```

在对话框中输入 `/status`，确认：
- 接口地址指向 `https://api.adtyai.com`
- 当前模型与预期一致
- 认证方式为 API Token，而非官方个人订阅

随后可在控制台 [https://www.adtyai.com](https://www.adtyai.com) 的 **使用日志（Usage Logs）** 中实时查看请求明细与 Prompt Cache 命中记录。

## 排查与进阶阅读

- **提示词缓存命中率低或费用偏高**：详见 [Claude Code 缓存命中诊断与修复](/zh-cn/integrations/cli/cache-fix)。
- **401 / 403 / 404 或断流报错**：详见 [Claude Code 排查](/zh-cn/faq/claude-code)。
- **在 Claude Code 中调用其他模型**：详见 [Claude Code 调用 ChatGPT](/zh-cn/integrations/advanced/chatgpt-claude-code) 与 [Claude Code 调用 DeepSeek](/zh-cn/integrations/advanced/deepseek-claude-code)。
