# 进阶客户端、桌面端、SDK 与跨协议总览

除了命令行编码工具外，**ADTY AI** 同样完整兼容各类自主编码 Agent、桌面端 AI 工作区、官方编程语言 SDK 以及跨协议模型调用。所有接入方式统一使用同一个 API 网关地址（`https://api.adtyai.com`）以及在 [ADTY AI 控制台](https://www.adtyai.com) 中创建的 API 密钥。

## 客户端与接入方式目录

按使用场景查找对应的接入文档，并确认各客户端所需的 Base URL 格式：

### 编码 Agent

| 客户端 | 接入协议 | Base URL | 文档入口 |
| --- | --- | --- | --- |
| **OpenCode** | OpenAI 兼容 / Anthropic | `https://api.adtyai.com/v1` | [OpenCode](/zh-cn/integrations/advanced/opencode) |
| **OpenClaw** | OpenAI Completions / Anthropic Messages | `https://api.adtyai.com/v1` 或 `https://api.adtyai.com` | [OpenClaw](/zh-cn/integrations/advanced/openclaw) |
| **Hermes Agent** | OpenAI 兼容 / Anthropic | `https://api.adtyai.com/v1` 或 `https://api.adtyai.com` | [Hermes Agent](/zh-cn/integrations/advanced/hermes) |

### 桌面端与工作区应用

| 客户端 | 接入协议 | Base URL | 文档入口 |
| --- | --- | --- | --- |
| **Cherry Studio** | OpenAI / Anthropic / Gemini | `https://api.adtyai.com` | [Cherry Studio](/zh-cn/integrations/advanced/cherry-studio) |
| **Claude Desktop** | Anthropic Messages | `https://api.adtyai.com` | [Claude Desktop](/zh-cn/integrations/advanced/claude-desktop) |
| **WorkBuddy** | OpenAI 兼容 / Anthropic | `https://api.adtyai.com/v1` 或 `https://api.adtyai.com` | [WorkBuddy](/zh-cn/integrations/advanced/workbuddy) |
| **AionUI** | OpenAI / Anthropic / Gemini + 本地 CLI | `https://api.adtyai.com/v1` 或 `https://api.adtyai.com` | [AionUI](/zh-cn/integrations/advanced/aionui) |
| **All API Hub** | 控制台站点 + API 网关 | 站点：`https://www.adtyai.com` / API：`https://api.adtyai.com` | [All API Hub](/zh-cn/integrations/advanced/all-api-hub) |

### 官方 SDK

| SDK | 接入协议 | Base URL | 文档入口 |
| --- | --- | --- | --- |
| **OpenAI SDK**（Python / Node.js） | Chat Completions 与 Responses API | `https://api.adtyai.com/v1` | [官方 SDK 接入](/zh-cn/integrations/advanced/sdks) |
| **Anthropic SDK**（Python / TypeScript） | Anthropic Messages API | `https://api.adtyai.com` | [官方 SDK 接入](/zh-cn/integrations/advanced/sdks) |
| **Google GenAI SDK**（Python / TypeScript） | Gemini 原生接口 | `https://api.adtyai.com` | [官方 SDK 接入](/zh-cn/integrations/advanced/sdks) |

### 跨协议调用

| 使用方式 | 协议转换方向 | Base URL | 文档入口 |
| --- | --- | --- | --- |
| **Claude Code 调用 ChatGPT / GPT** | Anthropic Messages → OpenAI | `https://api.adtyai.com` | [Claude Code 调用 ChatGPT](/zh-cn/integrations/advanced/chatgpt-claude-code) |
| **Claude Code 调用 DeepSeek** | Anthropic Messages → DeepSeek | `https://api.adtyai.com` | [Claude Code 调用 DeepSeek](/zh-cn/integrations/advanced/deepseek-claude-code) |
| **Codex 调用 DeepSeek** | OpenAI Chat Completions / Responses → DeepSeek | `https://api.adtyai.com/v1` | [Codex 调用 DeepSeek](/zh-cn/integrations/advanced/deepseek-codex) |

## 如何区分 `https://api.adtyai.com` 与 `https://api.adtyai.com/v1`

第三方客户端最常见的报错原因，就是在 Base URL 末尾漏写或重复拼接 `/v1`。按下面规则即可快速判断：

| 地址写法 | 适用客户端 | 客户端最终拼出的请求地址示例 |
| --- | --- | --- |
| `https://api.adtyai.com/v1` | OpenAI 官方 SDK、Codex CLI、OpenCode（含 `@ai-sdk/openai-compatible` 与 `@ai-sdk/anthropic`）、Hermes OpenAI 模式，以及直接在地址后拼接 `/chat/completions`、`/responses` 或 `/messages` 的客户端 | `https://api.adtyai.com/v1/chat/completions` |
| `https://api.adtyai.com` | Anthropic 官方 SDK、Claude Code、Claude Desktop、Google GenAI SDK，以及会自动补全 `/v1` 或 `/v1beta` 的桌面端（如 Cherry Studio） | `https://api.adtyai.com/v1/messages` 或 `https://api.adtyai.com/v1beta/models/...` |

::: warning 切勿将控制台域名当作 API 地址
所有模型接口请求必须发送到 `https://api.adtyai.com`，不要填入 `https://www.adtyai.com`。网页域名 `https://www.adtyai.com` 仅用于账号登录、充值计费、密钥管理与模型价格查询，详情请见 [快速上手与双域名规则](/zh-cn/guides/overview)。
:::

## 接入前准备

1. **创建专属 API 密钥**：登录 [https://www.adtyai.com](https://www.adtyai.com)，在「令牌 / API 密钥」页面为不同客户端分别创建密钥并设置额度上限。安全规范参考 [账号安全与访问令牌](/zh-cn/guides/security)。
2. **核对模型 ID 与分组**：在 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 确认你选择的分组下精确的模型名称，并可在 [渠道可用性与分组选择](/zh-cn/guides/channel-status) 查看实时状态。
3. **需要多配置一键切换？** 如果你经常在 Claude Code、Codex 或 Claude Desktop 之间切换不同分组与供应商，推荐使用 [CC Switch 简介与安装](/zh-cn/integrations/cc-switch/)。

## 相关栏目

- 需要配置 Claude Code、Codex CLI、Kimi Code 或 Grok Build？请前往 [CLI 与快速开始](/zh-cn/integrations/cli/)。
- 需要查看原始 HTTP 端点、请求头或错误码？请查看 [API 参考](/zh-cn/api/)。
- 遇到连接超时、鉴权失败或流式输出异常？请查看 [通用常见问题](/zh-cn/faq/)。
