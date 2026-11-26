# 在 CC Switch 中配置 Codex CLI

本文说明如何通过 **CC Switch** 将 **OpenAI Codex CLI** 接入 **ADTY AI**，并核对自动生成的 `config.toml` 与 `auth.json` 文件。

## 准备工作

- 已在本地安装 **Codex CLI**（安装步骤详见 [Codex CLI](/zh-cn/integrations/cli/codex)）。
- 已安装 **CC Switch**（详见 [CC Switch 简介与安装](/zh-cn/integrations/cc-switch/)）。
- 已在 [https://www.adtyai.com](https://www.adtyai.com) 创建支持 Codex 模型的 **ADTY AI** API Key（`sk-...`）。
- 已清理 Shell 中可能冲突的 `OPENAI_API_KEY` 或 `OPENAI_BASE_URL` 环境变量（详见 [通用基础配置](/zh-cn/integrations/cc-switch/common)）。

## 第一步：在 CC Switch 中添加 ADTY AI 供应商

1. 打开 **CC Switch**，在顶部切换到 **Codex** 标签页。
2. 点击 **添加供应商（+）**，选择 **自定义（Custom）**。
3. 按下表填写配置：

| 字段 | 填写内容 | 说明 |
| --- | --- | --- |
| **供应商名称** | `ADTY AI` | CC Switch 列表中的显示名称 |
| **API Base URL**（`base_url`） | `https://api.adtyai.com/v1` | **必须包含 `/v1`** |
| **API Key**（`OPENAI_API_KEY`） | `sk-your-api-key` | 粘贴你的 ADTY AI API Key |
| **协议类型**（`wire_api`） | `responses` | 新版 Codex CLI 必须使用 Responses 协议 |
| **默认模型**（`model`） | `gpt-5.4` | 或 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 中展示的其他可用 Codex 模型 |

::: important 务必保留 `/v1` 与 `wire_api = "responses"`
与 Claude Code 不同，Codex CLI 的 `base_url` 必须以 `/v1` 结尾（`https://api.adtyai.com/v1`），且协议类型需要设为 `wire_api = "responses"`。缺少 `/v1` 或误设为 `chat` 都会导致接口路径或协议不匹配报错。
:::

## 第二步：核对生成的 `config.toml` 与 `auth.json`

CC Switch 会同时维护 Codex 的主配置文件与凭据文件。保存前请在预览框确认内容与下方示例一致：

### `~/.codex/config.toml`

```toml
model_provider = "adty"
model = "gpt-5.4"
model_reasoning_effort = "high"

[model_providers.adty]
name = "ADTY AI"
base_url = "https://api.adtyai.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

### `~/.codex/auth.json`

```json
{
  "OPENAI_API_KEY": "sk-your-api-key"
}
```

CC Switch 写入的文件路径如下：

- **macOS / Linux**：`~/.codex/config.toml` 与 `~/.codex/auth.json`
- **Windows**：`%USERPROFILE%\.codex\config.toml` 与 `%USERPROFILE%\.codex\auth.json`

::: tip 模型选择建议
请在 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 核对当前 API Key 所属分组支持的精确模型名称。你也可以在 CC Switch 里新建多个 Codex 档案（例如一套偏深度推理、一套偏快速响应），按任务一键切换。
:::

## 第三步：激活配置并检查文件

1. 在 CC Switch 中点击 **保存（Save）**。
2. 在 **Codex** 标签页找到 `ADTY AI` 卡片并点击 **启用 / 切换**。
3. 在终端确认配置文件已成功写入：

**macOS / Linux：**

```bash
cat ~/.codex/config.toml
```

**Windows PowerShell：**

```powershell
Get-Content "$env:USERPROFILE\.codex\config.toml"
```

## 第四步：在终端中验证运行

打开新终端并进入代码目录，启动 Codex CLI：

```bash
codex
```

也可以使用非交互命令快速做一次连通性测试：

```bash
codex exec "请回复 OK，并输出当前所在目录名称。"
```

配置正常时你会看到：

- Codex CLI 直接进入工作状态，不会弹出浏览器账号登录引导。
- 模型顺利返回回复。
- 前往 [https://www.adtyai.com/logs](https://www.adtyai.com/logs) 可以看到该次调用的完整日志与 Token 计费详情。

## 常见问题速查

| 现象 | 常见原因 | 解决办法 |
| --- | --- | --- |
| `401 Unauthorized` | Key 填写有误、已禁用，或终端残留了旧的 `OPENAI_API_KEY` 环境变量 | 清理 Shell 中的 `OPENAI_API_KEY`，并在 CC Switch 中重新切换启用 |
| `403 Forbidden` | 误将 `https://www.adtyai.com/v1` 当作接口地址 | 改为专用 API 域名 `https://api.adtyai.com/v1` |
| `404 Not Found` | `base_url` 遗漏了 `/v1` 后缀 | 确保 `base_url = "https://api.adtyai.com/v1"` |
| 提示模型不可用或无权限 | 所填模型不在当前 API Key 的可用列表或分组内 | 在 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 核对模型名称及 Key 限制 |

更多 Codex 参数调优与 WSL 环境问题请查看 [Codex 排查](/zh-cn/faq/codex)。如果你同时使用桌面客户端，请继续阅读 [配置 Codex 桌面应用](/zh-cn/integrations/cc-switch/codex-app)。
