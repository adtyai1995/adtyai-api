# Image Generation Overview

**ADTY AI** supports both **Gemini Native Image Generation** and the **OpenAI Images API** through your existing API key and account balance. Choose the protocol that best fits your workflow—conversational multimodal editing with Gemini, or standardized image generation/editing endpoints with OpenAI-compatible SDKs.

::: warning Always Use the Dedicated API Domain
Send all image generation API requests to `https://api.adtyai.com`. Never send API calls to the web console domain `https://www.adtyai.com`, which returns **HTTP 403**.
:::

## Choosing the Right Image API

| Dimension | Gemini Native Image Generation | OpenAI Images API |
| --- | --- | --- |
| **Endpoint** | `POST https://api.adtyai.com/v1beta/models/{model}:generateContent` | `POST https://api.adtyai.com/v1/images/generations` & `/v1/images/edits` |
| **Typical Models** | `gemini-2.5-flash-image`, `gemini-3-pro-image-preview` *(check Pricing for live IDs)* | `gpt-image-1` *(and other image models on Pricing)* |
| **Best Strengths** | Multi-turn conversational editing, fine-grained text rendering, image-to-image transformation with natural instructions, combined text + image replies | Drop-in compatibility with existing OpenAI SDKs, transparent backgrounds, explicit size/quality controls, batch `n` generation |
| **Official SDK** | Python `google-genai` (`base_url="https://api.adtyai.com"`) | Python / Node `openai` (`base_url="https://api.adtyai.com/v1"`) |
| **Response Payload** | Base64 bytes inside `candidates[].content.parts[].inlineData` | `b64_json` (default for `gpt-image-1`) or temporary `url` depending on model |
| **Detailed Guide** | [Gemini Image Generation](/integrations/paint/banana) | [OpenAI Images API](/integrations/paint/gpt-image) |

## Check Live Enabled Image Models

Image model availability, resolutions, and per-image or token-based pricing depend on the active channels in your token's group. Before hardcoding a model slug in production:

1. Open the **Pricing** page at [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing).
2. Search for `image`, `gemini`, or `gpt-image` to confirm the exact model IDs currently enabled.
3. Verify your API key (created at [https://www.adtyai.com](https://www.adtyai.com) under **API Tokens**) belongs to a group that supports that model. See [Channel Availability](/guides/channel-status) for group selection guidance.

## Handling `base64` vs. `url` Responses

When integrating image APIs into backend services or scripts, plan for how image payloads arrive:

- **Base64 (`inlineData.data` or `b64_json`)**:
  - Returned inline as a base64-encoded PNG/JPEG/WebP string directly inside the JSON response.
  - **Pros**: Self-contained, never expires, does not require a secondary download call, and avoids upstream CDN timeouts.
  - **Best practice**: Decode immediately and persist to local disk or object storage (S3 / R2 / OSS); never log raw base64 payloads into application logs.
- **Temporary URL (`data[].url`)**:
  - Some legacy OpenAI-compatible image models return a short-lived signed URL when `response_format="url"` is supported.
  - **Best practice**: Download and re-host URL-based outputs immediately after generation, because upstream signed links typically expire within minutes to an hour.

## Next Steps

- Build with Gemini multimodal image generation & editing: [Gemini Image Generation](/integrations/paint/banana)
- Build with OpenAI Images API (`gpt-image-1`): [OpenAI Images API](/integrations/paint/gpt-image)
- Review full endpoint conventions: [API Reference](/api/)
- Troubleshoot Gemini or general API issues: [Gemini FAQ](/faq/gemini) & [General FAQ](/faq/)
