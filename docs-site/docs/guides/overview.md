# Quick Start & Dual Domains

Get from a new account to your first verified model response on ADTY AI in under five minutes.

## Four-Step Quick Start

1. **Create or sign in to your account**
   Visit the web console at [https://www.adtyai.com](https://www.adtyai.com) to register or sign in. We recommend enabling 2FA early—see [Account Security & PAT](/guides/security).
2. **Fund your wallet**
   Open **Wallet** in the console to add USD balance via an available online payment channel or redeem a quota voucher. See [Wallet, Bonus & Vouchers](/guides/wallet-and-vouchers).
3. **Create an API key with a concrete model group**
   Before generating a key, check **Pricing** and [Channel Availability](/guides/channel-status) to select the concrete model group that carries the models, pricing tier, and reliability profile your workload needs. Then open **API Keys**, create a new key, bind it to that concrete model group, and copy the `sk-...` secret.
4. **Point your client to the API domain**
   Configure your CLI tool, desktop app, or SDK to use `https://api.adtyai.com` (with or without `/v1` depending on the protocol—see [API Reference](/api/)).

## Dual-Domain Separation

ADTY AI intentionally separates web console traffic from high-throughput model API traffic across two domains:

| Domain | Primary Role | Model API Calls (`/v1/*`, `/v1beta/*`) | Console Access |
| :--- | :--- | :--- | :--- |
| `https://www.adtyai.com` | Web Console & Documentation | **Rejected with HTTP `403`** (`api_domain_required`) | Yes |
| `https://api.adtyai.com` | Dedicated Model API Gateway | **Supported** (all OpenAI, Anthropic, Gemini routes) | Also supported |

::: danger Why `www` Rejects API Requests
Sending model requests to `https://www.adtyai.com/v1/...` or `https://www.adtyai.com/v1beta/...` will always fail with HTTP `403` and error code `api_domain_required`.

This isolation protects API latency and streaming stability from web frontend traffic and CDN rules. Always configure `https://api.adtyai.com` as your client's base URL.
:::

### Quick Base URL Cheat Sheet

Different client ecosystems expect slightly different URL suffixes:

- **OpenAI-compatible clients & Codex CLI**: `https://api.adtyai.com/v1`
- **Anthropic SDK & Claude Code**: `https://api.adtyai.com` *(do **not** append `/v1`—the client appends `/v1/messages` itself)*
- **Gemini Native SDK (`google-genai`)**: `https://api.adtyai.com` *(the SDK appends `/v1beta/models/...` automatically)*

## Quick Smoke Test with `curl`

Replace `<YOUR_API_KEY>` with a key created in the console and verify connectivity against the OpenAI-compatible Chat Completions endpoint.

### macOS / Linux

```bash
curl -sS https://api.adtyai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -d '{
    "model": "gpt-5.4",
    "messages": [
      { "role": "user", "content": "Reply with OK and nothing else." }
    ],
    "max_tokens": 32
  }'
```

### Windows PowerShell

```powershell
curl.exe -sS https://api.adtyai.com/v1/chat/completions `
  -H "Content-Type: application/json" `
  -H "Authorization: Bearer <YOUR_API_KEY>" `
  -d "{\"model\":\"gpt-5.4\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with OK and nothing else.\"}],\"max_tokens\":32}"
```

A successful response returns HTTP `200` with a JSON payload containing `choices[0].message.content` and a `usage` token summary:

```json
{
  "id": "chatcmpl-01J9EXAMPLE",
  "object": "chat.completion",
  "model": "gpt-5.4",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "OK"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 14,
    "completion_tokens": 2,
    "total_tokens": 16
  }
}
```

::: tip Choosing a Model for the Smoke Test
Make sure the `model` value in your test request is included in the concrete model group assigned to your API key. You can list all models available to a key by sending `GET https://api.adtyai.com/v1/models` with the same `Authorization: Bearer` header.
:::

## Next Steps

- Generate copy-paste client configs using the [Interactive Setup Wizard](/integrations/cli/).
- Explore all supported endpoints and headers in the [API Reference](/api/).
- Troubleshoot common setup mistakes in the [General FAQ](/faq/).
