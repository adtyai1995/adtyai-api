# Official SDKs

**ADTY AI** is drop-in compatible with the official **OpenAI**, **Anthropic**, and **Google GenAI** SDKs. Point the SDK's `base_url` (or `baseUrl`) at `https://api.adtyai.com` (with or without `/v1` depending on the SDK) and pass your ADTY AI API key.

## Quick Reference

| SDK | Python Package | Node.js / TS Package | Base URL |
| --- | --- | --- | --- |
| **OpenAI** (Chat Completions & Responses) | `openai` | `openai` | `https://api.adtyai.com/v1` |
| **Anthropic** (Messages API) | `anthropic` | `@anthropic-ai/sdk` | `https://api.adtyai.com` |
| **Google GenAI** (Gemini API) | `google-genai` | `@google/genai` | `https://api.adtyai.com` |

Always check [ADTY AI Pricing](https://www.adtyai.com/pricing) for exact `<model-id>` names enabled on your API key's group.

---

## 1. OpenAI SDK

### Install

::: code-group

```bash [Python]
pip install --upgrade openai
```

```bash [Node.js / TypeScript]
npm install openai
```

:::

### Chat Completions API (Standard + Streaming)

::: code-group

```python [Python]
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["GATEWAY_API_KEY"],
    base_url="https://api.adtyai.com/v1",
    timeout=120.0,
    max_retries=3,
)

# 1) Non-streaming request
resp = client.chat.completions.create(
    model="gpt-5.4",
    messages=[
        {"role": "system", "content": "You are a concise engineering assistant."},
        {"role": "user", "content": "Explain HTTP 429 retry-after in two sentences."},
    ],
)
print(resp.choices[0].message.content)

# 2) Streaming request
stream = client.chat.completions.create(
    model="gpt-5.4",
    messages=[{"role": "user", "content": "Write a 3-line Python fizzbuzz."}],
    stream=True,
)
for chunk in stream:
    delta = chunk.choices[0].delta.content if chunk.choices else None
    if delta:
        print(delta, end="", flush=True)
print()
```

```ts [TypeScript]
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.GATEWAY_API_KEY,
  baseURL: "https://api.adtyai.com/v1",
  timeout: 120_000,
  maxRetries: 3,
});

async function main() {
  // 1) Non-streaming request
  const resp = await client.chat.completions.create({
    model: "gpt-5.4",
    messages: [
      { role: "system", content: "You are a concise engineering assistant." },
      { role: "user", content: "Explain HTTP 429 retry-after in two sentences." },
    ],
  });
  console.log(resp.choices[0]?.message?.content);

  // 2) Streaming request
  const stream = await client.chat.completions.create({
    model: "gpt-5.4",
    messages: [{ role: "user", content: "Write a 3-line TypeScript fizzbuzz." }],
    stream: true,
  });
  for await (const chunk of stream) {
    const text = chunk.choices[0]?.delta?.content;
    if (text) process.stdout.write(text);
  }
  process.stdout.write("\n");
}

main();
```

:::

### Responses API (Standard + Streaming)

::: code-group

```python [Python]
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["GATEWAY_API_KEY"],
    base_url="https://api.adtyai.com/v1",
    timeout=180.0,
    max_retries=3,
)

# 1) Non-streaming Responses API
response = client.responses.create(
    model="gpt-5.4",
    instructions="Answer concisely.",
    input="List three benefits of immutable infrastructure.",
)
print(response.output_text)

# 2) Streaming Responses API
with client.responses.stream(
    model="gpt-5.4",
    input="Summarize idempotency keys in REST APIs.",
) as stream:
    for event in stream:
        if event.type == "response.output_text.delta":
            print(event.delta, end="", flush=True)
print()
```

```ts [TypeScript]
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.GATEWAY_API_KEY,
  baseURL: "https://api.adtyai.com/v1",
  timeout: 180_000,
  maxRetries: 3,
});

async function main() {
  // 1) Non-streaming Responses API
  const response = await client.responses.create({
    model: "gpt-5.4",
    instructions: "Answer concisely.",
    input: "List three benefits of immutable infrastructure.",
  });
  console.log(response.output_text);

  // 2) Streaming Responses API
  const stream = await client.responses.create({
    model: "gpt-5.4",
    input: "Summarize idempotency keys in REST APIs.",
    stream: true,
  });
  for await (const event of stream) {
    if (event.type === "response.output_text.delta") {
      process.stdout.write(event.delta);
    }
  }
  process.stdout.write("\n");
}

main();
```

:::

---

## 2. Anthropic SDK

::: warning Do Not Include `/v1` in Anthropic `base_url`
The official Anthropic SDKs automatically append `/v1/messages`. Pass `https://api.adtyai.com`, not `https://api.adtyai.com/v1`.
:::

### Install

::: code-group

```bash [Python]
pip install --upgrade anthropic
```

```bash [Node.js / TypeScript]
npm install @anthropic-ai/sdk
```

:::

### Messages API (Standard + Streaming)

::: code-group

```python [Python]
import os
import anthropic

client = anthropic.Anthropic(
    api_key=os.environ["GATEWAY_API_KEY"],
    base_url="https://api.adtyai.com",
    timeout=180.0,
    max_retries=3,
)

# 1) Non-streaming request
message = client.messages.create(
    model="claude-sonnet-4-6",
    max_tokens=1024,
    messages=[
        {"role": "user", "content": "Explain exponential backoff with jitter briefly."}
    ],
)
print(message.content[0].text)

# 2) Streaming request
with client.messages.stream(
    model="claude-sonnet-4-6",
    max_tokens=1024,
    messages=[
        {"role": "user", "content": "Write a minimal Go HTTP healthcheck handler."}
    ],
) as stream:
    for text in stream.text_stream:
        print(text, end="", flush=True)
print()
```

```ts [TypeScript]
import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  apiKey: process.env.GATEWAY_API_KEY,
  baseURL: "https://api.adtyai.com",
  timeout: 180_000,
  maxRetries: 3,
});

async function main() {
  // 1) Non-streaming request
  const message = await client.messages.create({
    model: "claude-sonnet-4-6",
    max_tokens: 1024,
    messages: [
      { role: "user", content: "Explain exponential backoff with jitter briefly." },
    ],
  });
  const firstBlock = message.content[0];
  if (firstBlock?.type === "text") {
    console.log(firstBlock.text);
  }

  // 2) Streaming request
  const stream = client.messages.stream({
    model: "claude-sonnet-4-6",
    max_tokens: 1024,
    messages: [
      { role: "user", content: "Write a minimal Go HTTP healthcheck handler." },
    ],
  });
  stream.on("text", (text) => process.stdout.write(text));
  await stream.finalMessage();
  process.stdout.write("\n");
}

main();
```

:::

---

## 3. Google GenAI SDK

Pass `https://api.adtyai.com` via `http_options` (`httpOptions` in TypeScript). The SDK appends `/v1beta/models/...` automatically.

### Install

::: code-group

```bash [Python]
pip install --upgrade google-genai
```

```bash [Node.js / TypeScript]
npm install @google/genai
```

:::

### Generate Content (Standard + Streaming)

::: code-group

```python [Python]
import os
from google import genai
from google.genai import types

client = genai.Client(
    api_key=os.environ["GATEWAY_API_KEY"],
    http_options=types.HttpOptions(base_url="https://api.adtyai.com"),
)

# 1) Non-streaming request
response = client.models.generate_content(
    model="gemini-2.5-pro",
    contents="Give three practical tips for writing maintainable SQL migrations.",
)
print(response.text)

# 2) Streaming request
for chunk in client.models.generate_content_stream(
    model="gemini-2.5-pro",
    contents="Explain gRPC deadlines in three bullet points.",
):
    if chunk.text:
        print(chunk.text, end="", flush=True)
print()
```

```ts [TypeScript]
import { GoogleGenAI } from "@google/genai";

const ai = new GoogleGenAI({
  apiKey: process.env.GATEWAY_API_KEY,
  httpOptions: {
    baseUrl: "https://api.adtyai.com",
  },
});

async function main() {
  // 1) Non-streaming request
  const response = await ai.models.generateContent({
    model: "gemini-2.5-pro",
    contents: "Give three practical tips for writing maintainable SQL migrations.",
  });
  console.log(response.text);

  // 2) Streaming request
  const stream = await ai.models.generateContentStream({
    model: "gemini-2.5-pro",
    contents: "Explain gRPC deadlines in three bullet points.",
  });
  for await (const chunk of stream) {
    if (chunk.text) process.stdout.write(chunk.text);
  }
  process.stdout.write("\n");
}

main();
```

:::

---

## Production Best Practices

### 1. Manage Secrets with Environment Variables

Never hardcode API keys in source code or commit `.env` files to Git. Prefer standard environment variables:

```dotenv
# Shared key or per-protocol keys
GATEWAY_API_KEY="<YOUR_API_KEY>"

# Or SDK-native env vars (picked up automatically when omitted in code)
OPENAI_BASE_URL="https://api.adtyai.com/v1"
OPENAI_API_KEY="<YOUR_API_KEY>"
ANTHROPIC_BASE_URL="https://api.adtyai.com"
ANTHROPIC_API_KEY="<YOUR_API_KEY>"
GOOGLE_GEMINI_BASE_URL="https://api.adtyai.com"
GEMINI_API_KEY="<YOUR_API_KEY>"
```

See [Account Security & PAT](/guides/security) for key rotation and per-environment quota caps.

### 2. Configure Realistic Timeouts

Reasoning models and long structured completions can take more than 60 seconds before finishing. Set read timeouts to at least `120s`–`300s` and enable streaming (`stream=True`) whenever possible so clients receive incremental tokens and avoid intermediate proxy idle timeouts.

### 3. Handle Retries on `429` and `5xx`

Both the OpenAI and Anthropic SDKs automatically retry `429 Too Many Requests` and transient `5xx` responses with exponential backoff when `max_retries` / `maxRetries` is set (`2`–`4` is a sensible default). For custom HTTP pipelines or Google GenAI calls, retry only idempotent/transient errors (`429`, `500`, `502`, `503`, `504`) with randomized jitter, and do **not** blindly retry `400`, `401`, or `403`.

### 4. Capture `x-request-id` for Debugging & Support

Every response from `https://api.adtyai.com` carries an `x-request-id` header. Logging this ID alongside error payloads makes troubleshooting far faster when contacting [Support Center](/guides/support).

::: code-group

```python [Python (OpenAI Raw Response)]
raw = client.chat.completions.with_raw_response.create(
    model="gpt-5.4",
    messages=[{"role": "user", "content": "ping"}],
)
print("x-request-id:", raw.headers.get("x-request-id"))
completion = raw.parse()
```

```ts [TypeScript (OpenAI withResponse)]
const { data, response } = await client.chat.completions
  .create({
    model: "gpt-5.4",
    messages: [{ role: "user", content: "ping" }],
  })
  .withResponse();

console.log("x-request-id:", response.headers.get("x-request-id"));
console.log(data.choices[0]?.message?.content);
```

:::

For full HTTP endpoint specifications and error schemas, read the [API Reference](/api/).
