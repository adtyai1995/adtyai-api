# Codex 调用 DeepSeek

你可以在 **Codex CLI** 中通过自定义供应商配置，将请求指向 **ADTY AI**（`https://api.adtyai.com/v1`），直接调用 `deepseek-chat`、`deepseek-reasoner` 或 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 中启用的精确 DeepSeek 模型 ID。

## 1. 接入前准备

1. 安装 Codex CLI（参考 [Codex CLI 接入指南](/zh-cn/integrations/cli/codex)）。
2. 在 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 核对当前分组支持的精确 DeepSeek `<model-id>`。
3. 在 [ADTY AI 控制台](https://www.adtyai.com) 创建可用 API 密钥。

## 2. 配置 `~/.codex/config.toml`

新建或编辑 `~/.codex/config.toml`（Windows 路径为 `%USERPROFILE%\.codex\config.toml`）：

```toml
model_provider = "adtyai_deepseek"
model = "deepseek-chat"

[model_providers.adtyai_deepseek]
name = "ADTY AI (DeepSeek)"
base_url = "https://api.adtyai.com/v1"
wire_api = "chat"
requires_openai_auth = true
```

如需改用推理模型，将 `model` 替换为：

```toml
model = "deepseek-reasoner"
```

### 保留 GPT 为默认、通过 Profile 按需调用 DeepSeek

如果你平时默认使用 GPT 模型，可以在 `~/.codex/config.toml` 中额外增加一个名为 `deepseek` 的配置档，需要时通过 `codex --profile deepseek` 启动：

```toml
[model_providers.adtyai_deepseek]
name = "ADTY AI (DeepSeek)"
base_url = "https://api.adtyai.com/v1"
wire_api = "chat"
env_key = "ADTYAI_DEEPSEEK_API_KEY"

[profiles.deepseek]
model_provider = "adtyai_deepseek"
model = "deepseek-chat"
```

## 3. `wire_api = "chat"` 与 `wire_api = "responses"` 如何选择？

在 Codex CLI 中调用 DeepSeek 时，正确配置 `wire_api` 非常关键：

| `wire_api` 取值 | 实际请求接口 | 适用场景说明 |
| --- | --- | --- |
| `"chat"` **（DeepSeek 默认推荐）** | `https://api.adtyai.com/v1/chat/completions` | 兼容几乎所有 DeepSeek 渠道，流式输出与工具调用最稳定 |
| `"responses"` | `https://api.adtyai.com/v1/responses` | **仅当**你的 Codex CLI 版本要求使用 OpenAI Responses 协议，**且**所选 ADTY AI DeepSeek 渠道明确支持 `/v1/responses` 协议转换时再启用 |

::: warning 遇到 `/v1/responses` 报 `400` / `404` / `501` 怎么处理？
如果运行时提示 `/v1/responses` 不被当前渠道支持，请将 `wire_api` 改为 `"chat"` 后重试。若你本地安装的较新 Codex CLI 版本已默认强制走 Responses 协议，请先在 [渠道可用性与分组选择](/zh-cn/guides/channel-status) 确认目标渠道是否支持 Responses 转换，或切换至保留 `wire_api = "chat"` 支持的 Codex CLI 版本。
:::

## 4. 配置密钥（`~/.codex/auth.json` 或环境变量）

当 `~/.codex/config.toml` 中设置了 `requires_openai_auth = true` 时，请在 `~/.codex/auth.json`（Windows 路径为 `%USERPROFILE%\.codex\auth.json`）中写入 ADTY AI 密钥：

```json
{
  "OPENAI_API_KEY": "<YOUR_API_KEY>"
}
```

若上面使用了独立环境变量 `env_key = "ADTYAI_DEEPSEEK_API_KEY"`，则在终端中导出该变量即可：

::: code-group

```bash [macOS / Linux]
export ADTYAI_DEEPSEEK_API_KEY="<YOUR_API_KEY>"
```

```powershell [Windows（PowerShell）]
[Environment]::SetEnvironmentVariable("ADTYAI_DEEPSEEK_API_KEY", "<YOUR_API_KEY>", "User")
```

:::

## 5. 验证执行效果

先用非交互模式快速验证鉴权与模型响应：

```bash
codex exec "请回复 OK 并告诉我当前所在工作目录。"
```

如果配置了命名 Profile，则执行：

```bash
codex --profile deepseek exec "请回复 OK 并列出当前目录的顶层文件。"
```

验证通过后，即可运行 `codex`（或 `codex --profile deepseek`）进入交互式研发会话，测试代码读取与修改。

## 常见问题排查

- **请求 `/v1/responses` 返回 `404` 或不支持错误**：确认 `[model_providers.adtyai_deepseek]` 中已设置 `wire_api = "chat"`。
- **`401 Unauthorized`**：检查 `~/.codex/auth.json` 是否为合法 JSON 且键名为 `"OPENAI_API_KEY"`（或确认配置的 `env_key` 已在当前终端生效）。
- **`model_not_found`**：在 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 核对 `deepseek-chat` / `deepseek-reasoner` 是否在当前密钥分组中启用。
- **希望图形化管理多套 Codex 配置？** 请查看 [CC Switch — 配置 Codex CLI](/zh-cn/integrations/cc-switch/codex)，更多排错请查阅 [Codex 排查](/zh-cn/faq/codex)。
