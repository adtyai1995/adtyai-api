# OpenCode

[OpenCode](https://opencode.ai) 是基于 Vercel AI SDK 构建的开源终端编码 Agent。它原生支持自定义供应商配置，你可以将 **ADTY AI** 通过 OpenAI 兼容协议（`@ai-sdk/openai-compatible`）或 Anthropic 协议（`@ai-sdk/anthropic`）接入 OpenCode，也可以在同一个配置文件中同时启用两套供应商。

## 1. 安装 OpenCode

根据操作系统选择任一种方式安装：

::: code-group

```bash [官方安装脚本（macOS / Linux）]
curl -fsSL https://opencode.ai/install | bash
```

```bash [npm（全平台）]
npm install -g opencode-ai
```

```bash [Homebrew（macOS / Linux）]
brew install anomalyco/tap/opencode
```

:::

验证安装成功：

```bash
opencode --version
```

## 2. 使用环境变量保管 API 密钥

建议将从 [ADTY AI 控制台](https://www.adtyai.com) 获取的 API 密钥写入环境变量 `GATEWAY_API_KEY`，避免把明文密钥直接写进配置文件：

::: code-group

```bash [macOS / Linux（zsh / bash）]
echo 'export GATEWAY_API_KEY="<YOUR_API_KEY>"' >> ~/.zshrc
source ~/.zshrc
```

```powershell [Windows（PowerShell）]
[Environment]::SetEnvironmentVariable("GATEWAY_API_KEY", "<YOUR_API_KEY>", "User")
```

:::

在 Windows 上设置永久环境变量后，请重新打开一个新的终端窗口使其生效。

## 3. 配置 `~/.config/opencode/opencode.json`

创建或编辑 `~/.config/opencode/opencode.json`（Windows 路径为 `%USERPROFILE%\.config\opencode\opencode.json`）。OpenCode 在运行时会自动解析 `{env:GATEWAY_API_KEY}`。

::: tip OpenCode 中两类协议均需填写 `https://api.adtyai.com/v1`
与 Anthropic 官方 Python/TypeScript SDK 不同，Vercel 的 `@ai-sdk/anthropic` 会直接在 `baseURL` 后拼接 `/messages`。因此，在 OpenCode 中无论是 `@ai-sdk/openai-compatible` 还是 `@ai-sdk/anthropic`，`baseURL` 都应填写 `https://api.adtyai.com/v1`。
:::

### 方式一：OpenAI 兼容协议（`@ai-sdk/openai-compatible`）

适合调用 GPT、DeepSeek、Gemini 及所有通过 OpenAI Chat Completions 暴露的模型：

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "gateway": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "ADTY AI",
      "options": {
        "baseURL": "https://api.adtyai.com/v1",
        "apiKey": "{env:GATEWAY_API_KEY}"
      },
      "models": {
        "gpt-5.4": {
          "name": "GPT-5.4",
          "limit": {
            "context": 272000,
            "output": 32768
          }
        },
        "deepseek-chat": {
          "name": "DeepSeek Chat",
          "limit": {
            "context": 128000,
            "output": 8192
          }
        }
      }
    }
  },
  "model": "gateway/gpt-5.4"
}
```

### 方式二：Anthropic 协议（`@ai-sdk/anthropic`）

适合调用 Claude 系列模型，保留原生 Anthropic Messages 特性（如提示词缓存和扩展思考）：

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "gateway-anthropic": {
      "npm": "@ai-sdk/anthropic",
      "name": "ADTY AI (Anthropic)",
      "options": {
        "baseURL": "https://api.adtyai.com/v1",
        "apiKey": "{env:GATEWAY_API_KEY}"
      },
      "models": {
        "claude-sonnet-4-6": {
          "name": "Claude Sonnet 4.6",
          "limit": {
            "context": 200000,
            "output": 16384
          }
        },
        "claude-opus-4-6": {
          "name": "Claude Opus 4.6",
          "limit": {
            "context": 200000,
            "output": 16384
          }
        }
      }
    }
  },
  "model": "gateway-anthropic/claude-sonnet-4-6"
}
```

### 在同一配置文件中同时声明两套供应商

你可以在 `"provider"` 下同时保留 `gateway` 与 `gateway-anthropic`（若两套模型位于不同分组，也可分别引用 `{env:GATEWAY_OPENAI_KEY}` 与 `{env:GATEWAY_CLAUDE_KEY}`）。填写模型键名时，请以 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 展示的精确 `<model-id>` 为准。

## 4. 使用 `opencode` 验证

1. 进入任意项目目录，启动 OpenCode：

   ```bash
   opencode
   ```

2. 在交互界面中输入 `/models`，选择已配置的 `gateway/<model-id>` 或 `gateway-anthropic/<model-id>`。
3. 也可以在终端直接执行单次连通性测试：

   ```bash
   opencode run "请用一句话确认 API 已正常连通。"
   ```

## 常见问题排查

- **Anthropic 协议返回 `404 Not Found`**：请确认 `baseURL` 已包含 `/v1`（即 `https://api.adtyai.com/v1`），而不是省略了 `/v1`。
- **`401 Unauthorized` 或读取不到密钥**：在启动 `opencode` 的同一终端里执行 `echo $GATEWAY_API_KEY`（PowerShell 执行 `$env:GATEWAY_API_KEY`），确认能输出完整的 `sk-...` 密钥。
- **`model_not_found`**：请检查 `models` 中的键名是否与 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 上当前密钥所属分组支持的模型 ID 完全一致。
- **更多排查思路**：请查看 [渠道可用性与分组选择](/zh-cn/guides/channel-status) 与 [通用常见问题](/zh-cn/faq/)。
