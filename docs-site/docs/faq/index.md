# General FAQ

Answers to the most common questions about ADTY AI domains, error codes, billing, model groups, streaming, and support diagnostics.

## Why am I getting HTTP `403` with `api_domain_required`?

You are sending model API requests to the web console domain (`https://www.adtyai.com`) instead of the dedicated API gateway domain (`https://api.adtyai.com`).

**How to fix it:**

1. Replace `https://www.adtyai.com` with `https://api.adtyai.com` in your client config or environment variables.
2. Check whether your client expects `/v1` at the end:
   - OpenAI SDKs, Codex CLI, OpenCode: `https://api.adtyai.com/v1`
   - Claude Code, Anthropic SDKs, Gemini `google-genai` SDK: `https://api.adtyai.com`
3. Restart your terminal or client so updated environment variables take effect.

See [Quick Start & Dual Domains](/guides/overview) for details.

## What do `401`, `403`, `429`, and `5xx` errors mean?

- **`401 Unauthorized`**: Your API key is missing, mistyped, expired, disabled, or revoked (including if a team employee was disabled or removed from a team). Verify the exact `sk-...` key in **API Keys** and check that you are sending the right header (`Authorization: Bearer` vs `x-api-key` vs `x-goog-api-key`).
- **`403 Forbidden` (`insufficient_user_quota` / Insufficient Quota)**: ADTY AI returns HTTP `403` (error code `insufficient_user_quota`) when your wallet balance (or shared team wallet balance) is too low to cover the request, or when the specific API key reaches its configured USD quota limit. Top up in **Wallet** or raise the key's quota cap. *(Other `403` responses include `api_domain_required` or model/IP allowlist denials. If HTTP `402` appears, it can only come from an upstream provider passthrough rather than ADTY AI's balance check.)*
- **`429 Too Many Requests`**: Request-per-minute (RPM), token-per-minute (TPM), or concurrent-request capacity was temporarily exceeded. Slow down parallel bursts and retry using exponential backoff.
- **`500` / `502` / `503` / `504`**: A transient upstream provider error or timeout occurred. Retry after a short delay, and inspect [Channel Availability](/guides/channel-status) to check whether the upstream route is experiencing an incident.

## How do USD billing, quotas, and token counting work?

- **USD accounting**: Your wallet balance and every charge in **Usage Logs** are denominated in USD.
- **Token-based calculation**: Each model has published base rates for input tokens, output tokens, and (where applicable) prompt cache reads/writes or image generation units. Your actual cost equals the measured usage multiplied by the pricing multiplier of the concrete model group assigned to your API key.
- **Reasoning & cached tokens**: Reasoning models count internal reasoning tokens as part of output billing per upstream specification. Prompt cache hits significantly reduce input token cost on supported models—see [Claude Code Cache Fix](/integrations/cli/cache-fix) if you use Claude Code heavily.
- **No hidden overhead**: Every completed call shows its exact token counts and USD cost inside **Usage Logs**.

## How should I choose a model group when creating an API key?

Every API key must be assigned to a **concrete model group** listed on the console's **Pricing** page. To pick the right group:

1. Open **Pricing** in the console at [https://www.adtyai.com](https://www.adtyai.com) and inspect which concrete groups include the models you need and at what pricing multiplier.
2. Open [https://www.adtyai.com/model-status](https://www.adtyai.com/model-status) ([Channel Availability](/guides/channel-status)) and compare 24-hour heartbeat stability, latency, and success rate across those groups.
3. Select your preferred concrete group when creating the API key.

If you need different routing tiers for different tools—such as one group for interactive coding in Claude Code and another for batch jobs—create separate API keys bound to each concrete group.

## Why do long streaming requests time out or disconnect mid-response?

Mid-stream disconnects are usually caused by one of four factors:

1. **Aggressive client timeout**: Many HTTP clients default to a 30-second or 60-second read timeout. Reasoning models or complex coding tool calls may pause silently before streaming tokens. Raise your client's read/stream timeout to at least `180s`–`300s`.
2. **Local proxy or VPN buffering**: Some corporate proxies or VPN clients buffer Server-Sent Events (SSE) or close idle TLS sockets early. Test with direct connectivity or a stable proxy configuration.
3. **Oversized prompt context**: Very large conversation histories increase time-to-first-token (TTFT). Compact long threads periodically.
4. **Upstream provider instability**: Verify the model's live 24-hour heartbeat on [Channel Availability](/guides/channel-status).

## How do I find a Request ID for a support ticket?

When opening a ticket in the [Support Center](/guides/support) about a failed or unexpectedly billed call:

1. Sign in to [https://www.adtyai.com](https://www.adtyai.com) and open **Usage Logs**.
2. Filter by time range, API key, or model name to locate the relevant entry.
3. Click or expand the row to copy the **Request ID** (also returned in response headers on API calls when available).
4. Paste the Request ID, timestamp, model name, and error message into your support ticket so staff can trace the exact gateway log immediately.

## Tool-Specific Troubleshooting

- [Claude Code FAQ](/faq/claude-code)
- [Codex CLI FAQ](/faq/codex)
- [Gemini FAQ](/faq/gemini)
