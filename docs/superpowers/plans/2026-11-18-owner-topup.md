# Owner Self-Use Top-Up Implementation Plan

**Goal:** Add online payment as a source of the owner's existing site principal.
**Architecture:** Reuse versioned payment scenes, both payment providers, existing immutable orders, fund batches and refund state machine. Scope comes from the trusted request site and a locked SQL owner check.
**Tech Stack:** Go, GORM, PostgreSQL, React, TypeScript.
The workspace master plan records the approved business rules and acceptance results in [DEC-036](../../../../ADTY_AI_统一执行总方案.md#owner-self-use).

## Constraints

- Preserve existing uncommitted changes; no commit or deployment requested.
- Fixed scale 500000 quota/USD; ratio 1, no gifts or commissions.
- Root configures dedicated `owner_self_use_topup` scenes; no default fallback.
- Restrict cash refund to unconsumed, unreserved original principal.

## Tasks

- [x] Cover scene configuration, quotes, fixed owner authorization, duplicate credit and the 100 to 99.20 balance on PostgreSQL.
- [x] Persist the order business, validate the scene and share the owner quote across both payment providers.
- [x] Reserve and release refunds against the original batch. Report owner deposits, vouchers and refunds in settlement.
- [x] Reuse wallet checkout components in the owner panel and support Root configuration in both interface languages.
- [x] Run the affected PostgreSQL, frontend, build and browser checks and record the results in DEC-036.

## Review Focus

Wrong host/owner/customer/team identities; ordinary scene bypass; configuration changes after quote; callback replay and closed site late payment; approved refund concurrent consumption.

## Progress

On 2026-11-19 the core, financial race and compatibility suites passed with 57, 47 and 52 passing test records respectively, including subtests. Both provider routes also passed the final owner-specific race run after tightening the face-value check. The 138 affected frontend tests, typecheck, scoped lint, translations, production builds and Chromium owner workflow passed. The master plan lists the remaining repository-wide frontend failures and the scope of the synthetic gateway checks.
