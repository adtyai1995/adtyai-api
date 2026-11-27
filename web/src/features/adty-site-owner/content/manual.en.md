# Site Owner Operations Manual & Guidelines

> **Audience**: Authorized subsite owners and subsite operations managers  
> **Visibility**: This manual is only available inside the authenticated console for site owners and platform administrators. Exact limits, supply discounts, tier thresholds, and deposit rules follow the live settings shown in your Site Owner Center and platform policy.

---

## 1. Account Scope & Workspace Overview

- **Pure Management Account**: Subsites are provisioned by platform administrators with a dedicated management-only owner account. Pure site owners only see the **Site Management** and **Account** sidebar groups—no personal API keys, no personal consumption wallet, no direct model relay calls, and no personal referral participation.
- **Strict Tenant Isolation**: You can only view and manage customers, teams, orders, tickets, earnings, and ledgers belonging to your own subsite. Upstream model channels, platform internal wallets, and other subsites are never exposed.
- **Sidebar Overview**:
  - **Site Management**: `Site Overview`, `Customers`, `Team management`, `Order management`, `Pricing & Multiplier`, `Recharge Bonus`, `Promotion Management`, `Earnings Details`, `Earnings Withdrawals`, `Settlement Sheets`, `Owner Wallets`, `Redemption Codes`, `Notifications`, `Staff accounts`, `Ticket management`, `Basic Authentication`, `Email configuration`, `Site settings`, `Site Owner Manual`.
  - **Account**: `Historical site accounts` (shown only if you previously owned a closed or transferred subsite), `Security & Access`.

---

## 2. Quick Launch Checklist for a New Subsite

After receiving your owner account and default domains, complete these steps in order:

1. **Enable Two-Factor Authentication (2FA)** (`Security & Access`): Bind a TOTP authenticator app and store recovery codes offline. Sensitive actions (pricing changes, vouchers, withdrawals, password resets, staff management) require step-up verification.
2. **Configure Branding & Support** (`Site settings → Brand`): Set your site name, page title, announcement, wallet notice, support contact, upload PNG/JPEG/WebP Logo & Favicon (≤ 200 KB), and click **Save brand**. If using Tawk.to live chat, fill in both `property_id` and `widget_id`.
3. **Bind Custom Dual Domains (Optional)** (`Site settings → Custom domains`): Copy the platform's **default DNS CNAME target**, prepare two **distinct subdomains** (such as `www.yourdomain.com` for the website and `api.yourdomain.com` for API requests—**apex/root domains are prohibited**), submit them together, and add the generated `TXT` + `CNAME` records within 72 hours. If Mainland China homepage acceleration is enabled by the platform, you may optionally complete its two-step setup for your active custom website domain.
4. **Publish Legal Documents** (`Site settings → Multilingual legal pages`): Subsites **never inherit** the main site's legal documents. Fill in `zh-CN` and `en` versions of your User Agreement, Privacy Policy, and Refund Policy as needed; publishing a User Agreement or Privacy Policy automatically enables the sign-up consent checkbox and footer links.
5. **Configure Outbound Email & Test** (`Email configuration`): Subsites do not use a platform default mail transport. Configure **Custom SMTP** or your own **Resend API**, click **Save**, and then send a test email to confirm delivery. Until configured, subsite operational emails return an error prompting configuration.
6. **Configure Basic Authentication** (`Basic Authentication`): Independently manage password login, new user sign-up, password registration, email verification (**disabled by default** and requires Step 5 outbound email first), email domain/alias restrictions, and the domain whitelist—completely isolated from main site settings.
7. **Review Supply Cost Factor & Set Retail Multiplier** (`Site Overview` → `Pricing & Multiplier`): Verify your `cost_factor` and configure a unified site retail multiplier (`1.0x – 10.0x`, default `1.0x` with zero markup).
8. **Configure Marketing Tools (Optional)** (`Recharge Bonus` / `Promotion Management`): Before enabling bonuses or referrals, ensure your **Earnings Wallet available balance** (or authorized margin cycle allowance) is sufficient so customer checkouts are never blocked.

---

## 3. Feature-by-Feature Operations Guide

### 3.1 Site Overview

- Track key date-range metrics: new customers, paying customers, active customers, customer spend, platform supply cost, owner spread earnings, bonus spend, refunds, and daily trends.
- Displays current site status (`active` or `closing` countdown), website URL, API URL, and active supply cost factor (`cost_factor`).
- Any `skipped_usd` figure indicates usage temporarily flagged for platform risk review, not missing funds.

