# Team Collaboration

ADTY AI Teams let organizations, labs, and engineering groups share a single USD wallet while preserving per-member key isolation, role boundaries, and shared balance ledger auditing.

## Team Shared Wallet

Instead of reimbursing individual developers or sharing one personal `sk-...` key across an entire company:

- The **Team Owner** funds the **Team Shared Wallet** inside the team workspace.
- Team-bound API keys issued for the owner or independent team employees deduct directly from the shared team balance.
- The Team Owner's personal wallet and personal API keys remain strictly separated from team billing.

::: tip Zero Key Sharing
Every engineer should create and bind their own team key rather than copying a teammate's key. Separate keys keep access isolated and let owners revoke one person's key without breaking anyone else's workflow.
:::

## Role Model

ADTY AI supports two distinct roles inside a team workspace:

| Role | Who It Is For | Wallet & Billing | Lifecycle Control |
| :--- | :--- | :--- | :--- |
| **Team Owner (Owner)** | Organization lead or billing admin | Funds and manages the shared team wallet; personal wallet and personal keys remain separate | Creates and manages employee sub-accounts, issues one-time invite links, views/revokes team-bound keys, and audits team top-ups and shared balance ledger |
| **Independent Team Employee** | Dedicated employee sub-account belonging exclusively to the team | Operates solely within the team workspace and bills the shared team wallet (no personal wallet or personal workspace switching) | Created directly by the owner or registered via a one-time invite link; enabled, disabled, reset, or removed directly by the owner |

### Onboarding Team Employees

Existing personal accounts are not merged into or switched across team workspaces. Instead, every team employee uses a dedicated **Independent Team Employee** sub-account provisioned in one of two ways:

- **Direct creation by the Team Owner**: Provision the independent team employee sub-account directly inside the team console when onboarding employees or contractors under centralized company-owned credentials.
- **One-time invitation link**: Issue a one-time invitation link so the employee can register their own independent team employee sub-account directly under your team.

## Creating & Binding Team API Keys

Each active team member (either the **Team Owner** or an **Independent Team Employee**) creates and binds their own API keys—owners cannot bind keys on behalf of employees:

1. Sign in to the console at [https://www.adtyai.com](https://www.adtyai.com) and open **API Keys (`/keys`)** to create a personal `sk-...` API key.
2. Choose a concrete model group matching the models your project requires (see [Channel Availability](/guides/channel-status)), and configure optional safeguards such as a per-key USD quota cap, model allowlist, IP allowlist, and expiration date.
3. Open **My Team (`/my-team`)**, enter the **Token ID** of your own key, and click **Bind key** to bind it to the team.
4. Configure the bound `sk-...` key in your CLI, IDE, or service.

Once bound, all requests authenticated with that key automatically deduct from the **Team Shared Wallet** and are recorded in the team's shared balance ledger.

## Usage Visibility & Cost Auditing

- **Team Owners** can view the team's **shared wallet balance**, **team top-up history**, **shared balance ledger**, **member list**, and the status of all **team-bound API keys** (with immediate revocation controls) on **My Team (`/my-team`)**. The platform does not expose cross-member request log details or cross-member model breakdown reports; each member (including the owner for their own requests) inspects their own request logs on **Usage Logs (`/usage-logs`)**.
- **Independent Team Employees** can view and manage their own bound team keys on **My Team (`/my-team`)** and inspect their own request logs (token breakdown, prompt caching, and Request IDs) on **Usage Logs (`/usage-logs`)** without exposing other employees' keys or traffic.

## Immediate Revocation on Member Removal

Offboarding is instant and enforced at the gateway level:

- Disabling or removing an **Independent Team Employee** sub-account immediately blocks all team-bound API keys associated with that account from executing further model requests.
- Active team keys belonging to other team employees remain unaffected.
- Historical shared balance ledger entries are preserved for accounting and compliance.
