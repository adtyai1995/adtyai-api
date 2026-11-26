# Grok Build CLI 接入指南

**Grok Build CLI** 是面向 xAI Grok 模型的终端编程工具，底层采用 OpenAI 兼容的 `/v1` 接口规范。将 `XAI_BASE_URL`（或 CLI 配置文件中的 OpenAI 兼容地址）指向 **ADTY AI** 后，即可直接使用你的 **ADTY AI** 余额与 API Key 调用 Grok 编程能力。

::: warning 接口地址必须指向 API 域名并带 `/v1`
请将 Grok Build CLI 指向 `https://api.adtyai.com/v1`：
- **正确**：`https://api.adtyai.com/v1`
- **错误**：`https://www.adtyai.com`（网页控制台域名会直接返回 **HTTP 403**）
:::

## 前置准备

1. 已安装 **Node.js 20+ LTS**（详见 [基础环境准备](/zh-cn/integrations/cli/environment)）。
2. 在 [https://www.adtyai.com](https://www.adtyai.com) 的 **令牌（API Tokens）** 页面创建一把 API 密钥（`sk-...`），并选择包含 Grok 模型的分组。
3. 前往 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 搜索 `grok`，确认当前在线的确切模型 ID（如 `grok-code-fast-1`、`grok-4` 等）。

## 1. 安装 Grok Build CLI

通过 npm 全局安装 CLI（或按官方发行方式安装对应版本）：

```bash
npm install -g @vibe-kit/grok-cli
# 若使用官方发布的独立包，请以对应包名为准，例如：
# npm install -g @xai/grok-cli
```

验证安装结果：

```bash
grok --version
```

::: tip 检查所安装版本的参数名称
不同发行版的 Grok CLI 在命令行参数与配置目录上可能略有差异。建议先运行 `grok --help` 查看当前版本支持的参数（如 `--base-url`、`--api-key`、`--model`）。
:::

## 2. 配置环境变量

Grok Build CLI 支持标准 xAI 环境变量（`XAI_BASE_URL`、`XAI_API_KEY`），部分版本同时兼容 `GROK_BASE_URL` / `GROK_API_KEY`。

### macOS / Linux（`~/.zshrc` 或 `~/.bashrc`）

```bash
export XAI_BASE_URL="https://api.adtyai.com/v1"
export XAI_API_KEY="sk-your-api-key"

# 可选：兼容读取 GROK_* 变量的 CLI 构建版本
export GROK_BASE_URL="https://api.adtyai.com/v1"
export GROK_API_KEY="sk-your-api-key"
```

刷新配置：

```bash
source ~/.zshrc   # Bash 用户执行：source ~/.bashrc
```

### Windows（PowerShell）

**当前窗口临时生效**：

```powershell
$env:XAI_BASE_URL="https://api.adtyai.com/v1"
$env:XAI_API_KEY="sk-your-api-key"
$env:GROK_BASE_URL="https://api.adtyai.com/v1"
$env:GROK_API_KEY="sk-your-api-key"
```

**永久写入当前用户环境**：

```powershell
[Environment]::SetEnvironmentVariable("XAI_BASE_URL", "https://api.adtyai.com/v1", "User")
[Environment]::SetEnvironmentVariable("XAI_API_KEY", "sk-your-api-key", "User")
[Environment]::SetEnvironmentVariable("GROK_BASE_URL", "https://api.adtyai.com/v1", "User")
[Environment]::SetEnvironmentVariable("GROK_API_KEY", "sk-your-api-key", "User")
```

写入后重新打开 PowerShell 窗口使变量生效。

## 3. 持久化配置文件

除环境变量外，你也可以在 `~/.grok/user-settings.json`（Windows 为 `%USERPROFILE%\.grok\user-settings.json`）中保存长期配置：

```json
{
  "apiKey": "sk-your-api-key",
  "baseURL": "https://api.adtyai.com/v1",
  "defaultModel": "grok-code-fast-1",
  "models": [
    "grok-code-fast-1",
    "grok-4"
  ]
}
```

macOS / Linux 建议收紧配置文件权限：

```bash
chmod 600 ~/.grok/user-settings.json
```

## 4. 选择 Grok 模型

Grok 模型列表会随上游版本持续更新，请先在 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 页面核对：
- 实时可用的准确模型名称（例如 `grok-code-fast-1`、`grok-4` 等）
- 支持该模型的令牌分组（参考 [渠道可用性与分组选择](/zh-cn/guides/channel-status)）

启动时可按需指定模型：

```bash
grok --base-url https://api.adtyai.com/v1 --model grok-code-fast-1
```

在交互会话中也可通过 `/models` 或对应命令切换模型。

## 5. 验证使用

进入工作目录并执行一条单次测试请求：

```bash
cd /path/to/your/project
grok -p "只回复 OK"
```

验证通过后即可进入交互模式：

```bash
grok
```

随后前往 [https://www.adtyai.com](https://www.adtyai.com) 的 **使用日志（Usage Logs）** 确认请求记录、所选模型与计费状态正常。

## 常见问题排查

- **HTTP 403 Forbidden**：检查 `XAI_BASE_URL` / `baseURL` 是否误填了网页控制台域名 `https://www.adtyai.com`，必须改为 `https://api.adtyai.com/v1`。
- **HTTP 404 或地址无效**：确认地址末尾包含 `/v1`。
- **提示模型不存在或无可用渠道**：核对 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 上的实时模型名称，并确认当前 API Key 所属分组包含该模型。
- 也可以通过 [OpenCode](/zh-cn/integrations/advanced/opencode) 或 [Cherry Studio](/zh-cn/integrations/advanced/cherry-studio) 接入 Grok 模型，更多问题详见 [通用常见问题](/zh-cn/faq/)。
