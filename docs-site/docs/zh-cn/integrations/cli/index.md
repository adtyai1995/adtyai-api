# 交互式配置向导与 CLI 总览

几分钟内将终端编程工具接入 **ADTY AI**。直接使用下方交互式向导选择你的客户端与操作系统，即可自动生成带当前站点专属 API 地址的配置片段，并一键复制或跳转至完整教程。

<SetupWizard />

::: warning 务必填写专用 API 域名
所有 CLI 工具与 SDK 均需指向专用 API 域名 `https://api.adtyai.com`（OpenAI 兼容协议使用 `https://api.adtyai.com/v1`）。切勿填写网页控制台域名 `https://www.adtyai.com`，否则接口请求会被网关直接拒绝并返回 **HTTP 403**。详见 [快速上手与双域名规则](/zh-cn/guides/overview)。
:::

## 支持的 CLI 客户端对比

不同 CLI 工具依赖的上游协议路径各不相同。配置时请特别注意是否需要携带 `/v1` 后缀：

| 客户端 | 接口协议 | Base URL 格式 | 核心配置方式 | 完整教程 |
| --- | --- | --- | --- | --- |
| **Claude Code** | Anthropic Messages（`/v1/messages`） | `https://api.adtyai.com`（*不带 `/v1`*） | `ANTHROPIC_BASE_URL` / `~/.claude/settings.json` | [Claude Code 接入指南](/zh-cn/integrations/cli/claude-code) |
| **Codex CLI** | OpenAI Responses（`/v1/responses`） | `https://api.adtyai.com/v1` | `~/.codex/config.toml` 与 `~/.codex/auth.json` | [Codex CLI 接入指南](/zh-cn/integrations/cli/codex) |
| **Kimi Code** | Anthropic 协议或 OpenAI 兼容协议 | `https://api.adtyai.com`（Anthropic）或 `https://api.adtyai.com/v1`（OpenAI） | `~/.kimi/config.toml` / 环境变量 | [Kimi Code 接入指南](/zh-cn/integrations/cli/kimi-code) |
| **Grok Build CLI** | xAI / OpenAI 兼容协议 | `https://api.adtyai.com/v1` | `XAI_BASE_URL` / CLI 配置文件 | [Grok Build CLI 接入指南](/zh-cn/integrations/cli/grok-build) |

::: tip 需要多配置一键切换？
如果你同时使用多个令牌分组、多把 API Key 或多款编程客户端（Claude Code、Codex CLI、Codex 桌面应用、Claude Desktop），推荐使用 [CC Switch 专题](/zh-cn/integrations/cc-switch/)，无需反复手动修改配置文件即可可视化管理供应商配置。
:::

## 推荐接入顺序

1. **生成 API 密钥**：登录控制台 [https://www.adtyai.com](https://www.adtyai.com)，进入 **令牌（API Tokens）** 页面，选择所需模型对应的分组并创建密钥（以 `sk-` 开头）。如不确定分组差异，可先阅读 [渠道可用性与分组选择](/zh-cn/guides/channel-status)。
2. **检查本地基础环境**：按照 [基础环境准备](/zh-cn/integrations/cli/environment) 确认 Node.js LTS（`v20+`）、全局命令 `PATH`、Git（Windows 含 Git Bash）以及到 `https://api.adtyai.com/v1/models` 的网络连通性。
3. **完成客户端配置**：按需阅读对应 CLI 的分步指南：
   - [Claude Code 接入教程](/zh-cn/integrations/cli/claude-code)
   - [Codex CLI 接入教程](/zh-cn/integrations/cli/codex)
   - [Kimi Code 接入教程](/zh-cn/integrations/cli/kimi-code)
   - [Grok Build CLI 接入教程](/zh-cn/integrations/cli/grok-build)
4. **优化缓存与成本**：若长时间使用 Claude Code 时发现 Prompt Cache 命中率偏低或费用异常升高，请阅读 [Claude Code 缓存命中诊断与修复](/zh-cn/integrations/cli/cache-fix)。

## 需要桌面客户端、Agent 框架或官方 SDK？

- 查看更多图形化客户端、智能体工具与 Python/TypeScript SDK，请访问 [进阶、桌面端与 SDK 总览](/zh-cn/integrations/advanced/)。
- 想在 Claude Code 或 Codex 中接入其他模型，可参考 [Claude Code 调用 ChatGPT](/zh-cn/integrations/advanced/chatgpt-claude-code)、[Claude Code 调用 DeepSeek](/zh-cn/integrations/advanced/deepseek-claude-code) 与 [Codex 调用 DeepSeek](/zh-cn/integrations/advanced/deepseek-codex)。
- 遇到鉴权失败、超时或模型报错，请查阅 [通用常见问题](/zh-cn/faq/)、[Claude Code 排查](/zh-cn/faq/claude-code) 或 [Codex 排查](/zh-cn/faq/codex)。
