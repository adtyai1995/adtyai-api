<div align="center">

# ADTY AI

A self-hosted AI API gateway with a unified console for the main site, reseller subsites, teams, fund ledgers, and support operations.

<p>
  <a href="./README.zh_CN.md">简体中文</a> |
  <strong>English</strong>
</p>

<p>
  <a href="https://github.com/adtyai1995/adtyai-api">github.com/adtyai1995/adtyai-api</a><br>
  Image <code>ghcr.io/adtyai1995/adtyai-api</code><br>
  Creator <a href="https://github.com/adtyai1995">adtyai1995</a><br>
  License <a href="./LICENSE">GNU AGPL v3</a>
</p>

</div>

The product interface and repository documentation support **Simplified Chinese** and **English** only: this file, [README.zh_CN.md](./README.zh_CN.md), and [README.md](./README.md) (Simplified Chinese default).

---

## Overview

ADTY AI unifies multiple upstream AI model providers behind a single API gateway and provides one web console to manage account authentication, model pricing, multi-channel payments, zero-overdraft fund ledgers, reseller subsites, shared team balances, delegated subsite staff, ticket management, announcements, and transactional auditing.

- **Delivered product**: Web console and API service.
- **Database and cache**: **PostgreSQL 18.x** for both the primary database and any separate log database, paired with **Redis** for caching and rate limiting.
- **Accounting scale**: Fixed integer quota scale of **1 USD = 500,000 quota** to prevent floating-point drift.

