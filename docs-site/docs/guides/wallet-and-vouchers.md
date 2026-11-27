# Wallet, Bonus & Vouchers

ADTY AI uses a unified, USD-denominated prepaid wallet. You top up on demand, pay strictly for actual token or image consumption, and inspect every deduction in real time.

<PaymentMethodsList />

## Online Top-Ups

Open **Wallet** in the console at [https://www.adtyai.com](https://www.adtyai.com) to add funds:

1. Enter your target top-up amount in USD (or choose a preset package).
2. Select one of the active payment methods shown above.
3. Review the checkout summary—including the USD amount due and any automatic Recharge Bonus—and complete payment.
4. Once payment confirms, the USD credit posts to your wallet balance automatically.

### Pending Order Reuse & Unpaid Order Limit

To keep billing clean and prevent accidental duplicate payments:

- **Automatic pending order reuse**: If you already have an active unpaid order matching the same payment flow, the console can reuse that pending order instead of generating redundant checkout records.
- **Maximum 10 unpaid orders**: Each account can hold at most **10 unpaid orders** at a time. If you hit this limit, complete or cancel existing pending orders in your top-up history before creating a new one.

::: tip Payment Confirmation Timing
Card/wallet channels typically settle within seconds. On-chain USDT transfers credit automatically after the required blockchain confirmations on the selected network. Always send the exact displayed amount on the exact chosen network.
:::

## Recharge Bonus

When a site configures **Recharge Bonus by payment method**:

- The bonus percentage is **applied automatically** based on the payment method you choose—no promo code is required.
- Eligible bonus amounts are previewed right on the top-up panel before you confirm payment.
- Once the order completes, both the base top-up and the bonus credit are reflected in your wallet records.

Bonus availability and rates are tied to the active payment method and current site configuration, so always check the Wallet page preview prior to checkout.

## Quota Vouchers

If you received a prepaid **Quota Voucher** code from a promotion, partner, or team administrator:

1. Navigate to **Wallet** → **Redeem Voucher** in the console.
2. Paste the voucher code carefully (without extra leading or trailing spaces) and submit.
3. Valid codes immediately add their face value in USD to your wallet balance.

::: warning Invalid Attempt Protection
To protect voucher codes against automated guessing, submitting repeated invalid codes triggers rate-limit protection and a temporary redemption cooldown. Double-check characters (such as `0` vs `O` and `1` vs `I`) before retrying.
:::

## Transparent Pay-As-You-Go Usage Logs

Every API call billed against your personal or team wallet is recorded in **Usage Logs** with full itemization:

- **Timestamp & Request ID**: Exact time of execution and unique identifier for debugging or [Support Center](/guides/support) tickets.
- **API Key & Model Group**: Which key and concrete model group processed the request.
- **Model & Token Breakdown**: Input tokens, output tokens, cached input read/write tokens (when supported), reasoning tokens, or image generation count.
- **Exact USD Deduction & Latency**: Final cost computed from the group's published pricing multiplier, plus first-token / total response duration.

If you manage shared spending across multiple developers, see [Team Collaboration](/guides/teams) to centralize top-ups inside a shared team wallet.
