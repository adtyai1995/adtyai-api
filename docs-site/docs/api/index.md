# API Reference

ADTY AI exposes a unified multi-protocol gateway at `https://api.adtyai.com`, supporting OpenAI, Anthropic, and native Google Gemini endpoints through a single `sk-...` API key.

## Base URL & Domain Rule

```text
https://api.adtyai.com
```

::: danger Always Use the Dedicated API Domain
All API traffic **must** target `https://api.adtyai.com`. Requests sent to the web console domain (`https://www.adtyai.com/v1/...` or `/v1beta/...`) are rejected with HTTP `403` (`api_domain_required`). See [Quick Start & Dual Domains](/guides/overview).
:::

## Authentication Headers

Use the authentication header expected by your client or SDK:

| Protocol / Endpoints | Primary Header | Notes |
| :--- | :--- | :--- |
| OpenAI-compatible (`/v1/models`, `/v1/chat/completions`, `/v1/responses`, `/v1/images/generations`) | `Authorization: Bearer sk-...` | Standard Bearer token authentication |
| Anthropic Messages (`/v1/messages`) | `x-api-key: sk-...` | Also accepts `Authorization: Bearer sk-...`; include `anthropic-version: 2023-06-01` |
| Gemini Native (`/v1beta/models/{model}:generateContent`) | `x-goog-api-key: sk-...` | Also accepts `Authorization: Bearer sk-...` |

Every API key is bound to a concrete model group selected when the key was created. Only models enabled in that group (and allowed by the key's model allowlist, if set) can be called.

---

## 1. List Available Models

`GET /v1/models`

Returns the list of models accessible to the authenticating API key's concrete model group.

### Request

```bash
curl -sS https://api.adtyai.com/v1/models \
  -H "Authorization: Bearer <YOUR_API_KEY>"
```

### Example Response

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

## 2. OpenAI Chat Completions

`POST /v1/chat/completions`

Standard OpenAI-compatible chat interface supporting text, structured JSON output, tool/function calling, vision inputs, and Server-Sent Events (SSE) streaming.

### Non-Streaming Request

```bash
curl -sS https://api.adtyai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -d '{
    "model": "gpt-5.4",
    "messages": [
      { "role": "system", "content": "You are a concise technical assistant." },
      { "role": "user", "content": "Explain HTTP 429 in one sentence." }
    ],
    "temperature": 0.2,
    "max_tokens": 128
  }'
```

### Non-Streaming Response

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
        "content": "HTTP 429 Too Many Requests indicates that the client has exceeded its allowed request or token rate limit and should retry after backing off."
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 27,
    "completion_tokens": 26,
    "total_tokens": 53
  }
}
```

### Streaming Request (`stream: true`)

```bash
curl -N https://api.adtyai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -d '{
    "model": "gpt-5.4",
    "stream": true,
    "stream_options": { "include_usage": true },
    "messages": [
      { "role": "user", "content": "Count from 1 to 3." }
    ]
  }'
```

### Streaming SSE Chunks

```text
data: {"id":"chatcmpl-01J9STREAM","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant","content":"1, "},"finish_reason":null}]}

data: {"id":"chatcmpl-01J9STREAM","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"2, 3."},"finish_reason":"stop"}]}

data: [DONE]
```

---

## 3. OpenAI Responses API

`POST /v1/responses`

Modern stateful/agentic OpenAI protocol required by **Codex CLI** (`wire_api = "responses"`) and newer OpenAI SDK workflows. Supports `input`, reasoning configuration, built-in/custom tool schemas, and streaming SSE events.

### Request

```bash
curl -sS https://api.adtyai.com/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -d '{
    "model": "gpt-5.4",
    "input": "Summarize the difference between TCP and UDP in two bullet points.",
    "max_output_tokens": 256
  }'
```

### Example Response

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
          "text": "- TCP is connection-oriented and guarantees ordered, reliable delivery.\n- UDP is connectionless with lower overhead and no built-in delivery guarantee."
        }
      ]
    }
  ],
  "usage": {
    "input_tokens": 16,
    "output_tokens": 31,
    "total_tokens": 47
  }
}
```

---

## 4. Anthropic Messages API

`POST /v1/messages`

Native Anthropic protocol used by **Claude Code**, **Claude Desktop**, and the official Anthropic SDKs. Supports prompt caching (`cache_control`), extended thinking, tool use, and SSE streaming.

::: tip Base URL in Anthropic Clients
When configuring Anthropic SDKs or Claude Code, set `ANTHROPIC_BASE_URL="https://api.adtyai.com"` **without** `/v1`, because Anthropic clients automatically append `/v1/messages`.
:::

### Request