### 3.2 Customers

- **Customer List**: Paginated by sign-up time (20 per page), automatically excluding subsite staff accounts and standalone team staff sub-accounts.
- **Visibility Controls**: Visibility of email addresses (masked by default, e.g. `a***@example.com`), balances, top-ups, usage, request logs, and API key lists is governed by platform policy. Request logs strip customer IPs and internal channel IDs; full plaintext API keys are **never shown**.
- **Exact Lookup**: Accepts only an **exact username or exact email** (no fuzzy search), rate-limited per hour (default **20 lookups/hour**, **counting misses too**) with audit logging.
- **Reset Customer Password**: Set manually (8–128 chars) or generate a random password (12 / 16 / 20 / 24 chars, **shown once only**). Immediately revokes all active sessions and PATs for that customer; requires **step-up verification**; disabled while closing.
- **Report Customer**: Submit suspected abuse or policy violations (≤ 1000 chars) to platform risk control.
- **Important**: Direct manual balance credits by site owners are retired. Always issue customer quota via **Redemption Codes** (`vouchers`).

### 3.3 Team Management

Provides three tabs (`Teams`, `Create team`, `Team registration links`) matching the main site's team management capabilities:

1. **Teams & Details (`Teams`)**: View your subsite's active/closed teams, members, bound API keys, and shared balance ledger.
2. **Create Team (`Create team`)**: Appoint an enabled customer of your subsite as team leader with a team name (1–64 chars) and supply price multiplier (`1.0` – `10.0`, blank defaults to `1.0`). Cross-site customers are rejected; disabled while closing.
3. **Team Registration Links (`Team registration links`)**: Issue one-time registration links (1–168 hours validity, shown once, bound to your subsite's public domain) and **revoke** unused links.
4. **Adjust Active Team Supply Multiplier**: Valid range **`1.0` – `10.0`** (up to 4 decimals). Billing formula: `Team Price = Subsite Supply Cost × Team Multiplier` (cannot go below `1.0x`). Changes affect only future requests.
5. **Boundary**: Manual team balance top-ups remain permanently disabled; team leaders recharge online.

### 3.4 Orders & Customer Refunds

- Filter unified subsite orders by `trade_no`, username, `tx_hash`, order type, status, and payment method.
- **Manual Refunds**: Initiate partial or full refunds on your **customer orders** through the 7-step state machine: `Initiate → Approve → Reclaim Quota → Handle Bonus → Clawback Commission → Record Payout Proof → Complete` (or `Terminate`).
- Every step requires **digest-bound step-up verification**. You cannot refund owner margin top-up orders.

### 3.5 Pricing & Multiplier

- **Single Unified Retail Multiplier**: Configure one site-wide multiplier `M` between **`1.0` and `10.0`** (up to 4 decimals; default `1.0`).
- **Profit Formula**:
  - `Subsite Supply Cost = Main Group Price × Supply Cost Factor (d)`
  - `Customer Price = Subsite Supply Cost × Retail Multiplier (M) = Main Group Price × d × M`
  - `Owner Spread Earnings = Customer Charge − Supply Cost = Supply Cost × (M − 1)`
- **Immediate Effect & Price-Increase Email**:
  - Changes take effect **immediately** after **step-up verification**.
  - **Every time you increase `M`, the system automatically emails all customers with verified emails on your subsite.** Double-check decimal digits before saving to avoid frequent price-hike notices.

### 3.6 Recharge Bonus

- Configure per enabled payment method (`Alipay`, `WeChat Pay`, `USDT` — all USDT chains share one rule) from `0%` to `100%`. Applies only to regular customer online wallet top-ups (not team top-ups or owner margin top-ups). Requires **step-up verification**.
- **Funding Pre-Freeze Warning**:
  - The bonus amount is pre-frozen the instant a customer creates an online top-up order—first from **available earnings**, and then from your **margin cycle allowance** only if platform admins authorized margin bonus coverage.
  - **If available funds cannot cover the bonus, customer checkout fails immediately.**
  - Canceled/expired orders release the pre-freeze; refunds only claw back the unconsumed bonus proportion.

### 3.7 Promotion Management

Includes summary KPIs, a **Cover Pending Commissions** button, and 5 tabs (`Withdrawals`, `Affiliates`, `Commissions`, `Audit Logs`, `Settings`).

- **Prerequisites**:
  1. `Default commission rate`: **`0% – 30%`** (hard cap 30%).
  2. `Pending coverage cap (USD)`: must be **`> 0`**.
  3. `Observation / freeze days`: integer **`>= 0`**.
- **Attribution & Risk Controls**: Single-tier direct invites; self-invites and circular invites are blocked; invitees must bind before their first paid order; team top-ups do not generate commissions.
- **Coverage Rule**: Covered first from available earnings, then from authorized margin cycle allowance. Uncovered commissions wait in `pending_coverage`; **the observation countdown only begins once a commission is 100% funded**.
- **Affiliate Payouts**:
  - **Transfer to Balance**: Always 0% fee, no minimum, instant, no manual review.
  - **External Cash Withdrawal**: Optionally enable **USDT** (`TRC20 / BEP20 / POLYGON / XLAYER`) and/or **Alipay** (with CNY rate). WeChat Pay is not supported. After approving a withdrawal, **you transfer funds offline** and record the `TxID` or upload payout proof (≤ 5 MB).
- **Automatic Docs Sync**: Changes to your referral switch, default commission rate, observation days, and cash withdrawal methods automatically sync to your subsite's `/docs/` Referral Program guide (and hide the sidebar entry when disabled).

### 3.8 Earnings Details & Exports

- Inspect per-request model, group, customer ID, request ID, customer spend, and owner spread earnings (50 rows/page).
- Export CSV reports (`Earnings Ledger`, `Operations Report`, `Customer Summary`) up to 100,000 rows per export with CSV-formula sanitization and masked customer emails.

### 3.9 Earnings Withdrawals

- **USDT Only (4 Networks)**: `TRC20` (starts with `T`) and `BEP20 / POLYGON / XLAYER` (starts with `0x`). Alipay is not supported for owner withdrawals.
- Live minimum amount, fee, and exchange rate are displayed on the withdrawal form. Submission requires **digest-bound step-up verification**.
- Confirmed spread earnings credit to available earnings in real time. If a daily reconciliation hold appears, contact platform support.
- Pending requests can be self-canceled until platform admins mark them `paying`.

### 3.10 Settlement Sheets

- Generated daily for the previous business day (default `Asia/Shanghai`). Post-settlement refunds or adjustments create versioned revisions you can compare side by side.
- Supports one-click **Export CSV** and clean A4 **Save as PDF**.

### 3.11 Owner Wallets, Margin Pools & Site Tiers

Displays your **Earnings Wallet (`earnings`)** and **Margin Wallet (`margin`)** alongside full transaction ledgers.

#### Two Separate Margin Pools

| Pool | Used For | Platform Authorization | Notes |
|---|---|---|---|
| **90% Cycle Allowance** (10% reserve floor) | Customer recharge bonus coverage & referral commission coverage | **Required** (independent switches) | Once exhausted, a **new real deposit** (online margin top-up, earnings-to-margin transfer, margin recharge code redemption, or admin credit) opens a fresh cycle and clears backlogged items |
| **Excess Margin** (above 100% tier floor) | Customer quota voucher issuance when earnings are insufficient | **Not required** (automatic) | Does not consume the 90% cycle pool and never breaches your tier's 100% deposit floor; check this page or contact platform support for exact deposit requirements |

- **Funding Margin**: Online margin top-up, instant earnings-to-margin transfer (no step-up needed, starts a new cycle immediately), or redeeming a platform-issued `site_margin` recharge code.
- **Site Tiers**: Evaluated automatically based on cumulative net valid customer recharge principal (owner top-ups, margin deposits, and team top-ups do not count). Refer to your Site Owner Center or contact platform support for tier and supply discount details.

### 3.12 Redemption Codes

- Issue customer quota vouchers (`site_customer`): denomination `> 0` and `≤ $100,000` (up to 2 decimals), batch size `1 – 100`, with a required reason.
- Funded by **available earnings first, then excess margin**.
- **Two Critical Rules**:
  1. **Plaintext codes are shown once only**: Copy or export `.txt` / `.csv` immediately before closing the modal.
  2. **Freeze is irreversible (no Unfreeze button)**: Freezing blocks redemption without returning funds. To reclaim locked funds, you must **Cancel (Invalidate)** the voucher.
- Issue, freeze, and cancel all require **step-up verification**.

### 3.13 Notifications & Announcements

- Publish targeted announcements to your subsite's regular users and/or team users (title ≤ 120 chars, Markdown/rich text sanitized up to 2000 plain-text chars).
- Published announcements **cannot return to draft**; they can only be recalled (archived read-only).

### 3.14 Staff Accounts

- **Browser-Only Security**: Strictly requires a live browser session (**PAT access is prohibited**). Create, edit, password reset, and delete all require **digest-bound step-up verification**.
- Username ≤ 20 chars (no `@`, globally unique); **staff passwords must be 15–128 characters**.
- Granular permissions (6 total): `tickets.manage`, `customers.read`, `customers.balance`, `customers.topups`, `customers.usage`, `customers.disable`.
- Staff never see owner wallets/earnings/withdrawals, unmasked customer emails, or API keys, and cannot reset customer passwords.
- Editing, resetting password, disabling, or deleting staff **immediately revokes their active sessions**; disabling or removing `tickets.manage` automatically unassigns their open tickets.

### 3.15 Ticket Management

- Handle subsite customer tickets with queue/status/priority/assignee filters, JSON export, public replies, internal staff notes, and assignment to owner or eligible staff.
- Image attachments: ≤ 5 MB each, ≤ 6 per ticket, ≤ 20 MB total; API-key-like strings are automatically redacted.

### 3.16 Basic Authentication

Independently configure sign-in, registration, and email validation rules for your subsite customers (fully isolated from the main site's global switches). Saving requires **step-up verification**:

- **Allow password login** (default on), **Allow new user registration** (default on), and **Allow password registration** (default on).
- **Enable email verification** (**disabled by default** on subsites): Requires valid Custom SMTP or Resend credentials saved under **Email configuration** first; disabled in the UI and rejected by the server until outbound mail is configured.
- **Enable email domain restriction** (default off) & **Email domain whitelist** (up to 200 domains; cannot be empty when domain restriction is enabled), and **Enable email alias restriction** (default off; blocks `+` aliases).

### 3.17 Email Configuration

Choose one of two mutually exclusive outbound mail modes (secrets are stored encrypted and never displayed back):
1. **Custom SMTP (`smtp`)**: Configure host, port, encryption (`None` / `SSL/TLS` / `STARTTLS`), credentials, and sender address. `localhost` and private/internal IPs are blocked (SSRF protection).
2. **Own Resend API (`resend`)**: Use your own Resend API key and verified sender domain.

- **Unconfigured Policy**: There is no platform default transport for subsites. Until valid SMTP or Resend credentials are saved, subsite operational mail sends return an error asking you to configure SMTP or Resend first (official site-closure migration emails continue to be delivered by the platform).
- Always click **Save** before clicking **Send test email**.

### 3.18 Site Settings

1. **Brand**: Name, website title, announcement, wallet notice, support contact, optional custom external docs URL (leave blank to use the built-in white-label `/docs/`), Tawk.to IDs, and Logo / Favicon (PNG/JPEG/WebP ≤ 200 KB; **click Save brand after uploading**).
2. **Custom Homepage**: Markdown, HTML, or iframe URL; clear to restore the default homepage.
3. **Multilingual Legal Pages**: Independent `en` and `zh-CN` User Agreement, Privacy Policy, and Refund Policy (never inherited from main site).
4. **Custom Domains**:
   - Submit two **distinct subdomains** (`Website Domain` + `API Domain`, **never an apex/root domain**) together and complete `TXT + CNAME` verification within **72 hours**.
   - **Dual-Domain Boundary**: Website domain serves pages/console only (calling `/v1` returns HTTP 403); API domain serves model requests (and console access) with `noindex`.
   - **Mainland China Homepage Acceleration (Optional)**: Supported for active custom **Website Domains** only—Step 1 adds `_acme-challenge` CNAME delegation until the edge certificate is ready; Step 2 adds the Mainland China DNS line CNAME to the platform's mainland target. **Never configure Mainland DNS routing on an API domain.**
5. **Search Engine Indexing**: Toggle indexing for your website domain (API domain is always `noindex`).
6. **Request Site Closure**: Submit a reason for platform review and scheduled notice period.

### 3.19 Historical Site Accounts

For closed or transferred subsites previously owned by your account: inspect historical earnings, submit and track remaining USDT withdrawals, export past settlement sheets, and remove archived website domains.

### 3.20 Security & Access

Manage password, email binding, active sessions, 2FA, and scoped Personal Access Tokens (select matching `site_owner_*` scopes for site management endpoints; for site-owner ticket endpoints `/api/adty/tickets*`, select **both** `profile:read` + `site_owner_customer:read` for read requests or **both** `profile:write` + `site_owner_customer:write` for write actions; staff management endpoints reject PATs).

---

## 4. Step-Up Verification & PAT Matrix

| Action | Step-Up Required | Digest-Bound | PAT Allowed |
|---|:---:|:---:|:---:|
| Save retail multiplier (`Pricing & Multiplier`) | Yes | No | Yes (`site_owner_setting:write`) |
| Save recharge bonus (`Recharge Bonus`) | Yes | No | Yes (`site_owner_order:write`) |
| Save basic authentication (`Basic Authentication`) | Yes | No | Yes (`site_owner_setting:write`) |
| Save email configuration (`Email configuration`) | Yes | No | Yes (`site_owner_setting:write`) |
| Delete active or archived custom domain | Yes | No | Yes (`site_owner_setting:write`) |
| Freeze / cancel customer vouchers | Yes | No | Yes (`site_owner_order:write`) |
| Reset customer password | Yes | No | Yes (`site_owner_customer:write`) |
| Issue customer vouchers | Yes | **Yes** | Yes (`site_owner_order:write`) |
| Submit USDT earnings withdrawal | Yes | **Yes** | Yes (`site_owner_order:write`) |
| Initiate / advance customer manual refund | Yes | **Yes** | Yes (`site_owner_order:write`) |
| Create / edit / reset / delete staff account | Yes | **Yes** | **No (live browser session only)** |
| Brand, homepage, legal, SEO, referral settings, team multiplier, earnings-to-margin, notices, closure request | **No** | — | Yes (matching `site_owner_*` scope) |
| Ticket management (`/api/adty/tickets*`) | **No** | — | Yes (both `profile:read` + `site_owner_customer:read` for reads, or both `profile:write` + `site_owner_customer:write` for writes) |

---

## 5. Site Closure Notice Period & Asset Handling

- **Notice Period (`status = closing`)**: New sign-ups stop and customers receive official migration notices. Write actions for pricing, bonuses, vouchers, site settings, password resets, notices, and adding/expanding staff are locked; you may continue withdrawing owner earnings, reviewing existing affiliate withdrawals, closing tickets, disabling/deleting staff, and removing custom domains.
- **After Scheduled Closure**:
  1. Customers and teams migrate automatically to the main site with full balances and order history; legacy subsite API keys expire and customers generate new keys after signing in on the main site.
  2. Confirmed owner earnings remain fully withdrawable via USDT under `Historical site accounts`.
  3. Unredeemed `site_customer` vouchers carry over for migrated customers only; unused margin recharge codes are canceled.
  4. Open tickets transfer to the main site; custom website domains switch to redirect mode and API domains unbind automatically.

---

## 6. Key Operational Pitfalls to Avoid

1. **Check available funds before enabling Recharge Bonus**: Insufficient earnings without authorized margin coverage will cause customer top-up creation to fail.
2. **Keep the two margin pools distinct**: The 90% cycle pool backs authorized bonus/commission coverage; excess margin backs voucher issuance.
3. **Increasing the retail multiplier takes effect immediately and emails customers**: Verify decimals carefully before saving.
4. **Voucher codes are shown once; Freeze has no Unfreeze**: Export codes immediately; use Cancel to return locked funds.
5. **Use vouchers for offline quota grants**: Direct manual credits to customers or teams are permanently retired.
6. **Site tier progress counts only net valid customer recharge principal**: Owner deposits, margin top-ups, and team top-ups do not qualify.
7. **Owner withdrawals are USDT-only across 4 networks**: Double-check network and address prefix (`T...` for TRC20, `0x...` for EVM chains).
8. **Affiliate balance transfer is always free and instant**: Account for the up to 30% cap when pricing; observation days only start once a commission is 100% funded.
9. **Affiliate cash withdrawals are paid offline by the site owner**: Transfer via USDT/Alipay and upload `TxID` or receipt proof.
10. **Never bind apex domains, and always share your API Domain with customers**: Website domains reject `/v1` calls with 403; Mainland acceleration applies only to custom website domains after `_acme-challenge` certificate issuance.
11. **Subsite legal pages never fall back to main site text**: Fill both `en` and `zh-CN` where needed.
12. **Custom SMTP blocks internal IPs—save before testing**; always click **Save brand** after uploading logos.
13. **Exact customer lookup consumes hourly quota even on misses** (default 20/hour).
14. **Staff passwords require at least 15 characters**, and any password/permission/status edit immediately logs them out.
