# 通用常见问题

汇总使用 ADTY AI 过程中最常见的双域名报错、HTTP 状态码排查、美元按量计费、具体模型分组选择、流式断连以及 Request ID 获取方式。

## 为什么会收到 HTTP `403` 及 `api_domain_required`？

因为你将模型 API 请求发往了网页控制台域名 `https://www.adtyai.com`，而非专用 API 网关域名 `https://api.adtyai.com`。

**解决方法：**

1. 将客户端配置或环境变量中的 `https://www.adtyai.com` 替换为 `https://api.adtyai.com`。
2. 确认客户端对 `/v1` 后缀的要求：
   - OpenAI SDK、Codex CLI、OpenCode：`https://api.adtyai.com/v1`
   - Claude Code、Anthropic SDK、Gemini `google-genai` SDK：`https://api.adtyai.com`
3. 修改后重新打开终端或重启客户端，确保新环境变量生效。

详情可参阅[快速上手与双域名规则](/zh-cn/guides/overview)。

## `401`、`403`、`429`、`5xx` 分别代表什么？

- **`401 Unauthorized`**：API Key 缺失、复制不完整、已过期、已被禁用，或所属团队成员已被停用或移出团队。请核对 `sk-...` 密钥以及对应协议所需的请求头（`Authorization: Bearer` / `x-api-key` / `x-goog-api-key`）。
- **`403 Forbidden`（`insufficient_user_quota` / 额度不足）**：当账户个人钱包或团队共享钱包余额不足，或者当前 API Key 达到设置的单 Key 美元上限时，ADTY AI 网关会返回 HTTP `403`（错误码 `insufficient_user_quota`）。请前往**钱包**充值或在**令牌**页面提高该 Key 限额。（其他 `403` 还包括 `api_domain_required` 或模型/IP 权限限制；若响应出现 HTTP `402`，仅可能来自上游服务商透传，并非 ADTY AI 余额不足状态码。）
- **`429 Too Many Requests`**：触发了每分钟请求数（RPM）、每分钟 Token 数（TPM）或并发限制。请控制批量并发速度并实施指数退避重试。
- **`500` / `502` / `503` / `504`**：上游模型提供方或中转链路发生瞬时错误或超时。可稍候重试，并前往[渠道可用性与分组选择](/zh-cn/guides/channel-status)确认对应线路的实时健康度。

## 美元计费、额度与 Token 统计是如何计算的？

- **美元统一计价**：钱包余额与**消费日志**中的所有明细均以美元（USD）核算。
- **按实际用量乘以分组倍率**：每个模型均公开输入 Token、输出 Token 以及（如模型支持）缓存读取/写入 Token 或单张图像的基础单价。单次实际扣费 = 实际计量消耗 × 该 API Key 所绑定具体模型分组的倍率。
- **推理 Token 与提示词缓存**：深度推理模型的内部思考过程会按上游规范计入输出 Token；在支持 Prompt Caching 的模型上保持高缓存命中率可大幅降低输入成本（使用 Claude Code 时推荐阅读[Claude Code 缓存命中修复](/zh-cn/integrations/cli/cache-fix)）。
- **逐笔透明可核对**：每次完成调用后，都可在控制台**消费日志**中核对精确的 Token 拆分与美元扣费。

## 创建 API Key 时应该如何选择模型分组？

每把 API Key 在创建时都需要指定一个控制台**价格**页列出的**具体模型分组**，建议按以下两步选择：

1. 打开 [https://www.adtyai.com](https://www.adtyai.com) 的**价格**页面，查看各具体分组包含的模型列表与对应计费倍率。
2. 打开 [https://www.adtyai.com/model-status](https://www.adtyai.com/model-status)（参见[渠道可用性与分组选择](/zh-cn/guides/channel-status)），对比相关具体分组的 24 小时心跳柱、平均延迟与成功率。
3. 在创建 API Key 时直接选定最契合当前业务场景的具体模型分组。

如果不同工具需要不同定位的线路（例如 Claude Code 使用低延迟交互分组、批处理任务使用高性价比分组），分别创建独立 API Key 即可。

## 为什么长文本或流式输出偶尔会超时中断？

流式连接在中途断开通常与以下四个因素有关：

1. **客户端读超时过短**：部分 HTTP 库默认 30–60 秒超时，而推理模型或大上下文代码编辑在首字输出前可能停顿更久，建议将流式读取超时调整至 `180s`–`300s`。
2. **本地代理或 VPN 缓冲**：部分代理节点会缓冲 SSE 数据流或提前回收空闲 TLS 长连接，可尝试直连或切换稳定节点测试。
3. **上下文体积过大**：超长历史对话会显著拉长首字响应时间（TTFT），建议在长会话中适时压缩或新开上下文。
4. **上游线路瞬时波动**：前往[渠道可用性与分组选择](/zh-cn/guides/channel-status)查看对应模型最近 24 小时心跳状态。

## 提交支持工单时，如何在消费日志中查找 Request ID？

当你针对某次报错或账单疑问向[支持中心](/zh-cn/guides/support)提交工单时，附带 Request ID 能让技术人员瞬间定位日志：

1. 登录 [https://www.adtyai.com](https://www.adtyai.com)，在左侧菜单进入**消费日志**。
2. 按时间范围、令牌名称或模型名称筛选出对应请求记录。
3. 点击或展开该条日志，复制其中的 **Request ID**（部分接口响应头中也会同步返回）。
4. 将 Request ID、调用时间、模型名称与错误截图一并附在工单内即可。

## 专项排查指南

- [Claude Code 排查](/zh-cn/faq/claude-code)
- [Codex 排查](/zh-cn/faq/codex)
- [Gemini 排查](/zh-cn/faq/gemini)
