# Claude Code 调用 DeepSeek

借助 **ADTY AI** 的 Anthropic Messages 协议兼容能力（`https://api.adtyai.com`），你可以直接在 **Claude Code** 中调用高性价比的 **DeepSeek** 模型完成代码阅读、批量重构与日常研发任务。

## 1. 接入前准备

1. 确认已安装 Claude Code（参考 [Claude Code 接入指南](/zh-cn/integrations/cli/claude-code)）。
2. 前往 [ADTY AI 模型价格页](https://www.adtyai.com/pricing)，确认目标分组下已启用的精确 DeepSeek `<model-id>`（通常为 `deepseek-chat`、`deepseek-reasoner`，或价格页标注的带版本号 ID）。
3. 在 [ADTY AI 控制台](https://www.adtyai.com) 创建绑定到该分组的 API 密钥。

## 2. 配置 `~/.claude/settings.json`

编辑 `~/.claude/settings.json`（Windows 路径为 `%USERPROFILE%\.claude\settings.json`），配置接口地址、密钥、DeepSeek 模型映射以及超时与最大输出 Token 参数：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>",
    "ANTHROPIC_MODEL": "deepseek-chat",
    "ANTHROPIC_SMALL_FAST_MODEL": "deepseek-chat",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-chat",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "deepseek-reasoner",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "deepseek-chat",
    "API_TIMEOUT_MS": "600000",
    "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "8192",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  }
}
```

### 核心参数说明

| 环境变量 | 建议取值 | 作用说明 |
| --- | --- | --- |
| `ANTHROPIC_BASE_URL` | `https://api.adtyai.com` | Anthropic 原生根地址，不带 `/v1` |
| `ANTHROPIC_MODEL` | `deepseek-chat` 或 `deepseek-reasoner` | 交互式编程默认模型（以价格页精确 ID 为准） |
| `ANTHROPIC_SMALL_FAST_MODEL` | `deepseek-chat` | 防止 Claude Code 后台辅助任务请求分组中不存在的 Claude Haiku 模型 |
| `API_TIMEOUT_MS` | `600000`（10 分钟） | 避免 `deepseek-reasoner` 在长思考链路期间因客户端超时中断 |
| `CLAUDE_CODE_MAX_OUTPUT_TOKENS` | `8192` | 防止 Claude Code 默认 `max_tokens` 超出 DeepSeek 模型单次输出上限而触发 `400` 报错 |

::: tip `deepseek-chat` 与 `deepseek-reasoner` 如何选？
- **日常编码首选 `deepseek-chat`**：响应更快，在多轮文件读写、补丁编辑和命令调用中工具调用格式稳定。
- **深度推理按需切换 `deepseek-reasoner`**：适合复杂 Bug 定位与方案设计，会话中可直接输入 `/model deepseek-reasoner` 切换；请先在 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 确认所选分组下的当前推理模型版本是否支持带工具调用的推理模式。
:::

## 3. 验证工具调用

进入测试项目目录并启动 Claude Code：

```bash
claude
```

按下面两步完成验证：

1. 输入 `/status`，确认接口地址为 `https://api.adtyai.com`，当前生效模型为配置的 DeepSeek `<model-id>`。
2. 发送一条真实工具调用指令：

   ```text
   列出当前目录文件，读取 README.md（或 package.json），用两句话总结项目用途，不要修改任何文件。
   ```

确认 Claude Code 能够顺利触发目录与文件读取工具、正确解析工具结果并完整输出总结，且没有 JSON 格式错误或中途断流。

## 4. 多方案快速切换

如果你习惯在关键架构任务中使用原生 Claude，在高频批量修改时切换到 DeepSeek，建议使用 [CC Switch — 配置 Claude Code](/zh-cn/integrations/cc-switch/claude-code) 分别保存两套配置文件，随时一键切换而无需反复手工编辑 `~/.claude/settings.json`。

## 常见问题排查

- **报 `400 Bad Request` 且提示 `max_tokens` 超限**：调低 `CLAUDE_CODE_MAX_OUTPUT_TOKENS`（例如改为 `8192` 或 `4096`），确保不超过当前 DeepSeek 模型的最大输出限制。
- **报错提示找不到 `claude-haiku-...`**：检查 `ANTHROPIC_SMALL_FAST_MODEL` 与 `ANTHROPIC_DEFAULT_HAIKU_MODEL` 是否都已改为 `deepseek-chat`（或当前分组启用的 DeepSeek 模型 ID）。
- **深度思考过程中连接中断**：确认 `API_TIMEOUT_MS` 已设为 `"600000"`，并检查本地代理是否存在过短（如 60 秒）的空闲断连策略。
- **更多排错参考**：请查阅 [渠道可用性与分组选择](/zh-cn/guides/channel-status) 与 [Claude Code 排查](/zh-cn/faq/claude-code)。
