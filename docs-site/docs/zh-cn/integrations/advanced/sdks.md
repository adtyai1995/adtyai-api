# 官方 SDK 接入

**ADTY AI** 原生兼容 **OpenAI**、**Anthropic** 与 **Google GenAI** 官方 SDK。只需将 SDK 的 `base_url`（或 `baseUrl`）指向 `https://api.adtyai.com`（按协议决定是否带 `/v1`）并传入你的 ADTY AI API 密钥，即可无缝接入业务代码。

## 地址速查表

| SDK | Python 安装包 | Node.js / TS 安装包 | Base URL |
| --- | --- | --- | --- |
| **OpenAI**（Chat Completions 与 Responses API） | `openai` | `openai` | `https://api.adtyai.com/v1` |
| **Anthropic**（Messages API） | `anthropic` | `@anthropic-ai/sdk` | `https://api.adtyai.com` |
| **Google GenAI**（Gemini 原生 API） | `google-genai` | `@google/genai` | `https://api.adtyai.com` |

调用前请在 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 核对当前密钥所属分组可用的精确 `<model-id>`。

---

## 1. OpenAI SDK

### 安装依赖

::: code-group

```bash [Python]
pip install --upgrade openai
```

```bash [Node.js / TypeScript]
npm install openai
```

:::

### Chat Completions API（普通调用 + 流式输出）

::: code-group

```python [Python]
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["ADTYAI_API_KEY"],
    base_url="https://api.adtyai.com/v1",
    timeout=120.0,
    max_retries=3,
)

# 1) 非流式调用
resp = client.chat.completions.create(
    model="gpt-5.4",
    messages=[
        {"role": "system", "content": "你是一名简洁严谨的后端工程师助手。"},
        {"role": "user", "content": "用两句话解释 HTTP 429 与 Retry-After 的作用。"},
    ],
)
print(resp.choices[0].message.content)

# 2) 流式调用
stream = client.chat.completions.create(
    model="gpt-5.4",
    messages=[{"role": "user", "content": "写一个 3 行以内的 Python fizzbuzz。"}],
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
  apiKey: process.env.ADTYAI_API_KEY,
  baseURL: "https://api.adtyai.com/v1",
  timeout: 120_000,
  maxRetries: 3,
});

async function main() {
  // 1) 非流式调用
  const resp = await client.chat.completions.create({
    model: "gpt-5.4",
    messages: [
      { role: "system", content: "你是一名简洁严谨的后端工程师助手。" },
      { role: "user", content: "用两句话解释 HTTP 429 与 Retry-After 的作用。" },
    ],
  });
  console.log(resp.choices[0]?.message?.content);

  // 2) 流式调用
  const stream = await client.chat.completions.create({
    model: "gpt-5.4",
    messages: [{ role: "user", content: "写一个 3 行以内的 TypeScript fizzbuzz。" }],
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

### Responses API（普通调用 + 流式输出）

::: code-group

```python [Python]
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["ADTYAI_API_KEY"],
    base_url="https://api.adtyai.com/v1",
    timeout=180.0,
    max_retries=3,
)

# 1) 非流式 Responses API
response = client.responses.create(
    model="gpt-5.4",
    instructions="回答尽量精炼。",
    input="列出不可变基础设施的三个核心收益。",
)
print(response.output_text)

# 2) 流式 Responses API
with client.responses.stream(
    model="gpt-5.4",
    input="简述 REST API 中幂等键（Idempotency Key）的设计要点。",
) as stream:
    for event in stream:
        if event.type == "response.output_text.delta":
            print(event.delta, end="", flush=True)
print()
```

```ts [TypeScript]
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.ADTYAI_API_KEY,
  baseURL: "https://api.adtyai.com/v1",
  timeout: 180_000,
  maxRetries: 3,
});

