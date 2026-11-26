---
layout: home

hero:
  name: ADTY AI
  text: Unified Multi-Protocol AI Gateway
  tagline: One API key and USD wallet for OpenAI, Anthropic, and Gemini models—engineered with dual-domain isolation, team collaboration, and live route observability.
  actions:
    - theme: brand
      text: Quick Start Guide
      link: /guides/overview
    - theme: alt
      text: Interactive Client Setup
      link: /integrations/cli/
    - theme: alt
      text: API Reference
      link: /api/

features:
  - title: Unified Multi-Protocol API
    details: Call OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, OpenAI Images, and native Gemini endpoints through one consistent gateway.
    link: /api/
    linkText: Explore endpoints
  - title: Dual-Domain Architecture
    details: Dedicated separation between the web console (https://www.adtyai.com) and the API gateway (https://api.adtyai.com) keeps traffic fast and cleanly isolated.
    link: /guides/overview
    linkText: Dual-domain guide
  - title: Interactive Client Setup
    details: Generate copy-ready configurations for Claude Code, Codex CLI, Kimi Code, OpenCode, Cherry Studio, and official SDKs in seconds.
    link: /integrations/cli/
    linkText: Setup guides
  - title: Teams & Shared Wallets
    details: Fund a centralized USD team wallet, provision independent team employee sub-accounts directly or via one-time invite links, issue team-scoped API keys, and audit per-request usage.
    link: /guides/teams
    linkText: Team collaboration
  - title: Live Channel Status
    details: Inspect 24-hour heartbeat bars, latency metrics, and success rates across concrete model groups before selecting a route for your API key.
    link: /guides/channel-status
    linkText: Channel availability
  - title: Secure Access & 2FA
    details: Protect accounts with TOTP two-factor authentication, offline recovery codes, step-up verification, session revocation, and scoped Personal Access Tokens.
    link: /guides/security
    linkText: Security controls
---

<div class="adty-home-wizard"><SetupWizard /></div>
