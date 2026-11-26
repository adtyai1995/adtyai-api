# Team Collaboration

ADTY AI Teams let organizations, labs, and engineering groups share a single USD wallet while preserving per-member key isolation, role boundaries, and full usage auditing.

## Team Shared Wallet

Instead of reimbursing individual developers or sharing one personal `sk-...` key across an entire company:

- The **Team Owner** funds the **Team Shared Wallet** inside the team workspace.
- Team members issue their own **team-bound API keys** that deduct directly from the shared team balance.
- Personal wallets and personal API keys remain strictly separated from team billing.

::: tip Zero Key Sharing
Every engineer should use their own team-bound key rather than copying a teammate's key. Separate keys make cost attribution effortless and let owners revoke access for one person without breaking anyone else's workflow.
:::

## Role Model

ADTY AI supports three distinct identities inside a team workspace:

| Identity | Who It Is For | Wallet & Billing | Lifecycle Control |
| :--- | :--- | :--- | :--- |
| **Team Owner** | Organization lead or billing admin | Funds and manages the shared team wallet | Controls team membership, sub-accounts, keys, and workspace policies |
| **Team Member** | Existing platform user invited to the team | Team-bound keys bill the shared team wallet; personal keys remain separate | Owner can adjust access or remove the member from the team at any time |
| **Standalone Team Staff Sub-Account** | Dedicated staff account provisioned directly by the team | Operates under team workspace billing without needing external personal registration | Provisioned, disabled, or revoked directly by the team owner |

### When to Choose Team Members vs. Staff Sub-Accounts

- **Invite as Team Member** when collaborators already have their own ADTY AI accounts and want to switch cleanly between personal and team workspaces.
- **Create a Standalone Team Staff Sub-Account** when onboarding employees or contractors under centralized company-owned credentials whose login lifecycle should begin and end entirely inside your team.

## Creating Team-Bound API Keys

1. Switch to your team workspace in the console at [https://www.adtyai.com](https://www.adtyai.com) (or select the team billing scope when creating a key).
2. Choose a concrete model group matching the models your project requires (see [Channel Availability](/guides/channel-status)).
3. Configure optional safeguards: a per-key USD quota cap, model allowlist, IP allowlist, and expiration date.
4. Distribute or configure the generated `sk-...` key in the member's CLI, IDE, or service.

All requests authenticated with that key automatically deduct from the **Team Shared Wallet** and appear under the team's usage ledger.

## Usage Visibility & Cost Auditing

- **Team Owners** get workspace-wide visibility across every team member, staff sub-account, and team-bound API key—making it easy to spot unexpected spikes, review model mix, and reconcile internal budgets.
- **Team Members** can inspect usage logs for their own team keys to debug requests, verify prompt caching, and obtain Request IDs without exposing other members' traffic.

## Immediate Revocation on Member Removal

Offboarding is instant and enforced at the gateway level:

- Removing a **Team Member** from the team or disabling a **Standalone Team Staff Sub-Account** immediately blocks all team-bound API keys associated with that user from executing further model requests.
- Active team keys belonging to other members remain unaffected.
- Historical usage records are preserved in the team ledger for accounting and compliance.
