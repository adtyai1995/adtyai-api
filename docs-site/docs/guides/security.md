# Account Security & PAT

Manage authentication hardening, active login sessions, and programmatic management tokens from the security center at [https://www.adtyai.com/security](https://www.adtyai.com/security).

## Two-Factor Authentication (2FA)

Protecting your console account with TOTP 2FA ensures that a leaked password alone cannot be used to view API keys, alter security settings, or access wallet features.

### Enabling TOTP 2FA

1. Open [https://www.adtyai.com/security](https://www.adtyai.com/security).
2. Click **Enable 2FA** and scan the QR code with any standard TOTP authenticator app (such as 1Password, Bitwarden, Google Authenticator, or Authy).
3. Enter the 6-digit rotating code to verify pairing.
4. **Save your offline recovery codes immediately** in a password manager or secure offline location.

::: warning Offline Recovery Codes
Each recovery code is single-use and lets you regain access if you lose your authenticator device. Regenerating recovery codes immediately invalidates all previously issued codes. Never store recovery codes in a public repository or unencrypted chat.
:::

## Step-Up Verification for Sensitive Actions

Even while you are already signed in to the console, high-impact operations require a fresh **step-up verification** challenge (via your TOTP code or account verification factor):

- Revealing the full plaintext value of an existing API key
- Changing your account password
- Modifying or disabling 2FA settings
- Submitting external referral cash withdrawals

This short-lived verification window protects your account if a browser session is temporarily left unlocked.

## Active Session Review & Revocation

Under **Active Sessions** on [https://www.adtyai.com/security](https://www.adtyai.com/security), you can audit every signed-in browser session associated with your account:

- Review login IP address, approximate client/device info, creation time, and last activity timestamp.
- **Revoke individual sessions** you no longer recognize or used on a shared workstation.
- **Sign out all other sessions** immediately after changing your password or rotating compromised credentials.

## Personal Access Tokens (PAT)

**Personal Access Tokens** allow scripts, dashboards, or automation tools (such as usage/balance query utilities) to call console management APIs without using your interactive login password or cookie.

- **Distinct from model API keys**: PATs manage account-level actions and read metadata; they are **not** used to call `/v1/chat/completions` or `/v1/messages`. Model inference always uses `sk-...` API keys.
- **Scoped permissions**: Grant each PAT only the minimum permission scopes required for its automation job (for example, read-only usage/balance scopes instead of full management scopes).
- **Expiration & revocation**: Assign an expiration date when creating a PAT and revoke unused tokens immediately from the Security page.

## API Key Best Practices

Follow these practices whenever issuing `sk-...` keys for local tools, CI pipelines, or production services:

- **One key per project or environment**: Separate development, staging, production, and personal CLI tools so one leak never requires rotating every client at once.
- **Set USD quota caps & expiration**: Apply a spending limit and optional expiry date on every key used by automated agents or untrusted environments.
- **Restrict allowed models & IPs**: When a server or workflow only needs specific models or runs from static egress IPs, lock the key down to those exact models and IP addresses/CIDRs.
- **Never commit keys to source control**: Load keys via environment variables, `.env` files listed in `.gitignore`, or a secret manager.
- **Rotate on suspicion**: If an API key is exposed in logs, screenshots, or Git history, disable or delete it immediately in the console and issue a replacement.