| Item | Value |
| --- | --- |
| Repository | https://github.com/adtyai1995/adtyai-api |
| Container image | `ghcr.io/adtyai1995/adtyai-api` |
| Creator | [adtyai1995](https://github.com/adtyai1995) |
| Issues & Security | Repository Issues and Security Advisories |

Container images are tested and built for `linux/amd64` and `linux/arm64` by [`.github/workflows/adty-ghcr.yml`](./.github/workflows/adty-ghcr.yml) and published to `ghcr.io/adtyai1995/adtyai-api`. Production deployments must pin an immutable commit tag (for example `ghcr.io/adtyai1995/adtyai-api:sha-1a2b3c4`) rather than a floating tag. Backend and frontend share the build-time `VERSION` (`main-<short SHA>`, release tag, `<branch>-<short SHA>`, or `pr-<number>-<short SHA>`).

---

## Languages and Bilingual Operations

All UI screens, docs, order and ledger copy, forms, error messages, announcements, tickets, and fixed notice templates are delivered in Simplified Chinese and English only:

- **Simplified Chinese (`zh-CN`)**: Default for Chinese browser preferences; formal landing route `/zh-cn`.
- **English (`en`)**: Default for all other preferences; formal landing route `/` (`x-default`).
- **Explicit user choice wins**; fixed notice templates maintain both languages in the admin console.
- **Dynamic translation**: Root can configure a system translation job to translate dynamic Chinese notices into English. Failures keep the Chinese source and mark English as pending, never charging customers, teams, or site owners and never calling this gateway recursively.

---

## Roles and Authorization

| Role | Scope & Boundaries |
| --- | --- |
| **Root (Super Admin)** | Platform-wide settings, upstream channels and models, payment channels and scenes, opening/closing subsites, site levels and withdrawal review, main-site team creation, granular admin delegation, and full audit trails. |
| **Ordinary Admin** | Zero console permissions by default; Root enables or revokes individual `area · action` grants at any time. Unauthorized requests return `403`. |
| **Subsite Owner** | Dedicated management-only account created by Root when opening a site and permanently fixed (no replacement, transfer, or consumer/API access); manages that site's branding, domains, single retail multiplier, customers, site teams, site tickets, one-level invite commissions, earnings/margin wallets, and staff grants. |
| **Subsite Staff** | Dedicated staff account created by the site owner, scoped exclusively to `/site-staff`; support, finance, operations, and management roles are read-only by default, with explicit opt-in for sensitive actions. Any permission change, disablement, or deletion invalidates active sessions immediately. |
| **Team Leader** | Manages shared team balance, one-time member registration links, dedicated team employees, and team API keys; fixed and non-transferable. |
| **Customer** | Sign up, sign in, top up or redeem redemption codes, manage personal API keys, view model pricing and personal usage logs, and open support tickets. Non-team accounts never see team menus or team metadata. |

---

## Core Capabilities

### 1. Unified Multi-Model API Gateway & Billing Guardrails

- **Broad protocol support**: Compatible with OpenAI (Chat Completions / Responses), Claude, Gemini, Embeddings, Rerank, image, audio, Realtime, and asynchronous task workflows; JavaScript task plugins extend async task billing and polling.
- **Streaming keep-alive**: Sends keep-alive frames prior to the first token across streaming protocols to avoid proxy timeouts during long reasoning calls.
- **Zero-overdraft pre-debit & settlement**: Paid requests reserve estimated quota upfront and reject immediately if funds are insufficient without calling upstream. Settlement refunds unused reserves or books shortfalls on the originating account and logs without pausing subsequent paid calls when balance is available. Free models still record the paying account for downstream compliance accounting.
- **Channel status & rate limiting**: Built-in scheduled model/image probes and Uptime Kuma monitoring surface 24-hour availability and latency on Model Square (`/pricing`) and Channel Status (`/model-status`), showing no data rather than 100% when samples are absent. API keys bind to an explicit concrete group; per-user and per-key RPM, TPM, and concurrency limits fail closed when Redis counters are unavailable.
- **Privacy & Model Square**: Customer logs hide upstream channels, secrets, and internal ratios. Model Square (`/pricing`) presents each enabled model's active site selling price, billing mode, cached pricing, and usable groups.

### 2. Accounts, Step-Up Verification & Console Usability

- **First-party authentication**: Password login with optional email verification and TOTP (2FA). Repeated password failures lock sign-in uniformly; registration verification codes avoid mailbox enumeration; no Passkeys or third-party OAuth sign-in are included.
- **Step-up verification**: Sensitive financial writes, secret updates, and owner actions require single-use step-up proof (password, email code, 2FA, session confirmation, or Root exemption) bound to the exact action and target.
- **Copy-friendly UI**: Page headings, section titles, form labels, table headers, tabs, and navigation labels are selectable and copyable site-wide.

### 3. Multi-Channel Top-Up, Payment Scenes & Fund Ledgers

- **Versioned payment scenes**: Wallet options follow server-authorized scenes across three active scenes: user top-up (`wallet_topup`), owner margin online top-up (`owner_margin_topup`), and team top-up (`team_topup`, inheriting wallet rules by default with optional per-scene overrides).
- **Multi-channel Epay (Alipay / WeChat Pay)**: Epay channel management (gateway URL, merchant ID/key, display name, limits, priority, and live order test) is embedded directly at the top of **Billing & Payment → Payment Gateway → Epay**. Customers choose explicit per-channel buttons and are prompted to pick another channel on failure without silent failover.
- **BEpusdt USDT settlement**: Supports TRC20, BEP20, Polygon, and X Layer with an in-site checkout showing address, exact amount, and QR code, backed by signature verification and active polling recovery.
- **Method-based bonuses & redemption codes**: Bonuses follow per-method rate snapshots (subsites can configure site bonus rates directly for any payment method enabled on the main site without per-site Root authorization, backed by frozen earnings or authorized margin). Intentional customer quota grants use one-time redemption codes (full code shown once at creation), with optional expiring trial credits cleared automatically upon expiry.
- **Layered wallet & multi-step manual refunds**: Balances track principal, expiring bonuses, and permanent bonuses (spent in that exact order). Manual refunds run through a multi-step state machine in Order Management (submit, approve, reclaim principal, adjust bonuses, reverse commissions, record offline payout, complete — supporting partial refunds under `S + H + U + X + r <= L`) without paying cash for bonuses or driving balances negative, proportionally reversing main-site or subsite commissions while actual payouts happen offline.
- **End-to-end traceability**: Manual adjustments require a reason and idempotency key and prohibit non-Root operators from adjusting peers or higher roles. Orders, refunds, redemptions, adjustments, fund batches, and payment anomalies are auditable by permission.

### 4. Reseller Subsites

- **Structured site opening**: Root verifies readiness checks (reseller switch, at least one enabled site level with a positive supply discount, and platform root domain) at the top of Reseller Sites and opens sites through a dedicated modal that creates a standalone management-only owner account, with `Idempotency-Key` protection, auto/custom subdomains, and a post-open handoff summary. No separate site opening agreement is required.
- **Domains & branding**: Supports subdomains, verified custom domains, split web/API hostnames, optional Cloudflare for SaaS and AliDNS automation, and custom branding, logo, and notices.
- **Level-driven supply pricing**:
  - Supply discounts are determined strictly by **enabled site levels** (each level requires a positive supply discount and an optional owner margin target; legacy global default factors, global floors, and monthly-spend tiers are retired).
  - Automatic level upgrades and downgrades evaluate **cumulative customer net top-up principal (minus refunds)** only. Opening a site or switching back to automatic evaluation assigns the matching level and synchronizes its margin target immediately.
  - Supply price updates take effect immediately with owner notifications; the single site-wide retail multiplier (`1x–10x`) takes effect immediately; subsite team pricing is `supply price × subsite team multiplier` (bounded to `1.0x–10.0x`) and never drops below the site supply price.
- **Owner wallets & margin funding**: The Site Owner Center exposes earnings and margin wallets (keeping operating ledgers admin-only; owner accounts are management-only without self-use top-up or personal spending transfers). Available earnings can be withdrawn via USDT or transferred into the site margin wallet; margin can be funded by Root direct top-up, Root-issued margin recharge codes (`site_margin`), owner online top-up (`owner_margin_topup`), or available-earnings transfer.
- **Main-site & subsite referrals**: Main-site users earn referral commission on direct paid principal (stopping upon becoming a site owner). Subsite customers get a dedicated one-level invite link (`?site_invite=`), pre-first-order binding, and a subsite `/referral` console with earnings/margin coverage and owner-reviewed cash withdrawals; after site closure, main-site referral finance operators with the existing referral read/review/payout permissions (including Root) take over reviewing and paying remaining subsite invite commission withdrawals.
- **Site closure**: Initiated by Root; customers and active teams migrate to the main site while historical orders, refunds, tasks, and settlements keep their original financial ownership.

### 5. Team Management & Isolation

- **Standalone workspaces**: Root **Team management** (`/admin-teams`) provides cross-site team listings, read-only detail sheets (members, team keys, shared balance ledger), registration links, and fund anomalies; owners manage site teams at `/site-owner/teams`.
- **Team creation & pricing**: Teams created from the main-site admin console bind to the main site (`site_id = 0`) and bill at `main-site live group selling price × team discount`; subsite teams are managed per subsite and bill at `supply price × subsite team multiplier` (`1.0x–10.0x`, never below supply price).
- **Strict isolation**: Non-team accounts never see `/my-team` or team UI (`adty_has_team` gates navigation and route access). Existing ordinary accounts cannot join via invite codes; one-time registration links create dedicated team members. Team keys debit only shared team balance; active leaders can reset dedicated employee passwords (revoking old sessions immediately); closing a team preserves shared balance and read-only leader history.

### 6. Subsite Staff Workspace

- Site owners manage staff accounts, roles, permissions, password resets, and removal; sensitive grants (such as `customers.disable`) are off by default and require explicit opt-in.
- Staff cannot view full customer keys, reset customer passwords, impersonate customers, issue refunds, request customer balance adjustments, or touch owner wallets. Customer quota grants are issued solely via site redemption codes.

### 7. Ticket Management, Announcements & Telegram Notification Bots

- **Ticket management**: Dedicated platform queue (`/admin-tickets`), site-owner queue (`/site-owner/tickets`), staff workspace (`/site-staff`), and standalone customer support center (`/support`) share one ticket ledger with queue badges, multi-image batch upload (quota preflight, redaction reminders, private access, and crash-safe audited deletion), internal note isolation, and assignment workflows.
- **Announcements & notices**: Targeted to all users, all subsites, or specific subsites with server-stored versioned read receipts and actionable severe notices.
- **Telegram notification bots**: **System Settings → Operations → Telegram bots** manages the **User bot** and **Admin bot** in two independent cards (status badge, `@username`/ID, enable toggle, encrypted masked token, test delivery, copyable Webhook URL, and one-time secret) alongside the platform admin bot binding card. Bots send notifications only and never modify accounts, funds, or keys.

---

## Deployment

### Production

See [docs/adty/deploy.md](./docs/adty/deploy.md) for full production deployment, Nginx/TLS, Cloudflare, backup, and rollback procedures.

Use [`deploy/adty/docker-compose.yml`](./deploy/adty/docker-compose.yml) and pin an immutable image tag via `ADTY_IMAGE` in `.env` (upgrades and rollbacks change only that tag):

```text
ADTY_IMAGE=ghcr.io/adtyai1995/adtyai-api:sha-1a2b3c4
```

Verify the image digest, PostgreSQL 18, Redis, payment callback URLs, session secrets, and automated backups before going live. Never commit production secrets.

### Local Development Compose

The root [`docker-compose.yml`](./docker-compose.yml) is for local development only (not production). Set `POSTGRES_PASSWORD` and `REDIS_PASSWORD`, then run:

```bash
docker compose up -d --build
```

Open `http://localhost:3000`. It builds `adtyai-api:local` bound to loopback port `3000`, uses default project name `adtyai-api`, and defaults to `Asia/Shanghai` time. If upgrading an existing local stack with a different project name, pass `docker compose -p <previous-project>` to retain its PostgreSQL volume.

---

## Documentation

| Document | Contents |
| --- | --- |
| [docs/adty/features.md](./docs/adty/features.md) | Feature rules, console paths, database tables, and APIs |
| [docs/adty/deploy.md](./docs/adty/deploy.md) | Production deployment, image tags, upgrade, and rollback |
| [docs/adty/conventions.md](./docs/adty/conventions.md) | Engineering conventions and coding standards |
| [docs/authentication.md](./docs/authentication.md) | Sign-in sessions and authentication design |
| [docs/plugin-api/v1.md](./docs/plugin-api/v1.md) | Task plugin v1 API reference |

---

## Explicitly Not Included

- Public platform status page
- Standalone usage-analytics board in the user center
- Model official list price comparisons and historical price snapshot backfill
- Check-in rewards, subscription plans, promo codes, and amount-tier bonuses
- Passkeys and third-party OAuth sign-in (GitHub, Discord, WeChat, OIDC, Telegram login, etc.)
- Model hosting/hardware deployment management or desktop client releases
- Telegram bot commands that alter accounts, funds, payments, or API keys
- Subsite opening agreements and site owner handover

---

## Usage Boundaries & License

This project is intended for authorized AI API gateways, organizational authentication, multi-model routing, usage metering, cost accounting, and private deployments. Callers must obtain upstream credentials lawfully and comply with upstream terms and applicable laws; operators offering public services are responsible for local regulatory filings, content safety, identity verification, log retention, tax, and payment compliance.

Released under [GNU AGPL v3](./LICENSE). Please also review [NOTICE](./NOTICE) and [THIRD-PARTY-LICENSES.md](./THIRD-PARTY-LICENSES.md). Source file license headers are retained as required by the license and do not represent the creator list of ADTY AI; the Go module path `github.com/QuantumNous/new-api` is kept solely for Go compilation and imports. The sole official repository for ADTY AI is https://github.com/adtyai1995/adtyai-api and the sole creator is [adtyai1995](https://github.com/adtyai1995).
