# Homepage Startup Implementation Plan

> Execute inline with regression tests before each behavior change. User authorized the three analyzed optimizations and a local commit.

**Goal:** Remove unused landing-page requests, reduce startup dependencies, and serve branded subsite content before JavaScript.

**Architecture:** Keep React/Rsbuild and the existing Go HTML injection. Reduce landing data to the referral banner. Resolve initial home language before loading its pack. Embed current server setup completion on home documents so completed installations need no extra setup round trip; keep the existing check as fallback. Generate neutral subsite templates alongside main-site prerenders, then substitute escaped host-resolved brand data.

**Constraints:** Keep current home-language rules, authenticated-route checks, tenant isolation, HTML no-store behavior and custom-home behavior. No CDN changes. Work in the current checkout; commit only this task's explicit paths, preserving existing changes.

## Tasks

- [x] Add a landing request regression; remove unused pricing hooks/computation and unused landing data fields. Verify visible referral and API examples remain.
- [x] Test initial language selection for anonymous root, explicit language paths, remembered choice, session hint, other pages and blocked storage. Select before loading the pack.
- [x] Test server setup hint and frontend fallback. Skip setup HTTP only on a completed, server-rendered home; leave installation and non-home routes checked.
- [x] Generate subsite templates using the same LandingSections, with explicit brand/API placeholders. Test both languages, logo absent/present, escaped names/URLs, independent sites, fallback when templates are missing, and absence of main-site branding.
- [x] Run affected frontend suites, typecheck, lint/format, production build, i18n and browser smoke; run focused Go tests. Review the exact diff and commit only task files locally.

## Review focus

- A stale session hint must not grant authentication or alter protected-route checks.
- Missing server bootstrap metadata must retain the existing setup request.
- Templates must not substitute user data recursively or leak main-site assets into subsites.
- Main-site custom home content must retain its existing behavior.
- Language bootstrap must tolerate denied browser storage and retry failed chunks.

## Progress

- Initial audit confirmed the target production files are clean; unrelated financial changes and locale edits already exist.
- Implemented all four code tasks above. Removed unused price query dependencies and the unreferenced landing-price calculation module.
- Initial language regression went from four failures to passing; setup hint and site-template regressions were also observed failing before implementation.
- Refined setup hint to a persisted-completion snapshot at handler initialization (Root, setup record, both mode options), avoiding the mutable global flag and half-installed state. A new installation retains the API fallback until restart.
- Validation: 89 frontend tests in 13 affected suites passed; relocated setup suite passed again (5 tests). Bun 1.4.4 typecheck passed. Targeted formatting passed; lint has only the existing prerender script console-output warning. i18n check passed for 2106 keys in two languages. Production build and Chromium homepage smoke passed.
- Go homepage/SEO and subsite tests passed, including custom home and missing-template fallback. Database-backed tests used PostgreSQL server_version_num=180007 in isolated test schemas.
- Independent scoped review completed with no findings. Commit includes only this task's explicit files; no CDN or deployment changes.
