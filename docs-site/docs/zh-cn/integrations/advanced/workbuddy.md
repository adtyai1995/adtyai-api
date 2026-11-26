# WorkBuddy

WorkBuddy 是兼顾研发编码与日常办公协作的桌面端 AI 工作区。你可以在 WorkBuddy 中添加自定义 OpenAI 兼容或 Anthropic 供应商，直接接入 **ADTY AI**。

## 1. 创建专用 API 密钥

1. 登录 [ADTY AI 控制台](https://www.adtyai.com)，打开 **令牌 / API 密钥** 页面。
2. 点击新建密钥并按用途清晰命名（例如 `workbuddy-coding` 或 `workbuddy-office`）。
3. 选择与目标模型对应的渠道分组，并按需设置额度上限。更多权限隔离建议见 [账号安全与访问令牌](/zh-cn/guides/security)。

## 2. 添加自定义供应商

打开 WorkBuddy，进入 **设置 → 模型供应商**（或 **API 配置**），点击 **添加自定义供应商**。

按你计划调用的模型协议选择对应地址：

| 协议类型 | API Base URL | 适用场景 |
| --- | --- | --- |
| **OpenAI 兼容协议** | `https://api.adtyai.com/v1` | GPT、DeepSeek、Gemini 及统一 OpenAI 协议多模型调用 |
| **Anthropic 协议** | `https://api.adtyai.com` | 原生 Claude 系列模型（Sonnet / Opus / Haiku） |

依次填写以下配置项：

- **供应商名称**：`ADTY AI`
- **API 地址（Base URL）**：OpenAI 兼容填 `https://api.adtyai.com/v1`，Anthropic 填 `https://api.adtyai.com`
- **API 密钥**：填入 `<YOUR_API_KEY>`

::: tip 留意是否自动拼接 `/v1`
部分版本的桌面客户端会在 OpenAI 兼容地址后自动补全 `/v1`。若输入框下方提供请求地址预览，请确认最终请求路径为 `https://api.adtyai.com/v1/chat/completions`（或 Anthropic 的 `https://api.adtyai.com/v1/messages`），没有出现 `/v1/v1` 重复。
:::

## 3. 添加模型 ID 并验证连接

1. 点击 **拉取模型列表**（如客户端支持）或点击 **手动添加模型**。
2. 从 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 复制准确的 `<model-id>`（例如 `claude-sonnet-4-6`、`gpt-5.4`、`deepseek-chat`、`gemini-2.5-pro`）。
3. 点击 **测试连接** 验证密钥与地址；通过后新建工作区对话，发送简短测试消息（如 `请回复 OK 确认连接正常`）确认流式响应正常。

## 4. 编码任务 vs 日常办公的分组与模型选择

研发编码与日常办公对上下文长度、工具调用稳定性和单次成本的侧重点不同，建议按场景选择模型，必要时拆分为两把不同分组的密钥以便分账统计：

| 使用场景 | 优先关注指标 | 推荐模型层级 |
| --- | --- | --- |
| **代码研发与项目分析** | 长上下文准确度、文件与工具调用稳定性、复杂逻辑推理 | 旗舰 Claude Sonnet / Opus 或 GPT-5 系列，绑定专注稳定性的编码渠道分组 |
| **日常办公**（文档润色、翻译、会议纪要、表格处理） | 首字响应速度与高性价比 | 轻量级 Claude Haiku、GPT mini、Gemini Flash 或 `deepseek-chat` |

你可以随时在 [渠道可用性与分组选择](/zh-cn/guides/channel-status) 与 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 查看各分组实时状态与覆盖模型。

## 常见问题排查

- **`401 Unauthorized`**：检查密钥是否从 [https://www.adtyai.com](https://www.adtyai.com) 完整复制，以及是否已达到密钥限额。
- **`404 Not Found`**：核对当前所选协议是否多写或漏写了 `/v1`。
- **提示模型不存在或无权限**：在 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 核对模型 ID 是否包含在当前密钥绑定的分组中，更多排错见 [通用常见问题](/zh-cn/faq/)。
