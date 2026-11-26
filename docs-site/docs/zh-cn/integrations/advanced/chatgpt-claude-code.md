# Claude Code 调用 ChatGPT

**ADTY AI** 在支持的渠道分组上提供 Anthropic Messages 跨协议兼容能力。你可以继续使用 **Claude Code** 熟悉的终端交互体验（文件修改、命令行工具执行、代码 Diff 审阅与斜杠命令），同时将底层推理模型路由到 OpenAI GPT 系列模型。

## 1. 接入前准备

1. 确保本地已安装 Claude Code（参考 [Claude Code 接入指南](/zh-cn/integrations/cli/claude-code)）。
2. 打开 [ADTY AI 模型价格页](https://www.adtyai.com/pricing)，确认以下两项信息：
   - 拟调用的精确 GPT `<model-id>`（例如 `gpt-5.4`、`gpt-5.3-codex`、`gpt-5-mini` 等，以价格页实时列表为准）。
   - 所选渠道分组已启用对应 GPT 模型且支持 Anthropic Messages 协议转换。
3. 在 [ADTY AI 控制台](https://www.adtyai.com) 创建绑定到该分组的 API 密钥。

## 2. 配置 `~/.claude/settings.json`

编辑 `~/.claude/settings.json`（Windows 路径为 `%USERPROFILE%\.claude\settings.json`），将 `ANTHROPIC_BASE_URL` 设为 `https://api.adtyai.com`，并通过 `ANTHROPIC_MODEL` 与 `ANTHROPIC_SMALL_FAST_MODEL` 指定目标 GPT 模型：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>",
    "ANTHROPIC_MODEL": "gpt-5.4",
    "ANTHROPIC_SMALL_FAST_MODEL": "gpt-5-mini",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "gpt-5.4",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "gpt-5.4",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "gpt-5-mini"
  }
}
```

::: tip 为什么必须同时配置 `ANTHROPIC_SMALL_FAST_MODEL`？
Claude Code 会把上下文压缩、标题摘要、轻量文件查询等后台辅助任务发送到小模型槽位。如果你的密钥分组仅包含 GPT 模型而未覆盖 `ANTHROPIC_SMALL_FAST_MODEL`（及 `ANTHROPIC_DEFAULT_HAIKU_MODEL`），后台请求会因尝试调用默认的 Claude Haiku ID 而报 `model_not_found`。将其一并指向可用的轻量 GPT 模型即可避免静默报错。
:::

### 临时切换模型

无需修改 `~/.claude/settings.json`，也可以在启动时或会话内随时指定模型：

```bash
# 启动时指定 GPT 模型
claude --model gpt-5.4
```

或在已打开的 Claude Code 会话中输入：

```text
/model gpt-5.4
```

::: tip 在原生 Claude 与 GPT 配置间快速切换
若日常需要频繁在原生 Claude 渠道与 GPT 渠道之间来回切换，推荐使用 [CC Switch — 配置 Claude Code](/zh-cn/integrations/cc-switch/claude-code) 管理多套独立配置方案，一键切换更省心。
:::

## 3. 验证调用效果

1. 在任意项目目录中启动 Claude Code：

   ```bash
   claude
   ```

2. 输入 `/status`，确认当前 API 地址为 `https://api.adtyai.com`，当前模型为指定的 GPT `<model-id>`。
3. 发送一条带工具调用的测试指令（例如 `读取 README.md 并用 3 条要点概括项目结构`），验证流式输出与文件读取工具是否均正常完成。

## 4. 跨协议差异与注意事项

由于 Claude Code 面向 Anthropic 原生协议设计，经 ADTY AI 转换至 OpenAI GPT 模型时存在少量行为差异：

- **扩展思考（Extended Thinking）参数差异**：Anthropic 的 `thinking` 预算参数与 OpenAI 的推理强度控制并非一一对应。若遇到不支持参数的报错，请避免强制开启 Anthropic 专属思考参数，或改用常规 GPT 模型 ID。
- **提示词缓存（Prompt Caching）统计差异**：Anthropic 使用显式 `cache_control` 断点，而 OpenAI 采用自动前缀缓存。即便上游自动前缀缓存已经生效，Claude Code 界面显示的缓存命中指标仍可能与原生 Claude 渠道存在差异。
- **Anthropic 专属 Beta 特性**：仅限 Anthropic 服务端的实验性工具或特定 `anthropic-beta` 请求头在跨协议路由中会被忽略或不受支持。
- **何时改用 Codex CLI**：如果你希望完整体验 OpenAI 原生 Responses API 与推理档位控制，避免任何协议转换损耗，建议直接使用 [Codex CLI](/zh-cn/integrations/cli/codex)。

## 常见问题排查

- **报 `model_not_found` 或 `403`**：核对 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 上当前密钥分组内的精确 GPT `<model-id>`。
- **会话压缩或摘要任务报错**：确认 `ANTHROPIC_SMALL_FAST_MODEL` 与 `ANTHROPIC_DEFAULT_HAIKU_MODEL` 均已覆盖为当前分组可用的 GPT 模型。
- **更多 Claude Code 排错**：参考 [Claude Code 排查](/zh-cn/faq/claude-code) 与 [渠道可用性与分组选择](/zh-cn/guides/channel-status)。
