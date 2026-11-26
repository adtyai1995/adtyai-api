/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import type { WalletLotProjection } from '@/features/wallet/types'

/** Server-paged list (common.PageInfo). */
export type OwnerPage<T> = {
  page: number
  page_size: number
  total: number
  items: T[] | null
}

export type OwnerSiteStatus = 'active' | 'paused' | 'closing'

/** Brand fields the owner may edit; rendered as plain text everywhere. */
export type SiteBrand = {
  name: string
  logo_url: string
  title?: string
  favicon_url?: string
  notice: string
  support_contact: string
  tawk_property_id?: string
  tawk_widget_id?: string
  wallet_notice?: string
  voucher_purchase_url?: string
  docs_url?: string
}

/** One site price row (model.AdtySitePrice). Decimals are strings. */
export type SitePrice = {
  id: number
  site_id: number
  user_id: number
  group: string
  model?: string
  markup: string
  pending_markup: string
  pending_at: number
  version: number
}

/**
 * What a custom domain is for. `site` serves web pages, sign-in and console
 * (rejecting model API calls); `base_url` serves model API calls and also
 * allows opening the site homepage and console.
 */
export type SiteDomainRole = 'site' | 'base_url'

/** A custom domain (model.AdtySiteDomain, task 11.8). */
export type SiteDomain = {
  managed_by?: 'owner' | 'platform'
  automation_state?: string
  desired_state?: string
  id: number
  hostname: string
  role: SiteDomainRole
  status: 'pending' | 'active' | 'failed' | 'removed'
  ssl_status: string
  verify_records: string
  last_error: string
  edge_acme_alias?: string
  edge_cert_status?: string
  edge_cert_error?: string
  edge_cert_not_after?: number
  created_at: number
}

/** One payment method the owner may put a gift on (service.AdtySiteBonusMethod). */
export type OwnerBonusMethod = {
  method: string
  enabled: boolean
  /** Decimal fraction, not a percent and not an amount tier: 0.025 pays 2.5%. */
  rate: string
}

/** Per-site multilingual legal documents (en, zh-CN) + refund policy switch. */
export type SiteLegalDocs = {
  user_agreement: Record<string, string>
  privacy_policy: Record<string, string>
  refund_policy: Record<string, string>
  refund_policy_enabled: boolean
}

export type SiteMailSaveProvider = 'smtp' | 'resend'
export type SiteMailProvider = '' | SiteMailSaveProvider

/** GET /api/adty/site/mail-config — secrets are never returned in plaintext. */
export type SiteMailConfig = {
  provider: SiteMailProvider
  sender_name: string
  smtp_server: string
  smtp_port: number
  smtp_account: string
  smtp_from: string
  smtp_sender_name: string
  smtp_token_configured: boolean
  smtp_ssl_enabled: boolean
  smtp_starttls_enabled: boolean
  smtp_insecure_skip_verify: boolean
  smtp_force_auth_login: boolean
  resend_from: string
  resend_sender_name: string
  resend_api_key_configured: boolean
  updated_at?: number
}

export type SiteMailConfigUpdate = {
  provider: SiteMailSaveProvider
  sender_name?: string
  smtp_server?: string
  smtp_port?: number
  smtp_account?: string
  smtp_from?: string
  smtp_sender_name?: string
  smtp_token?: string
  clear_smtp_token?: boolean
  smtp_ssl_enabled?: boolean
  smtp_starttls_enabled?: boolean
  smtp_insecure_skip_verify?: boolean
  smtp_force_auth_login?: boolean
  resend_from?: string
  resend_sender_name?: string
  resend_api_key?: string
  clear_resend_api_key?: boolean
}

/** GET /api/adty/site/self (controller/adty_site_owner.go). */
export type OwnerSite = {
  id: number
  status: OwnerSiteStatus
  subdomain: string
  root_domain: string
  host: string
  base_url: string
  public_base_url?: string
  api_base_url?: string
  cost_factor: string
  custom_domain_enabled: boolean
  domain_automation_enabled?: boolean
  cname_target?: string
  domain_mainland_target?: string
  edge_mainland_enabled?: boolean
  edge_acme_alias_suffix?: string
  sender_domain_enabled: boolean
  brand: SiteBrand
  home_page_content?: string
  legal_docs?: SiteLegalDocs
  /** Payment methods Root authorized for this site's gift, with the owner's rule. */
  bonus_methods: OwnerBonusMethod[]
  seo_index: boolean
  close_execute_at: number
  close_requested_at?: number
  prices: SitePrice[] | null
  pricing_version: number
  domains: SiteDomain[] | null
  visibility: Record<string, string>
}

/** One day (or the total) of GET /api/adty/site/stats. Money fields are USD decimal strings. */
export type SiteDayStats = {
  day: string
  new_customers: number
  paid_customers: number
  active_customers: number
  topup_usd?: string
  revenue_usd: string
  platform_usd: string
  earning_usd: string
  bonus_usd: string
  refund_usd: string
  skipped_usd: string
}

export type SiteStats = {
  from: string
  to: string
  total: SiteDayStats
  series: SiteDayStats[] | null
}

export type EarningKind =
  | 'usage'
  | 'refund'
  | 'bonus'
  | 'bonus_reversal'
  | 'trial'
  | 'skipped'

export type EarningRow = {
  id: number
  day: string
  user_id: number
  kind: EarningKind
  reason?: 'self_use' | 'migration'
  revenue_usd: string
  earning_usd: string
  status: 'pending' | 'available' | 'void'
  settled_at: number
}

export type SiteBalances = {
  requires_review?: boolean
  pending_usd: string
  skipped_usd: string
  available_usd: string
  frozen_usd: string
  withdrawn_usd: string
}

export type OwnerEarnings = {
  balances: SiteBalances
  page: OwnerPage<EarningRow>
}

/** A customer as the owner sees it: email masked unless the platform allows full. */
export type OwnerCustomer = {
  id: number
  username: string
  email: string
  status: number
  group: string
  created_at: number
  balance?: number
  used_quota?: number
  wallet_lot_projection?: WalletLotProjection
  risk_flag?: 'self_use' | 'migration'
  reviewed: boolean
}

export type OwnerToken = {
  id: number
  name: string
  status: number
  accessed_time: number
  created_time: number
}

export type OwnerCustomerDetail = {
  customer: OwnerCustomer
  tokens?: OwnerToken[]
  /** This customer's own prices (user_id = customer). */
  prices: SitePrice[] | null
  pricing_version: number
}

export type OwnerLog = {
  id: number
  created_at: number
  type: number
  model_name: string
  token_name: string
  quota: number
  prompt_tokens: number
  completion_tokens: number
  use_time: number
  is_stream: boolean
}

export type OwnerTopUp = {
  trade_no: string
  face_usd: string
  bonus_rate: string
  bonus_usd: string
  credit_quota: number
  method: string
  status: string
  created_at: number
}

/** One DNS record Resend asks for (service.AdtyResendDomainRecord). */
export type SenderDnsRecord = {
  record: string
  name: string
  type: string
  value: string
  ttl: string
  status: string
  priority?: number
}

/** Result of PUT /api/adty/site/pricing. */
export type PricingResult = {
  version: number
  immediate: string[] | null
  /** Group ('' is the default) → time the increase takes effect. */
  pending: Record<string, number> | null
}
