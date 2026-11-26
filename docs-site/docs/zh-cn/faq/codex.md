# Codex 排查

汇总使用 OpenAI **Codex CLI** 连接 ADTY AI 时的常见报错与解决方式。完整配置模板请参阅 [Codex CLI 接入指南](/zh-cn/integrations/cli/codex)。

## 1. 漏配或错配 `wire_api = "responses"`

新版 Codex CLI 及 GPT-5 系列模型依赖 OpenAI **Responses API**（`POST /v1/responses`），不再使用旧版 Chat Completions 协议。

请在 `~/.codex/config.toml` 的模型供应商区块中明确写入 `wire_api = "responses"`：

```toml
model_provider = "adty"
model = "gpt-5.4"

[model_providers.adty]
name = "ADTY"
base_url = "https://api.adtyai.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

若缺失该字段或误写为 `"chat"`，通常会遇到 `400` / `404` 报错、推理摘要缺失或工具调用中断。

## 2. `base_url` 必须以 `/v1` 结尾

与 Claude Code 恰好相反，Codex CLI 的 `base_url` **必须**保留 `/v1` 后缀：

- **正确**：`base_url = "https://api.adtyai.com/v1"`
- **错误**：`base_url = "https://api.adtyai.com"` → 请求会打到 `/responses` 而非 `/v1/responses`（返回 `404 Not Found`）
- **错误**：`base_url = "https://www.adtyai.com/v1"` → 控制台域名拦截并返回 `403 api_domain_required`

## 3. `auth.json` 与 `OPENAI_API_KEY` 环境变量冲突

当 `config.toml` 配置了 `requires_openai_auth = true` 时，Codex 会从 `~/.codex/auth.json`（Windows 原生为 `$HOME\.codex\auth.json`）读取密钥：

```json
{
  "OPENAI_API_KEY": "<YOUR_API_KEY>"
}
```

常见鉴权排查要点：

- **JSON 语法错误**：末尾多余逗号或中文引号会导致解析失败。可用 `jq` 或 PowerShell `Get-Content $HOME\.codex\auth.json | ConvertFrom-Json` 校验格式。
- **残留环境变量覆盖**：终端里已导出的旧 `OPENAI_API_KEY` 可能与 `auth.json` 发生冲突。请通过 `env | grep OPENAI`（macOS/Linux）或 `Get-ChildItem Env:OPENAI*`（PowerShell）检查。
- **旧版 OAuth 登录态干扰**：若之前运行过 `codex login` 登录 ChatGPT 订阅账号，`auth.json` 中可能残留 OAuth 字段。建议先备份原文件，再替换为仅含 `"OPENAI_API_KEY"` 的干净 JSON。

## 4. 模型名称不匹配

当 Codex 报错 `404` 或 `403` 提示模型不可用时：

1. 检查 `~/.codex/config.toml` 中的 `model = "..."` 以及启动时是否附带了 `--model` 参数。
2. 调用 `GET https://api.adtyai.com/v1/models`（详见[API 参考](/zh-cn/api/)）或查看控制台**价格**页，核实你的 API Key 所属具体模型分组实际开放的准确模型 ID。
3. 确认该 API Key 未设置把目标模型排除在外的模型白名单。

## 5. 长任务流式断连

在多文件重构或长时间推理阶段，SSE 长连接偶发断开时可按以下顺序排查：

- **代理稳定性**：检查终端代理是否过早回收空闲 HTTP/2 或 SSE 流式连接。
- **线路健康状态**：查看[渠道可用性与分组选择](/zh-cn/guides/channel-status)，确认当前模型分组 24 小时心跳保持绿色稳定。
- **拆分过重上下文**：切换无关任务时建议开启新会话，避免超大上下文持续拉高单轮响应时长。

## 6. 沙箱模式与命令审批策略

出于安全考虑，Codex CLI 默认对文件写入与 Shell 命令执行施加沙箱限制。若发现 Codex 只给建议却不改文件或无法执行测试：

- 检查当前生效的沙箱级别（`read-only`、`workspace-write`、`danger-full-access`）与审批策略（`untrusted`、`on-failure`、`on-request`、`never`）。
- 在常规 Git 仓库开发中，推荐 `workspace-write` 搭配 `on-request`，兼顾效率与安全。
- 在非 Git 目录运行时，Codex 可能会要求显式确认（如传入 `--skip-git-repo-check`）后才放行写操作。

## 7. Windows 与 WSL 路径及编码差异

- **两套独立配置目录**：原生 Windows 读取 `C:\Users\<用户名>\.codex\config.toml`，而 WSL 读取 `/home/<用户名>/.codex/config.toml`，两边配置互不共享。
- **必须保存为无 BOM 的 UTF-8**：Windows 下用记事本或旧版 PowerShell 重定向写入文件可能附带 UTF-8 BOM 或 UTF-16 编码，导致 TOML/JSON 解析报错。请使用 VS Code 或 UTF-8（无 BOM）编码保存 `config.toml` 与 `auth.json`。
- **WSL 网络与代理**：若代理软件运行在 Windows 宿主机，WSL2 内直接访问 `127.0.0.1` 可能不通（除非开启了镜像网络模式）。请先在 WSL 内执行 `curl -I https://api.adtyai.com/v1/models` 测试连通性。
- **仓库存放路径**：在 WSL 中使用 Codex 时，建议将代码仓放在 Linux 文件系统（`~/projects/...`）而非 `/mnt/c/...`，避免跨系统 IO 与 `git status` 卡顿。
