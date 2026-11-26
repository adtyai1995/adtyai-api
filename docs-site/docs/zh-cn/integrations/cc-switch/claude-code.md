# 在 CC Switch 中配置 Claude Code

本文介绍如何在 **CC Switch** 中添加 **ADTY AI** 作为 **Claude Code** 的模型供应商，核对生成的 `~/.claude/settings.json` 文件，并在终端中完成验证。

## 准备工作

- 已在本地安装 **Claude Code**（如尚未安装，请先参考 [基础环境准备](/zh-cn/integrations/cli/environment) 与 [Claude Code](/zh-cn/integrations/cli/claude-code)）。
- 已安装 **CC Switch**（详见 [CC Switch 简介与安装](/zh-cn/integrations/cc-switch/)）。
- 已在 [https://www.adtyai.com](https://www.adtyai.com) 创建支持 Claude 模型的 **ADTY AI** API Key（`sk-...`）。
- 已清理终端中可能冲突的 `ANTHROPIC_*` 环境变量（详见 [通用基础配置](/zh-cn/integrations/cc-switch/common)）。

## 第一步：添加 ADTY AI 供应商

1. 打开 **CC Switch**，在顶部导航栏切换到 **Claude Code** 标签页。
2. 点击 **添加供应商（+）**，选择 **自定义（Custom）** 或 Anthropic 兼容模板。
3. 按下表填写配置项：

| 字段 | 填写内容 | 说明 |
| --- | --- | --- |
| **供应商名称** | `ADTY AI` | 也可按用途命名为 `ADTY AI - Claude` |
| **API Base URL**（`ANTHROPIC_BASE_URL`） | `https://api.adtyai.com` | **切勿在末尾加 `/v1`** |
| **API Key**（`ANTHROPIC_AUTH_TOKEN`） | `sk-your-api-key` | 粘贴你的 ADTY AI API Key |
| **默认模型**（`ANTHROPIC_MODEL`） | `claude-sonnet-4-6` | 实际可用模型 ID 请以 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 为准 |
| **快速/轻量模型**（`ANTHROPIC_SMALL_FAST_MODEL`） | `claude-haiku-4-5-20251001` | 可选，用于后台快速辅助任务 |

::: warning 切勿在 Claude 接口地址后拼接 `/v1`
Claude Code 会在 `ANTHROPIC_BASE_URL` 后自动追加 `/v1/messages`。如果误填为 `https://api.adtyai.com/v1`，实际请求地址会变成 `/v1/v1/messages` 并返回 HTTP `404`。请务必填写 `https://api.adtyai.com`。
:::

## 第二步：核对生成的 `settings.json` 预览

保存前，可以在 CC Switch 的 JSON 预览区检查即将写入 Claude Code 的最终配置。标准配置示例如下：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-your-api-key",
    "ANTHROPIC_MODEL": "claude-sonnet-4-6",
    "ANTHROPIC_SMALL_FAST_MODEL": "claude-haiku-4-5-20251001",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  }
}
```

激活该档案后，CC Switch 会将其写入 Claude Code 的用户配置文件路径：

- **macOS / Linux**：`~/.claude/settings.json`
- **Windows**：`%USERPROFILE%\.claude\settings.json`

::: tip 模型固定与自由切换
如果你更习惯在会话内通过 `/model` 随时切换模型，可以将 `ANTHROPIC_MODEL` 和 `ANTHROPIC_SMALL_FAST_MODEL` 留空；也可以在 CC Switch 里分别建立多套档案（例如一套默认 `claude-opus-4-6` 用于架构评审，一套默认 `claude-sonnet-4-6` 用于日常开发），按需一键切换。
:::

## 第三步：启用该配置档案

1. 点击 **保存（Save）** 将 `ADTY AI` 供应商存入 CC Switch。
2. 在 Claude Code 列表中找到 `ADTY AI` 卡片，点击 **启用 / 切换** 使其生效。
3. 可在终端检查本地文件是否已更新：

**macOS / Linux：**

```bash
cat ~/.claude/settings.json
```

**Windows PowerShell：**

```powershell
Get-Content "$env:USERPROFILE\.claude\settings.json"
```

## 第四步：在终端中测试验证

打开一个**新的**终端窗口，进入任意项目目录并运行：

```bash
claude
```

进入交互界面后：

1. 输入 `/status`，确认当前 API Base URL 显示为 `https://api.adtyai.com` 且无鉴权异常提示。
2. 发送一条简短测试消息（例如：`回复 OK 并说明你当前使用的模型`）。
3. 前往 [https://www.adtyai.com/logs](https://www.adtyai.com/logs)，确认对应 API Key 下已正常记录请求与 Token 消耗。

## 在多个 ADTY AI 档案间切换

如果你配置了个人 Key 与团队 Key 等多个档案：

1. 在 CC Switch 的 **Claude Code** 页面点击切换到目标档案。
2. 在正在运行的终端里输入 `/exit` 退出旧会话，重新执行 `claude`，新进程就会加载最新的 `~/.claude/settings.json`。

## 排查与进阶优化

- **启动后仍弹出 Anthropic 官方登录页**：检查终端中是否存在残留的 `ANTHROPIC_API_KEY` 或 `ANTHROPIC_BASE_URL` 环境变量，或排查 `~/.claude.json` 初始化状态，详见 [Claude Code 排查](/zh-cn/faq/claude-code)。
- **返回 HTTP `403`**：通常是因为将控制台域名 `https://www.adtyai.com` 误填成了 API 地址，请改为 `https://api.adtyai.com`。
- **返回 HTTP `404`**：检查 `ANTHROPIC_BASE_URL` 末尾是否多写了 `/v1`。
- **长对话缓存命中率偏低**：参考 [Claude Code 缓存命中修复](/zh-cn/integrations/cli/cache-fix) 进一步降低 Token 成本。
- **想用 Claude Code 体验其他模型？** 可查阅 [Claude Code 调用 ChatGPT](/zh-cn/integrations/advanced/chatgpt-claude-code) 与 [Claude Code 调用 DeepSeek](/zh-cn/integrations/advanced/deepseek-claude-code)。
