# Gemini 排查

总结通过 ADTY AI 调用 Google Gemini 模型时的常见疑问，涵盖 `/v1beta` 原生端点与 `/v1` OpenAI 兼容端点的差异、鉴权头、安全过滤、多模态图片编码以及推理/生图模型选型。

## 1. `/v1beta` 原生端点与 `/v1` OpenAI 兼容端点如何选择？

ADTY AI 同时提供两种协议访问 Gemini 模型，请结合客户端与功能诉求选择：

| 对比维度 | Gemini 原生端点（`/v1beta`） | OpenAI 兼容端点（`/v1`） |
| :--- | :--- | :--- |
| **接口地址** | `POST https://api.adtyai.com/v1beta/models/{model}:generateContent` | `POST https://api.adtyai.com/v1/chat/completions` |
| **流式方式** | `:streamGenerateContent?alt=sse` | `"stream": true` |
| **典型客户端** | 官方 `google-genai` SDK、原生 REST 调用 | OpenAI SDK、Cherry Studio、OpenCode、通用对话工具 |
| **适用场景** | 完整使用 Gemini 原生特性（思考配置、多模态原生 Part、Gemini 原生生图、`safetySettings`） | 已有 OpenAI 生态工具零改动平滑接入 |

::: tip 避免重复拼接 `/v1beta`
使用官方 `google-genai` Python 或 TypeScript SDK 时，只需设置 `base_url="https://api.adtyai.com"`（不要带 `/v1beta` 或 `/v1`），SDK 会自动拼接完整路径。
:::

## 2. `x-goog-api-key` 与 `Authorization: Bearer` 鉴权头

- **Gemini 原生接口（`/v1beta`）**：推荐使用 `x-goog-api-key: <YOUR_API_KEY>`（官方 `google-genai` SDK 默认请求头）；ADTY AI 在 `/v1beta` 路径下也同时兼容 `Authorization: Bearer <YOUR_API_KEY>`。
- **OpenAI 兼容接口（`/v1`）**：使用标准 `Authorization: Bearer <YOUR_API_KEY>`。
- **避免将密钥放在 URL 查询参数中**：即便部分工具支持 `?key=...`，也建议始终通过 HTTP Header 传递密钥，防止 `sk-...` 被记录在代理或服务端访问日志中。

## 3. 安全过滤与 `finishReason` 异常处理

当请求返回 HTTP `200` 但响应文本为空或内容提前截断时，请检查响应 JSON 中的 `candidates[0].finishReason` 与 `promptFeedback`：

- **`STOP`**：正常生成结束。
- **`MAX_TOKENS`**：触及 `maxOutputTokens` 上限。注意在 Gemini 思考模型中，内部推理 Token 也会占用输出配额，遇到回答截断请适当调大 `maxOutputTokens`。
- **`SAFETY` / `PROHIBITED_CONTENT`**：触发了上游内容安全策略拦截，可结合 `safetyRatings` 字段查看触发类别并调整表述。
- **`RECITATION`**：生成内容与受版权保护的原文高度重合，建议改写提示词要求总结或原创表达。

生产代码中务必先判断 `finishReason` 及 `candidates[0].content.parts` 是否存在，不要直接硬编码读取 `parts[0].text`。

## 4. 多模态 Base64 图像载荷格式差异

原生协议与 OpenAI 兼容协议对 Base64 图片前缀的要求不同，混用会导致 `400` 报错：

### Gemini 原生协议（`inlineData`）

`data` 字段必须是**不含 `data:image/png;base64,` 前缀的纯 Base64 字符串**，并单独通过 `mimeType` 指定格式：

```json
{
  "contents": [
    {
      "role": "user",
      "parts": [
        { "text": "请描述这张架构图中的核心组件。" },
        {
          "inlineData": {
            "mimeType": "image/png",
            "data": "<不含前缀的纯Base64字符串>"
          }
        }
      ]
    }
  ]
}
```

### OpenAI 兼容协议（`image_url`）

`url` 字段必须包含完整的 Data URI 前缀（`data:image/png;base64,...`）：

```json
{
  "model": "gemini-2.5-pro",
  "messages": [
    {
      "role": "user",
      "content": [
        { "type": "text", "text": "请描述这张图片。" },
        {
          "type": "image_url",
          "image_url": {
            "url": "data:image/png;base64,<纯Base64字符串>"
          }
        }
      ]
    }
  ]
}
```

::: warning 控制图片体积
未压缩的高清 PNG 转 Base64 后体积会膨胀约 33%，过大的请求体容易触发 HTTP `413` 或 `400`。非像素级校验场景下，建议先适当缩放分辨率并压缩为 JPEG 或 WebP。
:::

## 5. 思考模型与图像生成模型选型

- **思考 / 推理模型**（如 `gemini-2.5-pro`、`gemini-2.5-flash`）：内部推理过程消耗 `maxOutputTokens`，复杂分析任务建议将 `maxOutputTokens` 设为 `4096` 以上并预留充足的读取超时。
- **Gemini 原生图像生成模型**：需要通过 `/v1beta` 原生接口并开启图像输出模态（如 `responseModalities` 包含 `IMAGE`），而非 `/v1/chat/completions`。完整示例请见 [Gemini 图像生成](/zh-cn/integrations/paint/banana)。
- **模型分组核验**：调用前请在控制台**价格**页确认目标 Gemini 模型 ID 已包含在当前 API Key 绑定的具体模型分组中，并在[渠道可用性与分组选择](/zh-cn/guides/channel-status)查看实时心跳状态。
