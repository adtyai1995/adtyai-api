# Codex CLI 接入指南

**OpenAI Codex CLI** 是 OpenAI 官方开源的终端编程助手，底层采用 **Responses API**（`/v1/responses`）协议。**ADTY AI** 原生支持 Responses 协议，可直接提供完整的工具执行、流式推理以及可调节推理强度（reasoning effort）体验。

::: warning 接口地址必须带 `/v1` 且使用 API 域名
配置 Codex CLI 时，`base_url` 必须填写 `https://api.adtyai.com/v1`。
- 正确：`https://api.adtyai.com/v1`（包含 `/v1` 后缀）
- 错误：`https://www.adtyai.com`（网页控制台域名会直接返回 **HTTP 403**）
:::

## 前置准备

1. 已安装 **Node.js 20+ LTS**（详见 [基础环境准备](/zh-cn/integrations/cli/environment)）。
2. 在 [https://www.adtyai.com](https://www.adtyai.com) 的 **令牌（API Tokens）** 页面创建一把支持 OpenAI / Codex 分组的 API 密钥（以 `sk-` 开头）。最新可用模型列表请查看 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing)。

## 1. 安装 Codex CLI

通过 npm 全局安装官方包：

```bash
npm install -g @openai/codex
```

验证安装结果：

```bash
codex --version
```

## 2. 写入 `config.toml` 与 `auth.json`

Codex CLI 会从用户主目录下的 `.codex` 文件夹读取配置与密钥：

- **macOS / Linux**：`~/.codex/config.toml` 与 `~/.codex/auth.json`
- **Windows**：`$HOME\.codex\config.toml` 与 `$HOME\.codex\auth.json`（通常为 `C:\Users\<你的用户名>\.codex\`）

### 第一步：编写 `config.toml`

创建 `~/.codex/config.toml`（Windows 为 `$HOME\.codex\config.toml`）：

```toml
model_provider = "gateway"
model = "gpt-5.4"
model_reasoning_effort = "medium"
disable_response_storage = true

[model_providers.gateway]
name = "ADTY AI"
base_url = "https://api.adtyai.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

关键字段说明：

| 参数 | 说明 |
| --- | --- |
| `model_provider` | 指定启用的供应商块名称（对应下方 `[model_providers.gateway]`）。 |
| `model` | 默认模型 ID，请以 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 上实时展示的 OpenAI / Codex 模型为准。 |
| `model_reasoning_effort` | 控制推理强度，可选 `"low"`、`"medium"`、`"high"`（部分模型支持 `"xhigh"`）。档位越高推理越深，但耗时与 Token 消耗也相应增加。 |
| `disable_response_storage` | 关闭服务端状态持久化存储依赖，提升中转链路兼容性。 |
| `base_url` | 填写 `https://api.adtyai.com/v1`。 |
| `wire_api` | 填写 `"responses"`，启用新版 Codex 使用的 OpenAI Responses 协议。 |
| `requires_openai_auth` | 指示 Codex CLI 从 `auth.json`（或环境变量 `OPENAI_API_KEY`）读取鉴权密钥。 |

### 第二步：编写 `auth.json`

创建 `~/.codex/auth.json`（Windows 为 `$HOME\.codex\auth.json`）：

```json
{
  "OPENAI_API_KEY": "sk-your-api-key"
}
```

### 一键写入命令

**macOS / Linux**：

```bash
mkdir -p ~/.codex

cat << 'EOF' > ~/.codex/config.toml
model_provider = "gateway"
model = "gpt-5.4"
model_reasoning_effort = "medium"
disable_response_storage = true

[model_providers.gateway]
name = "ADTY AI"
base_url = "https://api.adtyai.com/v1"
wire_api = "responses"
requires_openai_auth = true
EOF

cat << 'EOF' > ~/.codex/auth.json
{
  "OPENAI_API_KEY": "sk-your-api-key"
}
EOF

chmod 600 ~/.codex/auth.json
```

**Windows（PowerShell）**：

```powershell
$codexDir = Join-Path $HOME ".codex"
New-Item -ItemType Directory -Force -Path $codexDir | Out-Null

@'
model_provider = "gateway"
model = "gpt-5.4"
model_reasoning_effort = "medium"
disable_response_storage = true

[model_providers.gateway]
name = "ADTY AI"
base_url = "https://api.adtyai.com/v1"
wire_api = "responses"
requires_openai_auth = true
'@ | Set-Content -Path (Join-Path $codexDir "config.toml") -Encoding UTF8

@'
{
  "OPENAI_API_KEY": "sk-your-api-key"
}
'@ | Set-Content -Path (Join-Path $codexDir "auth.json") -Encoding UTF8
```

::: tip 推荐使用 CC Switch 管理多套配置
若希望可视化切换多个账号、分组或同时配置 Codex 桌面端，请查看 [CC Switch — 配置 Codex CLI](/zh-cn/integrations/cc-switch/codex) 与 [CC Switch — 配置 Codex 桌面应用](/zh-cn/integrations/cc-switch/codex-app)。
:::

## 3. 启动并验证 `codex`

进入项目目录，先用非交互式命令快速验证链路：

```bash
cd /path/to/your/project
codex exec "仅回复 OK 两个字母"
```

返回正常后即可启动交互式终端会话：

```bash
codex
```

如需临时覆盖模型或推理强度，无需修改 `config.toml`，可在命令行直接传参：

```bash
codex -m gpt-5.4 -c model_reasoning_effort="high"
```

测试完成后，可在 [https://www.adtyai.com](https://www.adtyai.com) 的 **使用日志（Usage Logs）** 中核对调用记录与消耗明细。

## 相关文档与排查

- **遇到 401、403、`wire_api` 不匹配或模型不可用**：查阅 [Codex 排查](/zh-cn/faq/codex)。
- **图形化配置与切换**：查阅 [CC Switch — 配置 Codex CLI](/zh-cn/integrations/cc-switch/codex)。
- **在 Codex 中使用 DeepSeek 模型**：查阅 [Codex 调用 DeepSeek](/zh-cn/integrations/advanced/deepseek-codex)。
- **更多通用问题**：查阅 [通用常见问题](/zh-cn/faq/)。
