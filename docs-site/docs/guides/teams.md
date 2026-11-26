# Team Collaboration

ADTY AI Teams let organizations, labs, and engineering groups share a single USD wallet while preserving per-member key isolation, role boundaries, and full usage auditing.

## Team Shared Wallet

Instead of reimbursing individual developers or sharing one personal `sk-...` key across an entire company:

- The **Team Owner** funds the **Team Shared Wallet** inside the team workspace.
- Team-bound API keys issued for the owner or independent team employees deduct directly from the shared team balance.
- The Team Owner's personal wallet and personal API keys remain strictly separated from team billing.

::: tip Zero Key Sharing
Every engineer should use their own team-bound key rather than copying a teammate's key. Separate keys make cost attribution effortless and let owners revoke access for one person without breaking anyone else's workflow.
:::

## Role Model

ADTY AI supports two distinct roles inside a team workspace:

| Role | Who It Is For | Wallet & Billing | Lifecycle Control |
| :--- | :--- | :--- | :--- |
| **Team Owner (Owner)** | Organization lead or billing admin | Funds and manages the shared team wallet; personal wallet and personal keys remain separate | Creates and manages employee sub-accounts, issues one-time invite links, manages team keys, and audits all team usage |
| **Independent Team Employee** | Dedicated employee sub-account belonging exclusively to the team | Operates solely within the team workspace and bills the shared team wallet (no personal wallet or personal workspace switching) | Created directly by the owner or registered via a one-time invite link; enabled, disabled, reset, or removed directly by the owner |

### Onboarding Team Employees

Existing personal accounts are not merged into or switched across team workspaces. Instead, every team employee uses a dedicated **Independent Team Employee** sub-account provisioned in one of two ways:

- **Direct creation by the Team Owner**: Provision the independent team employee sub-account directly inside the team console when onboarding employees or contractors under centralized company-owned credentials.
- **One-time invitation link**: Issue a one-time invitation link so the employee can register their own independent team employee sub-account directly under your team.

## Creating Team-Bound API Keys

1. Open your team workspace in the console at [https://www.adtyai.com](https://www.adtyai.com) (Team Owners can switch to the team workspace or select the team billing scope when creating a key; Independent Team Employees sign in directly to the team workspace).
2. Choose a concrete model group matching the models your project requires (see [Channel Availability](/guides/channel-status)).
3. Configure optional safeguards: a per-key USD quota cap, model allowlist, IP allowlist, and expiration date.
4. Distribute or configure the generated `sk-...` key in the employee's CLI, IDE, or service.

All requests authenticated with that key automatically deduct from the **Team Shared Wallet** and appear under the team's usage ledger.

## Usage Visibility & Cost Auditing

- **Team Owners** get workspace-wide visibility across every independent team employee and team-bound API key—making it easy to spot unexpected spikes, review model mix, and reconcile internal budgets.
- **Independent Team Employees** can inspect usage logs for their own team keys to debug requests, verify prompt caching, and obtain Request IDs without exposing other employees' traffic.

## Immediate Revocation on Member Removal

Offboarding is instant and enforced at the gateway level:

- Disabling or removing an **Independent Team Employee** sub-account immediately blocks all team-bound API keys associated with that account from executing further model requests.
- Active team keys belonging to other team employees remain unaffected.
- Historical usage records are preserved in the team ledger for accounting and compliance.