```bash
curl -sS https://api.adtyai.com/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: <YOUR_API_KEY>" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 256,
    "messages": [
      { "role": "user", "content": "Give me a haiku about clean diffs." }
    ]
  }'
```

### Example Response

```json
{
  "id": "msg_01J9ANTHROPIC",
  "type": "message",
  "role": "assistant",
  "model": "claude-sonnet-4-6",
  "content": [
    {
      "type": "text",
      "text": "Small focused commits,\nEvery changed line tells a tale,\nGreen checks greet the merge."
    }
  ],
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 15,
    "output_tokens": 21
  }
}
```

---

## 5. OpenAI Images Generations API

`POST /v1/images/generations`

Generate images with OpenAI image models such as `gpt-image-1`. See [OpenAI Images API](/integrations/paint/gpt-image) for full parameter and saving examples.

### Request

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

### Example Response

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

## 6. Gemini Native API

`POST /v1beta/models/{model}:generateContent`

Native Google Gemini protocol supporting multimodal inline images, structured generation configs, thinking budgets, and native Gemini image output. For streaming responses, use `:streamGenerateContent?alt=sse`.

### Request

```bash
curl -sS "https://api.adtyai.com/v1beta/models/gemini-2.5-pro:generateContent" \
  -H "Content-Type: application/json" \
  -H "x-goog-api-key: <YOUR_API_KEY>" \
  -d '{
    "contents": [
      {
        "role": "user",
        "parts": [
          { "text": "List three benefits of immutable infrastructure." }
        ]
      }
    ],
    "generationConfig": {
      "temperature": 0.3,
      "maxOutputTokens": 256
    }
  }'
```

### Example Response

```json
{
  "candidates": [
    {
      "content": {
        "role": "model",
        "parts": [
          {
            "text": "1. Eliminates configuration drift.\n2. Enables fast, deterministic rollbacks.\n3. Simplifies horizontal scaling and testing."
          }
        ]
      },
      "finishReason": "STOP"
    }
  ],
  "usageMetadata": {
    "promptTokenCount": 9,
    "candidatesTokenCount": 24,
    "totalTokenCount": 33
  }
}
```

---

## HTTP Status Codes

| Status Code | Meaning | Typical Cause & Action |
| :--- | :--- | :--- |
| `400 Bad Request` | Malformed payload or invalid parameter | Check JSON syntax, required fields (e.g., `max_tokens` for Anthropic), model parameter compatibility, and payload size. Do not retry unchanged. |
| `401 Unauthorized` | Invalid, missing, or revoked API key | Verify the `sk-...` key, ensure no stray spaces/newlines, and confirm the key is enabled in the console. |
| `402 Payment Required` | Insufficient balance or key quota cap reached | Top up your personal or team wallet, or raise the per-key USD quota cap in **API Keys**. *(Some clients surface quota exhaustion as `402` or `403` with an insufficient quota message.)* |
| `403 Forbidden` | `api_domain_required` or permission denial | 1) If error code is `api_domain_required`, switch from `https://www.adtyai.com` to `https://api.adtyai.com`. 2) Otherwise check the key's model group, model allowlist, IP allowlist, or team member status. |
| `404 Not Found` | Unknown endpoint path or unavailable model | Verify URL path (especially double `/v1/v1` in Anthropic clients or missing `/v1` in OpenAI clients) and verify the model ID via `GET /v1/models`. |
| `429 Too Many Requests` | Rate limit or concurrency limit reached | RPM, TPM, or upstream concurrency limit hit. Respect `Retry-After` if present and retry with exponential backoff. |
| `500` / `502` / `503` / `504` | Upstream provider or gateway transient error | Retry with exponential backoff and jitter; check [Channel Availability](/guides/channel-status) if persistent. |

## Retry & Reliability Best Practices

- **Retry only transient failures**: Apply exponential backoff with random jitter (e.g., `1s`, `2s`, `4s`, `8s` up to 3–5 attempts) for `429`, `500`, `502`, `503`, `504`, and network resets. Never blindly retry `400`, `401`, `402`, or `403`.
- **Honor `Retry-After`**: When a `429` or `503` response includes a `Retry-After` header, wait at least that duration before retrying.
- **Set realistic streaming timeouts**: Reasoning models and large coding turns can take several seconds before emitting the first visible token. Use generous read timeouts (for example, 120–300 seconds) and enable streaming whenever possible.
- **Log request IDs**: Record the response request ID or look it up in **Usage Logs** when troubleshooting errors or filing a ticket in the [Support Center](/guides/support).

For language-specific SDK snippets (Python, Node.js/TypeScript, Go), see [Official SDKs](/integrations/advanced/sdks).
