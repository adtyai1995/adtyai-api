# All API Hub

All API Hub is a browser extension and multi-account manager built for managing API relay accounts, checking live balances and model catalogs, and exporting ready-to-use client configurations in one click.

## Important: ADTY AI Uses Two Separate Domains

Before adding **ADTY AI** to All API Hub, keep our dual-domain architecture in mind (explained in full in [Quick Start & Dual Domains](/guides/overview)):

| Purpose | Canonical URL | Used For |
| --- | --- | --- |
| **Site / Console URL** | `https://www.adtyai.com` | Browser login, dashboard stats, wallet balance, key management, and pricing |
| **API Base URL** | `https://api.adtyai.com` | Actual model inference (`/v1/models`, `/v1/chat/completions`, `/v1/messages`) and exported client configs |

::: warning Do Not Swap the Two Domains
- Entering `https://api.adtyai.com` as the **Site URL** will fail account sync because the web console lives on `https://www.adtyai.com`.
- Exporting `https://www.adtyai.com` as the **API Base URL** into clients will cause API request failures because inference traffic must go to `https://api.adtyai.com`.
:::

## 1. Add ADTY AI to All API Hub

1. Sign in to [https://www.adtyai.com](https://www.adtyai.com) in your browser first if you plan to use browser-session detection.
2. Open the **All API Hub** extension and click **Add Account / Add Site**.
3. Enter the **Site URL**:

   ```text
   https://www.adtyai.com
   ```

4. Authenticate using either:
   - **Auto-Detect (Browser Session)**: Reads your active logged-in session on `https://www.adtyai.com` to fetch your account ID and generate/read management access.
   - **Manual Access Token**: Enter your **User ID** and Personal Access Token (PAT) from your account security page. See [Account Security & PAT](/guides/security) before creating or rotating a PAT.
5. In the account's advanced or endpoint settings, set the **API Base URL** (or custom API endpoint) to:

   ```text
   https://api.adtyai.com
   ```

6. Save the account and refresh to confirm your balance, group access, and token list load properly.

## 2. Manage API Keys & Inspect Models

Once connected, you can use All API Hub as a quick-reference panel alongside the console:

- **API Keys**: View existing keys, copy keys quickly, check remaining quotas, and see which channel group each key targets.
- **Model Catalog**: Inspect the models exposed to each key and verify current pricing and group coverage against [ADTY AI Pricing](https://www.adtyai.com/pricing).
- **Balance Monitoring**: Track wallet usage across keys without leaving your browser tab. For billing, top-ups, and vouchers, always open [https://www.adtyai.com](https://www.adtyai.com) (see [Wallet, Bonus & Vouchers](/guides/wallet-and-vouchers)).

## 3. One-Click Export to Supported Clients

All API Hub can export a selected API key and endpoint directly into clients such as **Cherry Studio**, **Claude Code**, **Codex CLI**, **OpenCode**, and **CC Switch**.

After exporting, quickly verify two fields in the target client:

1. **Base URL uses `https://api.adtyai.com`** (never `https://www.adtyai.com`), with `/v1` included or omitted according to the target client's protocol:
   - OpenAI-compatible SDKs, Codex, OpenCode: `https://api.adtyai.com/v1`
   - Cherry Studio, Claude Code, Claude Desktop, Anthropic SDK, Gemini SDK: `https://api.adtyai.com`
2. **Model IDs match your key's group** as listed on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing).

## Security Tips

- **Protect your Personal Access Token**: A PAT controls account-level resources, not just a single API key. Never paste a PAT into third-party chat clients—only export regular `sk-...` API keys to downstream tools.
- **Use scoped keys per app**: Create separate API keys with quota caps for each exported tool so you can revoke one key without disrupting others. See [Account Security & PAT](/guides/security).
