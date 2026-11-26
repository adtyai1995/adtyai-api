# OpenClaw

OpenClaw 是支持高度扩展的本地 AI Agent 平台，通过 `~/.openclaw/openclaw.json` 管理自定义模型供应商。你可以通过 OpenAI Completions（`openai-completions`）或 Anthropic Messages（`anthropic-messages`）协议将 **ADTY AI** 接入 OpenClaw。

## 1. 安装 OpenClaw

请先确保本地已安装 Node.js 22 及以上版本（参考 [基础环境准备](/zh-cn/integrations/cli/environment)），然后全局安装 OpenClaw：

```bash
npm install -g openclaw@latest
openclaw --version
```

## 2. 配置 `~/.openclaw/openclaw.json`

创建或编辑 `~/.openclaw/openclaw.json`（Windows 路径为 `%USERPROFILE%\.openclaw\openclaw.json`），在 `models.providers` 中声明自定义供应商。

### 方式一：OpenAI 兼容协议（`openai-completions`）

适合 GPT、DeepSeek、Gemini 及所有 OpenAI 兼容模型，使用 `https://api.adtyai.com/v1` 与 `api: "openai-completions"`：

```json
{
  "models": {
    "mode": "merge",
    "providers": {
      "gateway": {
        "baseUrl": "https://api.adtyai.com/v1",
        "apiKey": "<YOUR_API_KEY>",
        "api": "openai-completions",
        "models": [
          {
            "id": "gpt-5.4",
            "name": "GPT-5.4",
            "reasoning": true,
            "input": ["text", "image"],
            "contextWindow": 272000,
            "maxTokens": 32768
          },
          {
            "id": "deepseek-chat",
            "name": "DeepSeek Chat",
            "reasoning": false,
            "input": ["text"],
            "contextWindow": 128000,
            "maxTokens": 8192
          }
        ]
      }
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "gateway/gpt-5.4",
        "fallbacks": ["gateway/deepseek-chat"]
      }
    }
  }
}
```

### 方式二：Anthropic Messages 协议（`anthropic-messages`）

如需通过原生 Anthropic Messages 协议调用 Claude 模型，可配置 `api: "anthropic-messages"`：

```json
{
  "models": {
    "mode": "merge",
    "providers": {
      "gateway-claude": {
        "baseUrl": "https://api.adtyai.com/v1",
        "apiKey": "<YOUR_API_KEY>",
        "api": "anthropic-messages",
        "models": [
          {
            "id": "claude-sonnet-4-6",
            "name": "Claude Sonnet 4.6",
            "reasoning": true,
            "input": ["text", "image"],
            "contextWindow": 200000,
            "maxTokens": 16384
          },
          {
            "id": "claude-opus-4-6",
            "name": "Claude Opus 4.6",
            "reasoning": true,
            "input": ["text", "image"],
            "contextWindow": 200000,
            "maxTokens": 16384
          }
        ]
      }
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "gateway-claude/claude-sonnet-4-6"
      }
    }
  }
}
```

::: info 关于 `anthropic-messages` 的路径说明
不同版本的 OpenClaw 底层 Anthropic 客户端拼接路径略有差异：有的版本会直接拼接 `/messages`（需要填 `https://api.adtyai.com/v1`），有的版本会拼接 `/v1/messages`（需要填 `https://api.adtyai.com`）。如果测试对话返回 `404 Not Found`，请在这两种写法之间切换后重试。
:::

::: tip 避免在配置文件中明文保存密钥
OpenClaw 支持在 `apiKey` 中使用环境变量占位符（例如 `"${GATEWAY_API_KEY}"`），并从 `~/.openclaw/.env` 或系统环境中读取。更多密钥隔离建议见 [账号安全与访问令牌](/zh-cn/guides/security)。
:::

## 3. 声明模型列表与选择默认模型

- **精确填写模型 ID**：`models` 数组中每一项的 `id` 必须与 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 中当前密钥分组可用的模型 ID 一致。
- **供应商前缀格式**：在 `agents.defaults.model.primary` 或命令行中引用模型时，需要带上供应商名称前缀（例如 `gateway/<model-id>` 或 `gateway-claude/<model-id>`）。
- **通过命令行查看与切换默认模型**：

  ```bash
  openclaw models list
  openclaw models set gateway/gpt-5.4
  ```

## 4. 测试对话

先运行配置自检，再发送一条本地测试消息：

```bash
openclaw doctor
openclaw agent --message "你好，请用简短的一句话确认 API 连接正常。"
```

也可以直接启动交互式终端界面进行多轮对话测试：

```bash
openclaw tui
```

如果本地以后台网关服务模式运行 OpenClaw，在修改 `~/.openclaw/openclaw.json` 后请重启网关服务以加载最新配置。

## 常见问题排查

- **未识别自定义供应商**：确认 `models.mode` 已设为 `"merge"`，并检查 JSON 文件不存在多余逗号等语法错误。
- **持续报错 `401`**：检查 [https://www.adtyai.com](https://www.adtyai.com) 中对应 API 密钥是否处于启用状态、余额充足，且复制时没有混入多余空格。
- **提示模型不可用**：对照 [渠道可用性与分组选择](/zh-cn/guides/channel-status) 与 [ADTY AI 模型价格页](https://www.adtyai.com/pricing)，确认密钥所属分组包含所声明的模型 ID。
- **进一步排错**：参考 [API 参考](/zh-cn/api/) 与 [通用常见问题](/zh-cn/faq/)。
