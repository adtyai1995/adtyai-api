# Gemini FAQ

Common questions and troubleshooting fixes for calling Google Gemini models through ADTY AI—via both the native `/v1beta` API and the OpenAI-compatible `/v1` API.

## 1. Native `/v1beta` Endpoint vs. OpenAI-Compatible `/v1` Endpoint

ADTY AI supports Gemini models on two protocols. Pick the one that matches your client and feature requirements:

| Aspect | Gemini Native (`/v1beta`) | OpenAI-Compatible (`/v1`) |
| :--- | :--- | :--- |
| **Endpoint** | `POST https://api.adtyai.com/v1beta/models/{model}:generateContent` | `POST https://api.adtyai.com/v1/chat/completions` |
| **Streaming** | `:streamGenerateContent?alt=sse` | `"stream": true` |
| **SDK / Tools** | `google-genai` SDK, native REST clients | OpenAI SDK, Cherry Studio, OpenCode, generic chat UIs |
| **Best For** | Full Gemini-native capabilities (thinking config, native multimodal parts, Gemini image generation, `safetySettings`) | Drop-in compatibility across multi-provider apps with minimal code changes |

::: tip Avoid Double Path Prefixes
When using the official `google-genai` Python or TypeScript SDK, set `base_url="https://api.adtyai.com"` (without `/v1beta` or `/v1`). The SDK appends `/v1beta/models/...` automatically.
:::

## 2. `x-goog-api-key` vs. `Authorization: Bearer`

- **Gemini Native (`/v1beta`)**: Send `x-goog-api-key: <YOUR_API_KEY>` (the default header used by `google-genai`). ADTY AI also accepts `Authorization: Bearer <YOUR_API_KEY>` on `/v1beta` routes if your HTTP client prefers Bearer auth.
- **OpenAI-Compatible (`/v1`)**: Send `Authorization: Bearer <YOUR_API_KEY>`.
- **Avoid key leakage in URLs**: Even if a tool supports `?key=...` query strings, prefer HTTP headers so your `sk-...` key never appears in web server or proxy access logs.

## 3. Safety Filters & `finishReason` Handling

If a Gemini response returns HTTP `200` with empty text or stops unexpectedly, inspect `candidates[0].finishReason` and `promptFeedback`:

- **`STOP`**: Normal completion.
- **`MAX_TOKENS`**: Output reached `maxOutputTokens` (note that on thinking models, reasoning tokens also consume output token budget—increase `maxOutputTokens` if answers cut off early).
- **`SAFETY` / `PROHIBITED_CONTENT`**: Upstream safety filters blocked the prompt or output. Rephrase ambiguous prompts or inspect `safetyRatings` to see which category triggered.
- **`RECITATION`**: Output matched copyrighted/verbatim training material too closely. Ask for an original summary or paraphrase instead.

Always check `finishReason` defensively in production code instead of assuming `candidates[0].content.parts[0].text` is always present.

## 4. Multimodal Base64 Image Payloads

Formatting differs slightly between native Gemini and OpenAI-compatible endpoints:

### Gemini Native (`inlineData`)

Provide **raw base64 data without** the `data:image/png;base64,` prefix, paired with an explicit `mimeType`:

```json
{
  "contents": [
    {
      "role": "user",
      "parts": [
        { "text": "Describe the architecture diagram in this image." },
        {
          "inlineData": {
            "mimeType": "image/png",
            "data": "<RAW_BASE64_WITHOUT_PREFIX>"
          }
        }
      ]
    }
  ]
}
```

### OpenAI-Compatible (`image_url`)

Include the full data URI prefix (`data:image/png;base64,...`):

```json
{
  "model": "gemini-2.5-pro",
  "messages": [
    {
      "role": "user",
      "content": [
        { "type": "text", "text": "Describe this image." },
        {
          "type": "image_url",
          "image_url": {
            "url": "data:image/png;base64,<RAW_BASE64>"
          }
        }
      ]
    }
  ]
}
```

::: warning Payload Size & Encoding
Large uncompressed PNGs expand by ~33% when base64-encoded and can trigger HTTP `413` or `400` payload errors. Resize high-resolution screenshots and encode as JPEG or WebP when full lossless fidelity is unnecessary.
:::

## 5. Thinking Models vs. Image Generation Models

- **Thinking / reasoning models** (such as `gemini-2.5-pro` and `gemini-2.5-flash`): Spend part of `maxOutputTokens` on internal reasoning. Raise `maxOutputTokens` (for example, `4096` or higher) and allow generous read timeouts for complex analytical prompts.
- **Native Gemini image generation models**: Require the native `/v1beta` endpoint with image output enabled (`responseModalities` including `IMAGE`) rather than `/v1/chat/completions`. Follow the dedicated walkthrough in [Gemini Image Generation](/integrations/paint/banana).
- **Availability check**: Confirm the exact Gemini model ID is enabled in your API key's concrete model group on the console's **Pricing** page and healthy on [Channel Availability](/guides/channel-status).
