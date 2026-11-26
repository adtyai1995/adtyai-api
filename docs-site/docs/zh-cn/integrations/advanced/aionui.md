# AionUI

AionUI 是一款开源的多 Agent 桌面端图形界面，既支持直接通过 API 调用模型对话，也可以统一托管本地 CLI 编码 Agent（如 Claude Code、Codex CLI、Gemini CLI）。你可以同时在 AionUI 的设置中添加 **ADTY AI** 自定义供应商，并让其调用的底层 CLI Agent 全部走 **ADTY AI** 网关。

## 1. 在 AionUI 设置中添加自定义供应商

打开 AionUI，进入 **设置 → 模型 / 供应商（Model / Providers）**，点击 **添加自定义供应商**，并根据选择的协议填写对应的 Base URL：

| 供应商协议 | Base URL | 说明 |
| --- | --- | --- |
| **OpenAI 兼容（OpenAI-Compatible）** | `https://api.adtyai.com/v1` | 适用于 GPT、DeepSeek、Gemini 及通用 OpenAI 协议渠道 |
| **Anthropic** | `https://api.adtyai.com` | 原生 Claude Messages 协议（不要加 `/v1`） |
| **Gemini** | `https://api.adtyai.com` | 原生 Gemini 协议（不要加 `/v1` 或 `/v1beta`） |

填写步骤：

1. **名称**：`ADTY AI`（或按协议分别命名为 `ADTY AI (OpenAI)`、`ADTY AI (Anthropic)` 等）。
2. **Base URL**：按上表填入对应地址。
3. **API Key**：粘贴在 [ADTY AI 控制台](https://www.adtyai.com) 创建的 `<YOUR_API_KEY>`。
4. **模型列表**：点击拉取模型，或从 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 复制准确的 `<model-id>` 手动添加后保存。

## 2. 在 AionUI 中配置底层 CLI Agent

在 AionUI 中启动某个 CLI Agent 面板时，AionUI 会拉起你本地安装的对应 CLI 程序，并继承该 CLI 的本地配置文件与环境变量。建议先完成对应 CLI 的本地配置：

### Claude Code

1. 按 [Claude Code 接入指南](/zh-cn/integrations/cli/claude-code) 完成安装与配置。
2. 确认 `~/.claude/settings.json` 中已写入：

   ```json
   {
     "env": {
       "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
       "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>"
     }
   }
   ```

### Codex CLI

1. 按 [Codex CLI 接入指南](/zh-cn/integrations/cli/codex) 完成安装与配置。
2. 确认 `~/.codex/config.toml` 中的 `base_url = "https://api.adtyai.com/v1"`，且 `~/.codex/auth.json` 已填入 ADTY AI 密钥。

### Gemini CLI

1. 安装 Gemini CLI，并在系统用户环境中设置 ADTY AI 地址与密钥（参考 [基础环境准备](/zh-cn/integrations/cli/environment) 与 [Gemini 排查](/zh-cn/faq/gemini)）：

   ::: code-group

   ```bash [macOS / Linux]
   export GOOGLE_GEMINI_BASE_URL="https://api.adtyai.com"
   export GEMINI_API_KEY="<YOUR_API_KEY>"
   ```

   ```powershell [Windows（PowerShell）]
   [Environment]::SetEnvironmentVariable("GOOGLE_GEMINI_BASE_URL", "https://api.adtyai.com", "User")
   [Environment]::SetEnvironmentVariable("GEMINI_API_KEY", "<YOUR_API_KEY>", "User")
   ```

   :::

::: tip 修改 CLI 配置后请重启 AionUI
桌面应用启动时会缓存当前环境变量。修改 `~/.claude/settings.json`、`~/.codex/config.toml` 或系统环境变量后，请彻底退出并重新打开 AionUI，以确保新启动的 CLI 子进程加载到最新配置。
:::

## 3. 验证响应效果

1. **直接 API 对话验证**：新建普通对话，在模型选择器中选中刚配置的 **ADTY AI** 模型，发送 `请回复 OK 确认连接成功`，验证流式输出是否正常。
2. **CLI Agent 会话验证**：在 AionUI 中打开一个项目目录，选择 Claude Code、Codex 或 Gemini CLI，让它读取并总结目录中的一个文件，确认流式响应与本地工具调用均能顺利执行。

## 常见问题排查

- **AionUI 无法识别本地 CLI**：先在普通终端确认 `claude`、`codex` 或 `gemini` 命令可直接执行，再检查其安装路径是否已加入系统 `PATH`（参考 [基础环境准备](/zh-cn/integrations/cli/environment)）。
- **自定义供应商报 `404 Not Found`**：检查 OpenAI 兼容协议是否填了 `https://api.adtyai.com/v1`，而 Anthropic 与 Gemini 是否填了 `https://api.adtyai.com`。
- **单个 Agent 鉴权或模型报错**：在 [渠道可用性与分组选择](/zh-cn/guides/channel-status) 确认对应密钥所属分组支持目标模型。
- **更多专题排错**：请见 [Claude Code 排查](/zh-cn/faq/claude-code)、[Codex 排查](/zh-cn/faq/codex) 与 [Gemini 排查](/zh-cn/faq/gemini)。
