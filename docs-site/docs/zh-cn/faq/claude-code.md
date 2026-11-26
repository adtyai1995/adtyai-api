# Claude Code 排查

整理使用 **Claude Code** 接入 ADTY AI 时最常遇到的配置与运行问题。如需从零安装与配置，请先阅读 [Claude Code 接入指南](/zh-cn/integrations/cli/claude-code)。

## 1. `ANTHROPIC_BASE_URL` 误加 `/v1` 导致 HTTP `404`

这是 Claude Code 新手最高频的配置错误。Claude Code 会自动在 `ANTHROPIC_BASE_URL` 后面拼接 `/v1/messages`。

- **错误**：`https://api.adtyai.com/v1` → 实际请求变成 `/v1/v1/messages`（返回 `404 Not Found`）
- **错误**：`https://www.adtyai.com` → 控制台域名拦截并返回 `403 api_domain_required`
- **正确**：`https://api.adtyai.com`（末尾**不要**加 `/v1`，也不要留斜杠）

```bash
export ANTHROPIC_BASE_URL="https://api.adtyai.com"
export ANTHROPIC_AUTH_TOKEN="<YOUR_API_KEY>"
```

## 2. `ANTHROPIC_AUTH_TOKEN` 与 `ANTHROPIC_API_KEY` 混用冲突

Claude Code 同时识别这两个变量，如果在 Shell 启动脚本（`~/.zshrc`、`~/.bashrc`）或 Windows 系统环境变量中同时残留旧值，极易引发莫名其妙的 `401` 鉴权失败或启动时的密钥确认弹窗。

- **推荐做法**：统一使用 `ANTHROPIC_AUTH_TOKEN`（与[交互式配置向导](/zh-cn/integrations/cli/)一致），并清理环境中多余的旧 `ANTHROPIC_API_KEY`。
- 如果把凭证写在 `~/.claude/settings.json` 的 `env` 字段中，请确认当前终端没有导出同名的旧系统环境变量覆盖它。
- 快速检查当前终端实际生效的变量：

```bash
# macOS / Linux
env | grep -E "ANTHROPIC_|CLAUDE_"
```

```powershell
# Windows PowerShell
Get-ChildItem Env: | Where-Object { $_.Name -match "^(ANTHROPIC_|CLAUDE_)" }
```

## 3. 启动时仍弹出官方 OAuth 登录页面

如果运行 `claude` 时仍然跳转浏览器要求登录 `claude.ai` 订阅账号，而未直接使用 ADTY AI API Key：

1. 确认当前终端确实加载了 `ANTHROPIC_BASE_URL` 与 `ANTHROPIC_AUTH_TOKEN`（或已正确写入 `~/.claude/settings.json`）。
2. 如之前登录过官方订阅账号，可在 Claude Code 内执行 `/logout` 清除残留 OAuth 会话，或清理 `~/.claude.json` 与系统钥匙串中的旧登录态。
3. 在无头服务器或 CI 环境中卡在首次引导界面时，可确认 `~/.claude.json` 中已包含 `"hasCompletedOnboarding": true`。

## 4. Windows PowerShell 执行策略与 Git Bash 问题

### PowerShell 禁止运行脚本（`PSSecurityException`）

当运行 `claude` 或 `npm` 全局命令提示“因为在此系统上禁止运行脚本”时，在 PowerShell 中执行：

```powershell
Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned
```

重新打开 PowerShell 窗口后执行 `claude --version` 验证。

### Windows 提示找不到 Git Bash

原生 Windows 版 Claude Code 需要调用 Git Bash 执行终端工具。若报错提示找不到 `bash.exe`：

1. 安装 [Git for Windows](https://git-scm.com/downloads/win)。
2. 显式指定 `bash.exe` 路径：

```powershell
$env:CLAUDE_CODE_GIT_BASH_PATH="C:\Program Files\Git\bin\bash.exe"
```

如需永久生效，可将 `CLAUDE_CODE_GIT_BASH_PATH` 添加到 Windows 用户环境变量或 PowerShell `$PROFILE` 中。

## 5. Prompt Cache 缓存命中率突然归零

Claude Code 每轮都会携带较长的系统提示词、工具声明与上下文前缀。如果连续多轮对话中 `cache_read_input_tokens` 降为 `0`，不仅响应变慢，输入费用也会显著上升。

常见诱因包括客户端版本行为变化、会话中途切换模型/Beta 请求头或工具定义抖动。请直接参考专项教程 [Claude Code 缓存命中修复](/zh-cn/integrations/cli/cache-fix) 进行排查与恢复。

## 6. 代理环境变量（`HTTPS_PROXY` / `HTTP_PROXY`）

如果本地网络需通过代理访问 `https://api.adtyai.com`：

```bash
export HTTPS_PROXY="http://127.0.0.1:7890"
export HTTP_PROXY="http://127.0.0.1:7890"
export NO_PROXY="localhost,127.0.0.1"
```

- 在启用企业 HTTPS 证书审计的内网环境中，如遇到 `UNABLE_TO_VERIFY_LEAF_SIGNATURE` 证书错误，可通过 `NODE_EXTRA_CA_CERTS="/path/to/corp-ca.pem"` 注入企业根证书。
- 若关闭代理软件后终端仍遗留无效的 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY`，请将其 `unset` 后重试。

## 7. 思考预算（Thinking）与 Effort 参数建议

使用支持扩展思考或自适应思考强度的 Claude 模型时：

- 请确保 `max_tokens` **严格大于**思考 Token 预算，否则接口会返回 `400 Bad Request`。
- 手动设置 `MAX_THINKING_TOKENS` 时应结合任务复杂度适度配置；过高的预算会明显延长首个可见字符出现前的等待时间。
- 确认你在 Claude Code 中配置的默认模型以及子任务模型别名，均已包含在当前 API Key 所绑定的具体模型分组内。
