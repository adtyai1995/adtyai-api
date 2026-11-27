# Kimi Code 接入指南

**Kimi Code CLI**（`kimi-cli`）是月之暗面推出的终端智能编程助手，支持 Shell 执行、代码库检索与 MCP 扩展。**ADTY AI** 同时提供 OpenAI 兼容接口与 Anthropic 兼容接口，使用同一把 API Key 即可接入 `kimi-k2.5` 或平台定价页中的其他模型。

::: warning 按协议填写正确的 Base URL
- **OpenAI 兼容协议**：`https://api.adtyai.com/v1`
- **Anthropic 兼容协议**：`https://api.adtyai.com`（*不带 `/v1`*）
- 切勿填写网页控制台域名 `https://www.adtyai.com`，否则会返回 **HTTP 403**。
:::

## 前置准备

1. 本地已安装 **Python 3.12+** 或 **uv**（推荐使用 `uv` 进行隔离安装）。
2. 在 [https://www.adtyai.com](https://www.adtyai.com) 的 **令牌（API Tokens）** 页面创建一把 API 密钥（`sk-...`）。
3. 前往 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 确认当前令牌分组下实时可用的模型 ID（例如 `kimi-k2.5` 或其他编程模型）。

## 1. 安装 Kimi Code CLI

### 方式一：使用 `uv` 安装（推荐）

如果尚未安装 `uv`，先执行安装脚本：

```bash
# macOS / Linux
curl -LsSf https://astral.sh/uv/install.sh | sh

# Windows（PowerShell）
powershell -ExecutionPolicy ByPass -c "irm https://astral.sh/uv/install.ps1 | iex"
```

接着以全局独立工具方式安装 `kimi-cli`：

```bash
uv tool install --python 3.13 kimi-cli
```

### 方式二：使用 `pipx` / `pip` 安装

```bash
pipx install kimi-cli
# 或在独立虚拟环境中安装：
pip install --upgrade kimi-cli
```

验证命令是否已进入 `PATH`：

```bash
kimi --version
```

## 2. 配置 `~/.kimi/config.toml`

Kimi Code 默认读取以下路径的配置文件：
- **macOS / Linux**：`~/.kimi/config.toml`
- **Windows**：`%USERPROFILE%\.kimi\config.toml`

### OpenAI 兼容协议配置（推荐，适用于 Kimi 及多模型路由）

创建或编辑 `~/.kimi/config.toml`，将 `base_url` 指向 `https://api.adtyai.com/v1`：

```toml
default_model = "gateway-kimi"

[providers.gateway_openai]
type = "openai_legacy"
base_url = "https://api.adtyai.com/v1"
api_key = "sk-your-api-key"

[models.gateway-kimi]
provider = "gateway_openai"
model = "kimi-k2.5"
max_context_size = 262144
```

::: tip 配置多个模型别名
你可以在同一份配置中追加多个 `[models.<别名>]` 区块复用 `[providers.gateway_openai]`（或下方的 Anthropic 供应商），启动时用 `kimi --model <别名>` 快速切换。具体模型 ID 请以 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 为准。
:::

### Anthropic 兼容协议配置（备选）

如果你希望 Kimi Code 通过 Anthropic Messages 协议调用（例如配合 Claude 模型或 Anthropic 兼容链路），请使用 `https://api.adtyai.com`（**不要**带 `/v1`）：

```toml
default_model = "gateway-claude"

[providers.gateway_anthropic]
type = "anthropic"
base_url = "https://api.adtyai.com"
api_key = "sk-your-api-key"

[models.gateway-claude]
provider = "gateway_anthropic"
model = "claude-sonnet-4-6"
max_context_size = 200000
```

### 环境变量快速覆盖（临时测试）

如果希望不改动配置文件直接临时测试：

**macOS / Linux**：

```bash
export KIMI_BASE_URL="https://api.adtyai.com/v1"
export KIMI_API_KEY="sk-your-api-key"
export KIMI_MODEL_NAME="kimi-k2.5"
```

**Windows（PowerShell）**：

```powershell
$env:KIMI_BASE_URL="https://api.adtyai.com/v1"
$env:KIMI_API_KEY="sk-your-api-key"
$env:KIMI_MODEL_NAME="kimi-k2.5"
```

若使用 Anthropic 兼容环境变量模式，请设置 `ANTHROPIC_BASE_URL="https://api.adtyai.com"` 与 `ANTHROPIC_AUTH_TOKEN="sk-your-api-key"`。

## 3. 快速验证

进入工作目录并启动 Kimi Code：

```bash
cd /path/to/your/project
kimi
```

或者运行一条非交互式命令测试连通性：

```bash
kimi --print -p "只回复 OK"
```

在交互会话中可输入 `/model` 或 `/setup` 检查当前模型状态，并在 [https://www.adtyai.com](https://www.adtyai.com) 的 **使用日志（Usage Logs）** 中核对是否已正常计费与返回。

## 常见问题排查

- **提示模型不存在或无可用渠道**：核对 `kimi-k2.5`（或所填模型 ID）是否列在 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 中，且当前 API Key 所属分组支持该模型。
- **返回 HTTP 403**：检查 `base_url` 是否误填了网页控制台域名 `https://www.adtyai.com`，请改为 `https://api.adtyai.com`。
- **返回 HTTP 404**：确认 OpenAI 兼容模式带 `/v1`，Anthropic 模式不带 `/v1`。
- 更多排查项请查阅 [通用常见问题](/zh-cn/faq/) 或 [基础环境准备](/zh-cn/integrations/cli/environment)。
