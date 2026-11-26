# Hermes Agent

Hermes Agent 是面向自主多步推理、研究与编程任务的本地 AI Agent 框架。你可以通过 OpenAI 兼容协议（`https://api.adtyai.com/v1`）或 Anthropic 原生协议（`https://api.adtyai.com`）将 **ADTY AI** 接入 Hermes Agent。

## 1. 安装 Hermes Agent

建议使用 Python 3.11 及以上环境，通过官方安装脚本完成安装：

```bash
curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash
```

重新加载 Shell 后确认命令行已就绪：

```bash
hermes --version
```

## 2. 配置密钥与自定义供应商

Hermes Agent 默认将凭证保存在 `~/.hermes/.env`，并将模型与运行配置保存在 `~/.hermes/config.yaml`。

### 方式一：OpenAI 兼容协议（推荐）

适用于通过 ADTY AI OpenAI 兼容网关调用 GPT、DeepSeek、Gemini 及 Claude 等各类模型。

在 `~/.hermes/.env` 中写入：

```dotenv
OPENAI_BASE_URL="https://api.adtyai.com/v1"
OPENAI_API_KEY="<YOUR_API_KEY>"
```

在 `~/.hermes/config.yaml` 中指定自定义供应商与默认模型：

```yaml
model:
  provider: custom
  base_url: "https://api.adtyai.com/v1"
  default: "gpt-5.4"
```

### 方式二：Anthropic 原生协议

如果希望以 Anthropic Messages 原生格式调用 Claude 系列模型，请在 `~/.hermes/.env` 中写入：

```dotenv
ANTHROPIC_BASE_URL="https://api.adtyai.com"
ANTHROPIC_API_KEY="<YOUR_API_KEY>"
```

并在 `~/.hermes/config.yaml` 中配置 Claude 模型：

```yaml
model:
  provider: anthropic
  base_url: "https://api.adtyai.com"
  default: "claude-sonnet-4-6"
```

::: tip 使用交互式向导微调配置
写入 `~/.hermes/.env` 后，也可以执行 `hermes setup` 或 `hermes model` 以交互方式核对并切换当前激活的供应商与模型。
:::

## 3. 模型选型建议

Hermes Agent 的核心工作流高度依赖连续工具调用（文件读写、Shell 执行、检索与记忆管理）。请先在 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 核对准确 `<model-id>`，并按场景选择模型：

- **复杂代码修改与多步规划**：优先选择 Claude Sonnet / Opus 系列或旗舰级 GPT-5 系列模型。
- **高频自动化与低成本批处理**：优先选择 `deepseek-chat` 或轻量级 GPT / Gemini Flash 模型。
- **深度架构分析与疑难排错**：可选用推理型模型，并适当调高请求超时时间。

## 4. 工具调用兼容性建议

- **优先选择结构化工具调用稳定的模型**：确保所选 `<model-id>` 支持标准 JSON Function Calling 与并行工具调用，否则 Hermes 在多步任务中可能把工具参数直接输出成纯文本而非真正触发工具。
- **预留充足的输出 Token 上限**：多文件补丁和复杂工具入参会占用较多输出长度，避免在 `config.yaml` 中把 `max_tokens` 限制过低。
- **注意推理模型的采样参数**：部分推理模型不接受自定义 `temperature` 或 `top_p` 参数。若调用推理模型时遇到 `400 Bad Request`，请移除 `config.yaml` 中的自定义采样覆盖项。
- **保持密钥分组与模型一致**：在 [https://www.adtyai.com](https://www.adtyai.com) 创建 Hermes 专用密钥时，绑定支持目标模型系列的稳定分组，可先查看 [渠道可用性与分组选择](/zh-cn/guides/channel-status)。

## 5. 验证与排查

启动 Hermes 并同时验证对话输出和本地工具调用：

```bash
hermes
```

在会话中输入测试指令：

```text
请确认当前 API 已连通，然后调用文件工具列出当前目录中的文件。
```

若能正常返回文本并成功执行工具调用，即说明配置生效。

### 常见问题

- **请求仍发往官方 OpenAI 地址**：检查当前终端是否导出了其他 `OPENAI_BASE_URL` 环境变量覆盖了 `~/.hermes/.env`。
- **`401 Unauthorized`**：确认在 [https://www.adtyai.com](https://www.adtyai.com) 中对应的 API 密钥处于启用状态且余额充足。
- **工具调用解析失败**：尝试切换至函数调用表现更稳定的旗舰模型，并确认本地代理未截断 SSE 流式数据块。
- **长任务链路超时**：在 `~/.hermes/config.yaml` 中增大 HTTP 超时阈值，更多通用排错请见 [通用常见问题](/zh-cn/faq/)。
