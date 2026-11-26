# API 参考

ADTY AI 在 `https://api.adtyai.com` 提供多协议统一大模型网关，使用同一把 `sk-...` API Key 即可调用 OpenAI、Anthropic 与 Google Gemini 原生接口。

## Base URL 与双域名要求

```text
https://api.adtyai.com
```

::: danger 务必使用专用 API 域名
所有模型调用请求**必须**发往 `https://api.adtyai.com`。发往网页控制台域名（`https://www.adtyai.com/v1/...` 或 `/v1beta/...`）的请求将被拦截并返回 HTTP `403`（`api_domain_required`）。详见[快速上手与双域名规则](/zh-cn/guides/overview)。
:::

## 认证请求头（Authentication Headers）

请根据所调用协议及客户端 SDK 的约定传递密钥：

| 协议与端点 | 标准请求头 | 说明 |
| :--- | :--- | :--- |
| OpenAI 兼容端点（`/v1/models`、`/v1/chat/completions`、`/v1/responses`、`/v1/images/generations`） | `Authorization: Bearer sk-...` | 标准 Bearer Token 鉴权 |
| Anthropic Messages（`/v1/messages`） | `x-api-key: sk-...` | 亦兼容 `Authorization: Bearer sk-...`；请附带 `anthropic-version: 2023-06-01` |
| Gemini 原生端点（`/v1beta/models/{model}:generateContent`） | `x-goog-api-key: sk-...` | 亦兼容 `Authorization: Bearer sk-...` |

每把 API Key 均绑定一个创建时选定的具体模型分组，仅支持调用该分组已开放（且符合该 Key 模型白名单限制）的模型。

---

## 1. 查询可用模型列表

`GET /v1/models`

获取当前 API Key 所属具体模型分组下可供调用的模型清单。

### 请求示例

```bash
curl -sS https://api.adtyai.com/v1/models \
  -H "Authorization: Bearer <YOUR_API_KEY>"
```

### 响应示例

```json
{
  "object": "list",
  "data": [
    {
      "id": "gpt-5.4",
      "object": "model",
      "owned_by": "openai"
    },
    {
      "id": "claude-sonnet-4-6",
      "object": "model",
      "owned_by": "anthropic"
    },
    {
      "id": "gemini-2.5-pro",
      "object": "model",
      "owned_by": "google"
    }
  ]
}
```

---

## 2. OpenAI Chat Completions 接口

`POST /v1/chat/completions`

最通用的 OpenAI 兼容对话补全接口，支持多轮对话、结构化 JSON 输出、函数/工具调用、视觉识图以及 SSE 流式响应。

### 非流式请求示例

```bash
curl -sS https://api.adtyai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -d '{
    "model": "gpt-5.4",
    "messages": [
      { "role": "system", "content": "你是一位简洁精准的技术助手。" },
      { "role": "user", "content": "用一句话解释 HTTP 429 状态码。" }
    ],
    "temperature": 0.2,
    "max_tokens": 128
  }'
```

### 非流式响应示例

```json
{
  "id": "chatcmpl-01J9CHATEXAMPLE",
  "object": "chat.completion",
  "created": 1764200000,
  "model": "gpt-5.4",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "HTTP 429 Too Many Requests 表示客户端触发了请求频率或并发上限，应退避等待后再重试。"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 27,
    "completion_tokens": 28,
    "total_tokens": 55
  }
}
```

### 流式请求示例（`stream: true`）

```bash
curl -N https://api.adtyai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -d '{
    "model": "gpt-5.4",
    "stream": true,
    "stream_options": { "include_usage": true },
    "messages": [
      { "role": "user", "content": "从 1 数到 3。" }
    ]
  }'
```

### 流式 SSE 响应片段

```text
data: {"id":"chatcmpl-01J9STREAM","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant","content":"1, "},"finish_reason":null}]}

data: {"id":"chatcmpl-01J9STREAM","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"2, 3."},"finish_reason":"stop"}]}

data: [DONE]
```

---

## 3. OpenAI Responses API 接口

`POST /v1/responses`

新一代 OpenAI Agent 协议接口，是 **Codex CLI**（`wire_api = "responses"`）及新版 OpenAI SDK 推荐使用的标准端点，支持 `input`、推理参数配置、工具调用及流式事件输出。

### 请求示例

```bash
curl -sS https://api.adtyai.com/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -d '{
    "model": "gpt-5.4",
    "input": "用两条要点概括 TCP 与 UDP 的核心差异。",
    "max_output_tokens": 256
  }'
```

### 响应示例

```json
{
  "id": "resp_01J9RESPEXAMPLE",
  "object": "response",
  "status": "completed",
  "model": "gpt-5.4",
  "output": [
    {
      "id": "msg_01J9MSG",
      "type": "message",
      "role": "assistant",
      "status": "completed",
      "content": [
        {
          "type": "output_text",
          "text": "- TCP 面向连接，提供有序、可靠的数据传输保障。\n- UDP 无连接，协议开销更低但不内置重传与顺序保障。"
        }
      ]
    }
  ],
  "usage": {
    "input_tokens": 16,
    "output_tokens": 34,
    "total_tokens": 50
  }
}
```

---

## 4. Anthropic Messages API 接口

`POST /v1/messages`

Anthropic 原生协议接口，供 **Claude Code**、**Claude Desktop** 及官方 Anthropic SDK 调用，完整支持 Prompt Caching（`cache_control`）、扩展思考（Extended Thinking）、Tool Use 与 SSE 流式响应。

::: tip Anthropic 客户端的 Base URL 规范
配置 Claude Code 或官方 Anthropic SDK 时，请填写 `ANTHROPIC_BASE_URL="https://api.adtyai.com"`（**不要**带 `/v1`），因为客户端会自动拼接 `/v1/messages`。
:::

