# Usage & Balance Query in CC Switch

Keeping an eye on token consumption and remaining quota helps you avoid sudden interruptions mid-session. You can monitor your **ADTY AI** usage in two complementary ways: directly in the ADTY AI web console, or automatically inside **CC Switch** via its usage/balance query feature.

## 1. Real-Time Tracking in the ADTY AI Console

The ADTY AI web console is always the authoritative source of truth for billing, bonus credits, and request-level details:

- **Request Logs & Token Breakdown**: Visit [https://www.adtyai.com/logs](https://www.adtyai.com/logs) to inspect every API call, model ID, prompt/completion/cache token counts, latency, and exact cost per request.
- **Wallet Balance & Credits**: Visit [https://www.adtyai.com/wallet](https://www.adtyai.com/wallet) to view your remaining cash balance, bonus balance, top-up history, and voucher redemptions (see [Wallet, Bonus & Vouchers](/guides/wallet-and-vouchers)).
- **Team Shared Wallet**: If your API key belongs to a team workspace, view shared consumption under the team panel (see [Team Collaboration](/guides/teams)).

## 2. Configuring Usage & Balance Query in CC Switch

CC Switch supports displaying remaining quota or usage directly on each provider card using either OpenAI-compatible billing endpoints or a custom JavaScript extractor script.

::: warning Always Query the API Domain
All billing and token-usage API endpoints below must target **`https://api.adtyai.com`**—never `https://www.adtyai.com` (relay billing routes on the web domain return HTTP `403`).
:::

### Method A: OpenAI-Compatible Billing Endpoints (Recommended)

ADTY AI implements the standard OpenAI billing query endpoints using your existing provider API key (`Authorization: Bearer <YOUR_API_KEY>`):

- **Total / Limit**: `GET https://api.adtyai.com/v1/dashboard/billing/subscription`
  - Returns `hard_limit_usd` (total quota limit in the site's display currency; unlimited keys return `100000000`) and `access_until` (expiration Unix timestamp, or `0` if non-expiring).
- **Used Quota**: `GET https://api.adtyai.com/v1/dashboard/billing/usage`
  - Returns `total_usage` in **cents** (divide by `100` to get the display amount).

You can verify both endpoints in your terminal first:

```bash
curl -s https://api.adtyai.com/v1/dashboard/billing/subscription \
  -H "Authorization: Bearer sk-your-api-key"

curl -s https://api.adtyai.com/v1/dashboard/billing/usage \
  -H "Authorization: Bearer sk-your-api-key"
```

#### CC Switch Custom Usage Script Example

In CC Switch, open your **ADTY AI** provider card → **Usage Query / Custom Script** settings, and paste an extractor script that reads the active provider's `apiKey` and `baseUrl`:

```javascript
async function queryUsage({ apiKey }) {
  const apiBase = "https://api.adtyai.com";
  const headers = {
    Authorization: `Bearer ${apiKey}`,
    Accept: "application/json",
  };

  const [subRes, usageRes] = await Promise.all([
    fetch(`${apiBase}/v1/dashboard/billing/subscription`, { headers }),
    fetch(`${apiBase}/v1/dashboard/billing/usage`, { headers }),
  ]);

  if (!subRes.ok || !usageRes.ok) {
    throw new Error(`Query failed: ${subRes.status} / ${usageRes.status}`);
  }

  const sub = await subRes.json();
  const usage = await usageRes.json();

  const total = Number(sub.hard_limit_usd ?? 0);
  const used = Number(usage.total_usage ?? 0) / 100;
  const isUnlimited = total >= 100000000;

  return {
    isValid: true,
    total: isUnlimited ? null : Number(total.toFixed(4)),
    used: Number(used.toFixed(4)),
    remaining: isUnlimited ? null : Number(Math.max(0, total - used).toFixed(4)),
    unit: "USD",
    extra: isUnlimited ? "Unlimited key quota" : undefined,
  };
}
```

::: tip Adjust the Return Shape to Your CC Switch Version
Exact function signatures and field names (`remaining`, `used`, `total`, `unit`, or `extra`) can vary slightly across CC Switch releases. Check the built-in template shown inside CC Switch's Usage Script editor and map `total`, `used`, and `remaining` to the template's expected return keys.
:::

### Method B: ADTY AI Token Usage Endpoint (`/api/usage/token/`)

If you want richer per-key metadata—including whether a key has unlimited quota, model allowlists, expiration timestamp, and (when enabled on the key) underlying account or team wallet balance—query ADTY AI's read-only token usage endpoint:

```bash
curl -s https://api.adtyai.com/api/usage/token/ \
  -H "Authorization: Bearer sk-your-api-key"
```

Example response fields inside `data`:

```json
{
  "code": true,
  "message": "ok",
  "data": {
    "object": "token_usage",
    "name": "Claude Code Key",
    "total_granted": 5000000,
    "total_used": 1250000,
    "total_available": 3750000,
    "unlimited_quota": false,
    "model_limits_enabled": false,
    "expires_at": 0
  }
}
```

- **Key-level quota**: `total_granted`, `total_used`, and `total_available` report the API key's internal quota units.
- **Optional account / team balance**: If you enable **Allow user quota query** on that API key in the ADTY AI console, the response also includes `user_quota` (for personal keys) or `team_quota` (for team keys—team keys only report the team shared wallet and never expose a member's personal wallet).

### Method C: Account-Level Queries with a Scoped PAT

If you are building a custom script that inspects broader account information beyond a single relay API key, generate a dedicated **Personal Access Token (PAT)** with the minimum required read permissions from your account security page instead of using browser cookies or passwords. See [Account Security & PAT](/guides/security) for setup and rotation instructions.

## Security Hygiene for Usage Scripts

- **Prefer scoped API keys**: Use the same per-client API key configured for that CC Switch profile (ideally with a quota limit set) rather than pasting an unlimited master key into multiple tools.
- **Never hardcode secrets in shared scripts**: Always read the key from CC Switch's injected provider context (`apiKey`) instead of pasting literal `sk-...` strings or PATs into script templates you might share.
- **Keep query intervals reasonable**: Avoid polling every few seconds; manual refresh or periodic intervals (such as every 5–15 minutes) prevent hitting rate limits.
- **Scrub exports and screenshots**: Redact API keys and PATs before exporting CC Switch profiles or posting troubleshooting screenshots.

## Troubleshooting

- **HTTP `401 Unauthorized`**: The API key is revoked, disabled, or missing the `Bearer ` prefix in the `Authorization` header.
- **HTTP `403 Forbidden`**: Verify the script calls `https://api.adtyai.com`, not `https://www.adtyai.com`.
- **Shows `100000000` as total limit**: Your API key is configured with **Unlimited Quota**. Either set a finite per-key quota cap in the ADTY AI console or check your account wallet balance at [https://www.adtyai.com/wallet](https://www.adtyai.com/wallet).
