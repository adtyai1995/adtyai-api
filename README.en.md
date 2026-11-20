<div align="center">

# ADTY AI

A self-hosted AI API gateway, with a console for the main site, subsites, teams, and funds.

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

The product supports Simplified Chinese and English only. The repository introductions are this file and [简体中文](./README.zh_CN.md). GitHub shows [README.md](./README.md), which is the Chinese introduction.

## What it is

ADTY AI exposes one API in front of many model providers, and uses the same console to decide who can call, what price applies, and where the money is booked. A customer receives an API key and calls OpenAI-, Claude-, and Gemini-compatible endpoints. The platform debits the wallet from actual usage. Root can open a subsite with one fixed owner, its own brand, and its own selling price. A team can share one balance. Staff can do only what that site's owner has granted.

The delivered product is the web console and the API service. The formal business database is PostgreSQL. The cache is Redis. Current CI and the production compose example use PostgreSQL 18.

## Repository, image, and creator

| Item | Value |
| --- | --- |
| Repository | https://github.com/adtyai1995/adtyai-api |
| Container image | `ghcr.io/adtyai1995/adtyai-api` |
| Creator | [adtyai1995](https://github.com/adtyai1995) |
| Issues and security advisories | This repository |

Images are built by [`.github/workflows/adty-ghcr.yml`](./.github/workflows/adty-ghcr.yml) and published to `ghcr.io/<owner>/<name>`. The formal repository is `adtyai1995/adtyai-api`, so the formal image is `ghcr.io/adtyai1995/adtyai-api`.

Production uses a commit tag, for example `ghcr.io/adtyai1995/adtyai-api:sha-1a2b3c4`. Do not treat a floating tag as a production release. Merge-branch checks use `ghcr.io/adtyai1995/adtyai-api:merge-<tag>`. The local compose file builds `adtyai-api:local`. That is not the production image.

This product does not use another code host or an older container registry as its image source. A separately configured plugin-package address, if present, serves plugins only. It is not this product's repository.

The creator is adtyai1995 only. Do not list anyone else as a co-creator of ADTY AI.

## Supported languages

The interface, docs, order and fund copy, forms, errors, system notices, announcements, support, and fixed notice templates are Simplified Chinese and English only.

- Simplified Chinese: `zh-CN`. Used when the browser preference is Chinese.
- English: `en`. Used for every other browser preference.
- A manual choice wins.
- Fixed notices are Chinese, then English.
- Root can configure a translation task that turns approved dynamic Chinese into English. Failure keeps the Chinese source and says English is not ready. Translation does not debit a customer, team, or site owner, and it does not call this gateway recursively.

Traditional Chinese, French, Japanese, Korean, Russian, and Vietnamese packs and introductions have been removed. They are not in the language selector and they are not supported languages.

## Who uses it

- **Customer:** register, sign in, top up, create API keys, read their own usage, and open tickets.
- **Team leader:** manage the shared balance, members, and team keys. The leader cannot be replaced.
- **Subsite owner:** manage that site's brand, prices, customers, earnings, margin, and staff. Root assigns the owner. The owner cannot be replaced.
- **Subsite staff:** sign in to a separate workspace and do only what the current grant allows.
- **Root:** create sites and configure payments, channels, models, and platform rules.
- **Ordinary administrator:** no console permission until Root grants each area and action.

## Calling models

Upstream channels are exposed through one gateway. Channels, models, groups, and prices are configured in the console. A group has a stable ID. Its formal display name is Chinese and English only.

Customers and compatible clients can use chat completions, Claude, Gemini, embeddings, rerank, images, audio, asynchronous tasks, and Realtime. Streaming can send a keep-alive before the first token so a long reasoning call is not cut off by a proxy. JavaScript task plugins extend asynchronous task billing and polling.

Every paid request reserves an estimate first. If the balance is short, the request is refused and is not sent upstream. The wallet does not go negative. Settlement refunds the unused reserve and records any shortfall on that same account. A shortfall pauses new paid requests for that account. A free model still stores the paying account, so a later violation fee follows the original account.

A customer's own logs hide the channel, upstream secrets, and internal ratios. Administrator logs can still show the channel details needed to investigate.

When creating or editing an API key, the console shows that group's 24-hour request success rate and its active-probe availability. No sample, or a stopped collector, shows no data. It does not show 100%. Health data does not change a key's group and does not reveal upstream channel secrets.

Per-user limits can cap requests per minute, tokens per minute, and concurrency. API keys can have their own limits. If Redis is enabled and the counter is unavailable, the request is refused.

## Accounts and keys

The sign-in credential is the account password. Email verification, Passkeys, and TOTP can be turned on. Sensitive actions follow the current step-up policy. There is no GitHub, Discord, WeChat, OIDC, or other third-party sign-in. Telegram binds a notification target. It cannot sign in, reset a password, or operate an account.

After too many bad passwords, the same error is returned for a lock window and the password is not checked again. A registration code is not sent to an address that is already registered, so the endpoint cannot be used to enumerate mailboxes.

An API key belongs to a person or to a team. A team-bound key spends only the shared team balance. After the grant is revoked, the old key cannot be switched onto the personal wallet.

## Top-up and payment

The wallet shows only the payment scenes the server currently allows. Ordinary top-up uses the `wallet_topup` scene. An owner's self-use top-up uses the separate `owner_self_use_topup` scene. Without that scene, the owner cannot place that order, and the product does not fall back to ordinary top-up.

Connected payment methods:

- Alipay and WeChat Pay, through multiple Epay channels. Each enabled channel and method is its own button, named by Root. If the chosen channel cannot take the order, the customer is told to pick another. The server does not switch channels for them.
- USDT, through BEpusdt, on TRC20, BEP20, Polygon, and X Layer. Only chains enabled in the console are shown. The in-site payment window shows the address, the amount due, and a QR code.

Each order freezes the scene, channel, display name, amount, and gateway facts from the moment it was created. A late, verified success notification can still credit the order. A success notification for an order that was already refunded goes to manual review and is not credited again.

Root sets a gift rate per payment method. A subsite must be authorized for that method before its owner can set a local rate inside the grant. A gift requires real coverage: site earnings are frozen first, and margin is used only when Root has authorized that fill. Amount-tier gifts are retired. Promo codes are removed. Quotes and orders do not accept them.

Quota is added on purpose only through a recharge voucher. The full code is returned once, at creation. Redeeming it does not add a gift and does not pay invite commission.

Refunds are started from recharge audit. The system claws back only the unspent principal of that order. The gift is recovered and is not paid out as cash. The balance is not driven negative. The amount to pay back is shown in the console. The actual transfer is completed offline.

## Funds

Internal booking uses an integer quota. **1 USD = 500,000 quota.** The scale is fixed. A wallet separates principal, expiring gifts, and permanent gifts. Spend principal first, then the earliest expiring gift, then permanent gifts. Refunds return along the original allocation.

Root can grant a customer a trial gift with an expiry date. After expiry, the primary node clears the unused remainder. Amounts already spent are not cleared again. A trial gift is not a top-up, pays no commission, and cannot be withdrawn.

A manual balance change requires a reason and an idempotency key. Increases and decreases keep a traceable funding record. A non-root operator cannot adjust a peer or a higher role. A decrease cannot take the live balance below zero.

Ledgers, orders, refunds, redemptions, adjustments, and payment exceptions can be traced by permission. A site owner can see that site's orders, not a platform-wide top-up total, and not the operating-wallet page. The operating wallet remains booked in the admin ledger.

## Main-site referral

A main-site user receives an invite link at registration. Commission is calculated on the top-up face amount, not on the gift. A refund claws commission back in proportion to the principal returned. Opening a subsite stops that account's main-site invite commission. Subsite customers do not use this main-site program.

## Subsites

Only Root can create a subsite, and only by assigning one owner from an existing main-site user. The owner cannot be replaced. There is no handover. Users cannot apply to open a site themselves.

A site is reached by its subdomain or by a verified custom domain. The first screen can use that site's name and logo. The website and the model API can use different hosts.

The selling price is the supply multiplier times the owner's markup multiplier. A price cut takes effect immediately. A price increase waits, and the old price remains during the wait. A subsite team price cannot fall below the supply price.

The owner has an earnings wallet and a margin wallet. The spread on customer usage is booked as earnings. After the hold, it can be withdrawn or moved into that owner's on-site spending principal. The owner can also top up that spending principal online when Root has opened the dedicated payment scene. That top-up adds no gift, no invite commission, and no new owner earnings.

A subsite customer's one-level invite commission is a separate hold. Cash withdrawal is reviewed only by that site's owner.

Closing a site is started by Root. Customers and surviving teams move to the main site. Historical orders, tasks, refunds, and settlements keep the financial owner frozen on the original operation.

## Teams

Only Root creates a team. A team has one leader, and the leader cannot be replaced. If the leader leaves or closes the team, the team closes, the shared balance remains, and team keys stop.

A signed-in ordinary account cannot join with an invite code. A one-time link is only for registering a new member. A key bound to the team spends only the shared balance. Team top-up is booked to that shared balance and does not use personal gift rules. On a subsite, the team price is the higher of the supply price and the site price times the team pay ratio.

## Subsite staff

The owner can create a staff account, set the name, role, and permissions, and disable, reset, or delete it. Support, finance, operations, and management roles are read-only by default. Customer-safety actions must be granted separately.

Staff enter their workspace with the normal sign-in. Every request checks membership, the site agreement, and the authorization version. Changing permissions, disabling, or deleting invalidates old sessions. Staff cannot read a full customer key, sign in as the customer, or directly refund, withdraw, or move the owner's funds. A balance change is a request. It names the reason, the original order, and the funding source. The owner or Root approves it, and the wallets move in one transaction.

## Support, notices, and announcements

Customers can open a ticket, reply, and upload images. Attachments are validated images only. Internal support notes are not returned on customer APIs. A ticket can cite an order number. It does not carry a full key.

Root can publish platform announcements, system notices, maintenance notices, and operating-rule changes to all users, all subsites, or one subsite. A site owner manages only that site's customer announcements. Read receipts for a signed-in account are stored on the server. Editing an announcement creates a new unread version. A severe notice aimed at a specific person can create a to-do.

There are exactly two Telegram bots: one for user notices and one for admin notices. Tokens are saved by Root only, encrypted, and masked. The bots send notices. They do not expose commands that change balances, refunds, or keys.

## Administration, audit, and deployment

Root manages users, models, channels, groups, payment channels, sites, finance, and system settings. An ordinary administrator sees sidebar entries and APIs only for granted permissions. A missing permission returns 403.

Important writes are audited. Fund and permission changes commit with the audit in the same transaction. If the separate log database is down, the event identity is kept and retried. An unaudited change is not reported as success.

Production deployment is in [docs/adty/deploy.md](./docs/adty/deploy.md). The compose file is `deploy/adty/docker-compose.yml`. The image tag is `ADTY_IMAGE` in `.env`. Upgrade and rollback change that tag only. Example:

```text
ADTY_IMAGE=ghcr.io/adtyai1995/adtyai-api:sha-1a2b3c4
```

Before going live, check the image digest, PostgreSQL 18, Redis, payment callback addresses, the session secret, and backups. Do not commit production secrets.

The root compose file is not the production deployment. From the repository root, set `POSTGRES_PASSWORD` and `REDIS_PASSWORD`, then run:

```bash
docker compose up -d --build
```

Open `http://127.0.0.1:3000`. That compose file builds `adtyai-api:local` and publishes port 3000 on the local interface only.

## Documentation

| Document | Contents |
| --- | --- |
| [docs/adty/features.md](./docs/adty/features.md) | Rules, console entries, tables, and APIs for each feature |
| [docs/adty/deploy.md](./docs/adty/deploy.md) | Production deploy, image tags, upgrade, and rollback |
| [docs/adty/conventions.md](./docs/adty/conventions.md) | Engineering conventions |
| [docs/authentication.md](./docs/authentication.md) | Sign-in sessions |
| [docs/plugin-api/v1.md](./docs/plugin-api/v1.md) | Task plugin API |

In-console integration docs cover Chinese and English. The price page shows the current site's selling price. Official list prices are synced in the console. This file does not freeze those numbers.

## Not included

- A public platform status page
- A separate usage-analytics board in the user center
- Check-in rewards, subscription plans, and promo codes
- Third-party account sign-in
- Model deployment and hardware instance management
- A desktop client release
- Telegram commands that operate accounts, payments, or keys
- Owner handover
- Amount-tier gifts

## Use

This project is for an authorized AI API gateway, organization authentication, multi-model management, usage records, cost accounting, and private deployment. Callers must obtain upstream keys and interface rights lawfully and follow upstream terms and the laws that apply to them. Operators who offer generative services to the public complete the filing, licensing, content-safety, identity, log-retention, tax, and payment duties required where they operate.

## License

This repository is released under the [GNU AGPL v3](./LICENSE). Also read [NOTICE](./NOTICE) and [THIRD-PARTY-LICENSES.md](./THIRD-PARTY-LICENSES.md). Copyright headers in source files stay because the license requires them. They are not the creator list of ADTY AI.

The Go module path remains `github.com/QuantumNous/new-api`. That path is for compilation and imports. It is not this product's repository address, and it is not the creator. The product repository is only https://github.com/adtyai1995/adtyai-api.
