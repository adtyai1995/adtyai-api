# Claude Code 缓存命中诊断与修复

**Anthropic Prompt Caching（提示词缓存）** 是降低 **Claude Code** 长会话费用、缩短首字响应时间（TTFT）的最核心机制。Claude Code 每轮都会携带庞大的前缀内容——系统指令、内置工具 Schema、MCP 工具定义、`CLAUDE.md` 以及历史对话——在健康状态下，单次会话应有 **80%～95% 以上的输入 Token 命中缓存（`cache_read_input_tokens`）**，而不是每轮重复按全额输入或缓存写入计费。

如果你在 **ADTY AI** 的使用日志中发现每轮都产生极高的 `cache_creation_input_tokens`、而 `cache_read_input_tokens` 接近 `0`，请按本文逐步排查并修复。

::: info 先核对接口直连域名
确保 Claude Code 直接指向 `https://api.adtyai.com`（切勿指向会返回 403 的网页控制台域名 `https://www.adtyai.com`，也尽量避免在中间叠加会改写请求体的第三方本地代理）。
:::

## 1. 为什么 Prompt Cache 对成本与延迟如此关键

Anthropic 服务端缓存严格按**字节级完全一致的前缀顺序**进行匹配：

```text
[1. 工具与 MCP Schema] → [2. 系统提示词与 CLAUDE.md] → [3. 截至缓存断点的历史消息]
```

- **缓存读取（`cache_read_input_tokens`）**：直接复用已缓存的 KV 状态，单价大幅低于普通输入 Token（通常约节省 90%），同时显著加快大仓库的首字响应速度。
- **缓存写入（`cache_creation_input_tokens`）**：首次建立前缀缓存时写入一次（略高于基础输入单价），后续轮次即可反复读取。
- **前缀失效连锁反应**：只要前缀靠前位置有**哪怕一个字节发生变化**（例如工具顺序变动、系统提示词被注入时间戳、或中途切换模型），该位置之后的全部内容都会失去缓存命中并重新触发整段缓存写入。

## 2. 如何在 ADTY AI 使用日志中查看缓存指标