async function main() {
  // 1) 非流式 Responses API
  const response = await client.responses.create({
    model: "gpt-5.4",
    instructions: "回答尽量精炼。",
    input: "列出不可变基础设施的三个核心收益。",
  });
  console.log(response.output_text);

  // 2) 流式 Responses API
  const stream = await client.responses.create({
    model: "gpt-5.4",
    input: "简述 REST API 中幂等键（Idempotency Key）的设计要点。",
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

::: warning Anthropic SDK 的 `base_url` 不要加 `/v1`
Anthropic 官方 SDK 会自动拼接 `/v1/messages`。请传入 `https://api.adtyai.com`，切勿写成 `https://api.adtyai.com/v1`。
:::

### 安装依赖

::: code-group

```bash [Python]
pip install --upgrade anthropic
```

```bash [Node.js / TypeScript]
npm install @anthropic-ai/sdk
```

:::

### Messages API（普通调用 + 流式输出）

::: code-group

```python [Python]
import os
import anthropic

client = anthropic.Anthropic(
    api_key=os.environ["ADTYAI_API_KEY"],
    base_url="https://api.adtyai.com",
    timeout=180.0,
    max_retries=3,
)

# 1) 非流式调用
message = client.messages.create(
    model="claude-sonnet-4-6",
    max_tokens=1024,
    messages=[
        {"role": "user", "content": "简短说明带随机抖动（Jitter）的指数退避原理。"}
    ],
)
print(message.content[0].text)

# 2) 流式调用
with client.messages.stream(
    model="claude-sonnet-4-6",
    max_tokens=1024,
    messages=[
        {"role": "user", "content": "写一个极简的 Go HTTP 健康检查处理函数。"}
    ],
) as stream:
    for text in stream.text_stream:
        print(text, end="", flush=True)
print()
```

```ts [TypeScript]
import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  apiKey: process.env.ADTYAI_API_KEY,
  baseURL: "https://api.adtyai.com",
  timeout: 180_000,
  maxRetries: 3,
});

async function main() {
  // 1) 非流式调用
  const message = await client.messages.create({
    model: "claude-sonnet-4-6",
    max_tokens: 1024,
    messages: [
      { role: "user", content: "简短说明带随机抖动（Jitter）的指数退避原理。" },
    ],
  });
  const firstBlock = message.content[0];
  if (firstBlock?.type === "text") {
    console.log(firstBlock.text);
  }

  // 2) 流式调用
  const stream = client.messages.stream({
    model: "claude-sonnet-4-6",
    max_tokens: 1024,
    messages: [
      { role: "user", content: "写一个极简的 Go HTTP 健康检查处理函数。" },
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

通过 `http_options`（TypeScript 中为 `httpOptions`）传入 `https://api.adtyai.com`，SDK 会自动拼接 `/v1beta/models/...` 路径。

### 安装依赖

::: code-group

```bash [Python]
pip install --upgrade google-genai
```

```bash [Node.js / TypeScript]
npm install @google/genai
```

:::

### Generate Content（普通调用 + 流式输出）

::: code-group

```python [Python]
import os
from google import genai
from google.genai import types

client = genai.Client(
    api_key=os.environ["ADTYAI_API_KEY"],
    http_options=types.HttpOptions(base_url="https://api.adtyai.com"),
)

# 1) 非流式调用
response = client.models.generate_content(
    model="gemini-2.5-pro",
    contents="给出三条编写可维护 SQL 迁移脚本的实战建议。",
)
print(response.text)

# 2) 流式调用
for chunk in client.models.generate_content_stream(
    model="gemini-2.5-pro",
    contents="用三条要点解释 gRPC Deadline 的作用。",
):
    if chunk.text:
        print(chunk.text, end="", flush=True)
print()
```

```ts [TypeScript]
import { GoogleGenAI } from "@google/genai";

const ai = new GoogleGenAI({
  apiKey: process.env.ADTYAI_API_KEY,
  httpOptions: {
    baseUrl: "https://api.adtyai.com",
  },
});

async function main() {
  // 1) 非流式调用
  const response = await ai.models.generateContent({
    model: "gemini-2.5-pro",
    contents: "给出三条编写可维护 SQL 迁移脚本的实战建议。",
  });
  console.log(response.text);

  // 2) 流式调用
  const stream = await ai.models.generateContentStream({
    model: "gemini-2.5-pro",
    contents: "用三条要点解释 gRPC Deadline 的作用。",
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

## 生产环境最佳实践

### 1. 使用环境变量管理密钥

切勿在源代码中硬编码 API 密钥或将 `.env` 提交至 Git 仓库，推荐统一通过环境变量注入：

```dotenv
# 统一变量或按协议拆分
ADTYAI_API_KEY="<YOUR_API_KEY>"

# 或使用各 SDK 原生环境变量（代码中省略对应参数时自动读取）
OPENAI_BASE_URL="https://api.adtyai.com/v1"
OPENAI_API_KEY="<YOUR_API_KEY>"
ANTHROPIC_BASE_URL="https://api.adtyai.com"
ANTHROPIC_API_KEY="<YOUR_API_KEY>"
GOOGLE_GEMINI_BASE_URL="https://api.adtyai.com"
GEMINI_API_KEY="<YOUR_API_KEY>"
```

关于密钥限额与轮换机制，请参考 [账号安全与访问令牌](/zh-cn/guides/security)。

### 2. 设置合理的超时时间

推理类模型或长文本生成耗时往往超过 60 秒。建议将读取超时（Read Timeout）设为 `120s`–`300s`，并尽可能开启流式传输（`stream=True`），以降低首字延迟并避免中间网关的空闲断连。

### 3. 针对 `429` 与 `5xx` 实施重试

OpenAI 与 Anthropic 官方 SDK 在配置 `max_retries` / `maxRetries`（推荐 `2`–`4`）后会自动对 `429` 限流与临时性 `5xx` 进行指数退避重试。如自研 HTTP 客户端或调用 Google GenAI，请仅对瞬态状态码（`429`、`500`、`502`、`503`、`504`）配合随机抖动重试，不要盲目重试 `400`、`401`、`403`。

### 4. 记录 `x-request-id` 便于排错与提单

`https://api.adtyai.com` 的每次响应都会返回 `x-request-id` 响应头。在日志中随错误详情记录该 ID，并在向 [支持中心（工单）](/zh-cn/guides/support) 反馈时附带，可大幅加快排查速度：

::: code-group

```python [Python（OpenAI 获取原始响应头）]
raw = client.chat.completions.with_raw_response.create(
    model="gpt-5.4",
    messages=[{"role": "user", "content": "ping"}],
)
print("x-request-id:", raw.headers.get("x-request-id"))
completion = raw.parse()
```

```ts [TypeScript（OpenAI withResponse）]
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

完整 HTTP 接口定义与错误格式请参阅 [API 参考](/zh-cn/api/)。