### 请求示例

```bash
curl -sS https://api.adtyai.com/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: <YOUR_API_KEY>" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 256,
    "messages": [
      { "role": "user", "content": "写一句关于清晰代码评审的短评。" }
    ]
  }'
```

### 响应示例

```json
{
  "id": "msg_01J9ANTHROPIC",
  "type": "message",
  "role": "assistant",
  "model": "claude-sonnet-4-6",
  "content": [
    {
      "type": "text",
      "text": "小步提交配合聚焦的评审意见，既能守住代码质量，又能让团队保持高效交付。"
    }
  ],
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 18,
    "output_tokens": 28
  }
}
```

---

## 5. OpenAI Images 图像生成接口

`POST /v1/images/generations`

用于调用 `gpt-image-1` 等 OpenAI 图像生成模型。更多尺寸、画质及解码落盘示例请见[OpenAI Images 绘图](/zh-cn/integrations/paint/gpt-image)。

### 请求示例

```bash
curl -sS https://api.adtyai.com/v1/images/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -d '{
    "model": "gpt-image-1",
    "prompt": "Minimalist isometric studio illustration of an API server rack in soft daylight",
    "size": "1024x1024",
    "n": 1
  }'
```

### 响应示例

```json
{
  "created": 1764200100,
  "data": [
    {
      "b64_json": "iVBORw0KGgoAAAANSUhEUgAA..."
    }
  ],
  "usage": {
    "input_tokens": 18,
    "output_tokens": 1056,
    "total_tokens": 1074
  }
}
```

---

## 6. Gemini 原生接口

`POST /v1beta/models/{model}:generateContent`

Google Gemini 原生 REST 协议，支持多模态内联图像、思考预算配置与原生 Gemini 图像生成。如需流式输出，可将动作后缀替换为 `:streamGenerateContent?alt=sse`。

### 请求示例

```bash
curl -sS "https://api.adtyai.com/v1beta/models/gemini-2.5-pro:generateContent" \
  -H "Content-Type: application/json" \
  -H "x-goog-api-key: <YOUR_API_KEY>" \
  -d '{
    "contents": [
      {
        "role": "user",
        "parts": [
          { "text": "列举不可变基础设施的三项优势。" }
        ]
      }
    ],
    "generationConfig": {
      "temperature": 0.3,
      "maxOutputTokens": 256
    }
  }'
```

### 响应示例

```json
{
  "candidates": [
    {
      "content": {
        "role": "model",
        "parts": [
          {
            "text": "1. 消除环境配置漂移。\n2. 支持快速、确定性的版本回滚。\n3. 简化水平扩容与自动化验证。"
          }
        ]
      },
      "finishReason": "STOP"
    }
  ],
  "usageMetadata": {
    "promptTokenCount": 11,
    "candidatesTokenCount": 32,
    "totalTokenCount": 43
  }
}
```

---

## HTTP 状态码说明

| 状态码 | 含义 | 常见原因与处理建议 |
| :--- | :--- | :--- |
| `400 Bad Request` | 请求体格式或参数不合法 | 检查 JSON 格式、必填字段（如 Anthropic 必填 `max_tokens`）、模型支持的参数及请求体大小，勿原样重试。 |
| `401 Unauthorized` | API Key 无效、缺失或已撤销 | 核对 `sk-...` 密钥是否有多余空格/换行，并在控制台确认密钥未被禁用或过期。 |
| `402 Payment Required` | 余额不足或令牌触达限额 | 为个人/团队钱包充值，或在**令牌**页面调高该 Key 的美元消费上限（部分场景下余额不足也可能以 `402` 或带额度不足说明的 `403` 返回）。 |
| `403 Forbidden` | `api_domain_required` 或权限受限 | 1) 若错误码为 `api_domain_required`，请将地址从 `https://www.adtyai.com` 改为 `https://api.adtyai.com`；2) 否则请检查分组可用模型、令牌模型白名单、IP 白名单或团队成员状态。 |
| `404 Not Found` | 路径错误或模型不存在 | 检查路径是否多拼了 `/v1/v1` 或漏写了 `/v1`，并通过 `GET /v1/models` 核实准确的模型 ID。 |
| `429 Too Many Requests` | 触发 RPM / TPM / 并发限流 | 降低瞬时并发，读取 `Retry-After`（如有）并采用指数退避重试。 |
| `500` / `502` / `503` / `504` | 上游服务或网关瞬时异常 | 进行带抖动的指数退避重试；若持续报错请查看[渠道可用性与分组选择](/zh-cn/guides/channel-status)。 |

## 重试与稳定性最佳实践

- **仅对瞬时错误重试**：对 `429`、`500`、`502`、`503`、`504` 及网络中断采用带随机抖动的指数退避（例如 `1s`、`2s`、`4s`、`8s`，最多 3–5 次）；对 `400`、`401`、`402`、`403` 应立即修正参数或凭证，避免无意义重试。
- **遵循 `Retry-After` 响应头**：遇到限流且响应头包含 `Retry-After` 时，至少等待该秒数后再发起下次请求。
- **合理设置流式超时**：深度推理模型或复杂编码工具链在首字返回前可能存在数秒静默思考期，建议开启流式输出并将客户端读超时设为 `180s`–`300s`。
- **留存 Request ID**：生产日志中建议记录响应中的请求 ID，或前往控制台**消费日志**检索，提交[支持中心](/zh-cn/guides/support)工单时一并提供。

如需 Python、Node.js / TypeScript、Go 等语言集成代码，请查阅[官方 SDK 接入](/zh-cn/integrations/advanced/sdks)。