1. 登录控制台 [https://www.adtyai.com](https://www.adtyai.com)，进入 **使用日志（Usage Logs）**。
2. 按 Claude Code 对应的 API Key 筛选，查看同一会话中连续 5～10 轮请求记录。
3. 观察每轮的 Token 构成：
   - **第 1 轮（新会话启动）**：出现较高的 `cache_creation`、且 `cache_read` 为 `0`，属于正常预热。
   - **第 2～N 轮（距离上一轮 5 分钟内）**：正常应表现为**极高的 `cache_read`**（数万 Token）与**极小的 `cache_creation`**（仅包含本轮新增对话与工具输出增量）。
   - **异常信号**：如果第 2、3、4 轮每次仍写入 3 万～10 万 `cache_creation`，而 `cache_read` 持续为 `0` 或极低，说明每轮前缀都在被破坏。

你可以用以下公式粗略评估有效缓存命中率：

```text
缓存命中率 = cache_read / (input_tokens + cache_creation + cache_read)
```

在持续迭代的多轮编程任务中，健康会话的命中率通常高于 **80%**。

## 3. 缓存未命中的 7 个常见原因与修复方法

### 原因 1：超过 5 分钟静默导致 TTL 自然过期
Anthropic 标准临时提示词缓存的有效期（TTL）为 **5 分钟**。
- **表现**：连续对话时命中率很高，但离开 6 分钟以上再继续追问，下一轮 `cache_read` 归零并重新写入。
- **建议**：属于上游正常过期机制。相关追问尽量集中在一个连续时间段内完成；若长时间暂停后切入新任务，可直接用 `/clear` 开启轻量新会话。

### 原因 2：系统提示词、工具顺序或 MCP 定义动态变化
由于工具定义与系统提示词位于前缀最前端，任何细微变动都会导致整条会话缓存全部失效。
- **常见诱因**：
  - 某些 MCP Server 每次返回的工具列表顺序不稳定，或在会话中途动态增删工具。
  - 中间层脚本、Hook 或第三方中转代理向 `system`、`tools` 或请求体中注入了时间戳、随机 ID，或改写了 `cache_control` 断点。
  - 在长会话中途反复编辑项目根目录的 `CLAUDE.md`。
- **修复**：
  - 编码期间保持 `CLAUDE.md` 稳定，仅在任务切换间隙调整。
  - 运行 `claude mcp list` 清理不常用的 MCP Server，保持工具 Schema 精简且顺序固定。
  - 让 Claude Code **直连** `https://api.adtyai.com`，移除不必要的本地改写代理。

### 原因 3：环境变量与 `settings.json` 混用冲突
如果 `~/.zshrc`、Windows 用户环境变量、`~/.claude/settings.json` 以及项目目录 `.claude/settings.local.json` 同时存在旧配置，可能导致主会话、子 Agent 或 IDE 插件落到不一致的配置上，甚至触发异常重试。
- **修复**：统一排查并清理冗余环境变量，只保留一份干净的 `~/.claude/settings.json`：

```bash
# macOS / Linux：检查当前 Shell 是否残留旧变量
env | grep -iE "ANTHROPIC|CLAUDE"
```

```powershell
# Windows PowerShell：检查环境变量
Get-ChildItem Env: | Where-Object { $_.Name -match "ANTHROPIC|CLAUDE" }
```

确认 `ANTHROPIC_BASE_URL` 统一为 `https://api.adtyai.com`（不带 `/v1`），并按需启用 `"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"`。

### 原因 4：会话中途频繁切换模型
Prompt Cache 严格绑定到**具体模型 ID**（例如 `claude-sonnet-4-6` 无法读取 `claude-opus-4-6` 创建的缓存）。
- **表现**：在长会话中每次运行 `/model` 切换 Opus 与 Sonnet，下一轮都会为整个历史上下文重新支付一次缓存写入费用。
- **修复**：开始一个任务前先选定主力模型；若需换模型做复核，建议先 `/compact` 或另开聚焦会话。

### 原因 5：`CLAUDE.md` 过大或上下文过度膨胀
过长的 `CLAUDE.md` 或一次性贴入超大日志，不仅会放大每次缓存重建的成本，还会频繁触发自动压缩（Auto-Compaction）——压缩会改写前文消息，立即使原有缓存失效。
- **修复**：
  - 将根目录 `CLAUDE.md` 控制在 150～200 行以内，只保留高价值约束，详细规范拆到子文档按需读取。
  - 切换不相关功能时执行 `/clear`，避免单个 150k+ Token 的超长会话贯穿全天。
  - 阶段性任务完成后主动运行 `/compact`，避免在写代码半途被动触发上下文压缩。

### 原因 6：频繁重启进程且未续接会话
每次关闭终端重新运行 `claude` 都会建立全新上下文；脚本中反复冷启动 `claude -p` 也无法复用历史对话。
- **修复**：5 分钟 TTL 窗口内重新进入项目时，使用 `claude -c`（`--continue`）或 `claude --resume` 续接当前会话。

### 原因 7：令牌分组与渠道稳定性
缓存保存在上游模型提供方节点上。若所选分组存在多类异构渠道切换或在高峰期发生故障转移，请求切至新上游节点时会重新预热一次缓存。
- **修复**：查阅 [渠道可用性与分组选择](/zh-cn/guides/channel-status)，优先选用稳定且专注 Claude 原生协议的令牌分组，并为 Claude Code 单独分配一把 API Key，便于在日志中独立观测缓存命中率。

## 4. 推荐的稳定 `~/.claude/settings.json` 模板

使用精简且稳定的配置，同时提高请求超时阈值，防止大上下文流式响应因超时中断而触发重试重建缓存：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-your-api-key",
    "API_TIMEOUT_MS": "600000",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  }
}
```

## 5. 60 秒自检验证流程

按以下步骤快速验证缓存是否已恢复正常：

1. 关闭中间改写代理，清除重复 Shell 环境变量，仅通过 `~/.claude/settings.json` 配置 `ANTHROPIC_BASE_URL="https://api.adtyai.com"`。
2. 在小型项目目录启动 `claude`。
3. **第 1 轮**：发送 `“读取 README.md 并用两点总结。”`
4. 30 秒内发送 **第 2 轮**：`“仅基于刚才读到的内容，列出 3 条具体改进建议。”`
5. 30 秒内发送 **第 3 轮**：`“把第 1 条建议整理成简明检查清单。”`
6. 打开 [https://www.adtyai.com](https://www.adtyai.com) 的 **使用日志（Usage Logs）**：
   - 第 1 轮应出现缓存写入（`cache_creation`）。
   - 第 2、3 轮应出现**较高的 `cache_read`**，覆盖系统提示词、工具定义与前序对话，而 `cache_creation` 仅占很小的增量。

如果在完全干净的配置下，第 2、3 轮 `cache_read` 仍持续为 `0`，请记录使用日志中的 **Request ID** 并前往 [支持中心（工单）](/zh-cn/guides/support) 提交排查，以便我们核查对应渠道链路。

## 相关文档

- [Claude Code 接入指南](/zh-cn/integrations/cli/claude-code)
- [Claude Code 排查](/zh-cn/faq/claude-code)
- [渠道可用性与分组选择](/zh-cn/guides/channel-status)
