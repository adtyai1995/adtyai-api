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
/** Server-paged list (common.PageInfo). */
export type SitePage<T> = {
  page: number
  page_size: number
  total: number
  items: T[] | null
}

/** GET /api/adty/admin/reseller/usage (service.AdtyResellerUsage). */
export type ResellerUsage = {
  sender: {
    in_use: number
    verified: number
    failed: number
    disabled: number
    /** Unix seconds of the last request refused for lack of Resend slots; 0 if none. */
    quota_full_at: number
  }
  widgets: { id: string; site_key: string; used: number; limit: number }[]
  widget_slots_left: number
  widget_slots_low: boolean
  unassigned_domains: number
}

export type SiteStatus =
  | 'pending'
  | 'active'
  | 'paused'
  | 'closing'
  | 'closed'
  | 'deleting'

/** Brand fields stored in site.brand JSON. */
export type AdminSiteBrand = {
  name?: string
  title?: string
  logo_url?: string
  favicon_url?: string
  notice?: string
  support_contact?: string
  tawk_property_id?: string
  tawk_widget_id?: string
  wallet_notice?: string
  voucher_purchase_url?: string
  docs_url?: string
}

/** A row of GET /api/adty/admin/sites (controller/adty_site_admin.go). Decimals are strings. */
export type AdminSite = {
  id: number
  owner_user_id: number
  ownership_version?: number
  owner_username: string
  subdomain: string
  root_domain: string
  host: string
  api_host?: string
  effective_cost_factor?: string
  status: SiteStatus
  settle_hold_reason?: string
  cost_factor: string | null
  tier_cost_factor: string | null
  tier_month: string
  /** Assigned site level code; empty while no level applies. */
  tier_code?: string
  /** 'manual' when Root pinned the level, otherwise automatic. */
  tier_mode?: string
  visibility: string
  allowed_groups?: string
  /** Brand JSON written by the owner or Root (name, title, notice, wallet_notice, voucher_purchase_url, etc.). */
  brand: string
  home_page_content?: string
  seo_index?: boolean
  contact_info: string
  opened_by: 'apply' | 'admin' | 'invite'
  review_note: string
  close_requested_at: number
  close_execute_at: number
  close_transfer_to: number
  close_claw_back: boolean
  close_reason: string
  closed_at: number
  archived_hidden_at?: number
  partner_code?: string | null
  referrer_site_id?: number
  referrer_site_subdomain?: string
  referrer_rate?: string | null
  referrer_bound_at?: number
  referrer_late_bound?: boolean
  referrer_stopped_at?: number
  referrer_stop_reason?: string
  customers: number
  month_topup_usd?: string
  month_revenue_usd?: string
  month_earning_usd?: string
  month_platform_usd?: string
  growth_pct?: number | null
  created_at: number
  updated_at: number
}

/** GET /api/adty/admin/sites/overview. Decimals are strings. */
export type SitesOverview = {
  from: string
  to: string
  sites: Record<string, number>
  site_customers: number
  main_customers: number
  site_topup_usd: string
  main_topup_usd: string
  site_revenue_usd: string
  site_platform_usd: string
  site_earning_usd: string
  site_skipped_usd: string
  site_bonus_usd: string
  pending_reports: number
  flagged_customers: number
  pending_applications: number
  custom_hostnames: number
  hostname_monthly_fee_usd: string
  earning_hook_failures: number
  settlement_holds: Array<{
    site_id: number
    subdomain: string
    settle_hold_reason: string
  }>
}

export type SiteDayStats = {
  day: string
  revenue_usd: string
  platform_usd: string
  earning_usd: string
  topup_usd: string
}

type UsageTotal = { requests: number; quota: number }
type UsageRow = UsageTotal & { key: string; usd: string }
export type SiteStatsResponse = {
  stats: {
    from: string
    to: string
    total: SiteDayStats
    series: SiteDayStats[]
  }
  breakdown: {
    by_model: UsageRow[]
    by_group: UsageRow[]
    by_payer: UsageRow[]
    unverified: UsageTotal
    unknown_site: UsageTotal
  }
  monthly: unknown
  withdrawn_usd?: string
}

export type FlaggedCustomer = {
  user_id: number
  username?: string
  site_id: number
  risk_flag: string
  risk_reason: string
  reviewed: boolean
  skipped_usd: string
}

export type SiteReport = {
  id: number
  site_id: number
  user_id: number
  username?: string
  reason: string
  status: string
}

export type SiteAudit = {
  id: number
  site_id: number
  action: string
  operator_id: number
  operator_username?: string
  old_value: string
  new_value: string
  reason: string
  created_at: number
}

export type SiteCustomer = {
  id: number
  username: string
  email: string
  quota: number
  used_quota: number
  status: number
  group: string
  created_at: number
  risk_flag: '' | 'self_use' | 'migration'
  risk_reason: string
  reviewed: boolean
  joined_at: number
}

/** PUT /api/adty/admin/sites/:id/status. Independent pause/resume is retired; only close is a write status. Omitting transfer_to or sending 0 inherits to main; a non-zero value is rejected. */
export type SiteStatusChange = {
  execute_at?: number
  expected_execute_at?: number
  status: 'closed'
  transfer_to?: number
  claw_back?: boolean
  immediate?: boolean
  reason: string
}

export type TurnstileWidget = { id: string; site_key: string }

/**
 * GET /api/adty/admin/reseller/settings (controller/adty_site_admin.go).
 * Secrets never come back: cloudflare_apiToken is empty and
 * turnstile_widget_secret is null; the *_configured flags say whether one is saved.
 */
export type ResellerSettings = {
  domain_automation_enabled?: boolean
  aliyun_domain?: string
  domain_mainland_target?: string
  aliyun_access_key_id?: string
  aliyun_access_key_Secret?: string
  aliyun_id_configured?: boolean
  aliyun_secret_configured?: boolean
  enabled: boolean
  root_domains: string[] | null
  reserved_subdomains: string[] | null
  unknown_host_mode: 'main' | 'reject'
  markup_increase_delay_hours: number
  settle_hour: number
  freeze_days: number
  close_notice_days: number
  site_login_only: boolean
  custom_domain_enabled: boolean
  sender_domain_enabled: boolean
  self_use_check_enabled: boolean
  migration_check_enabled: boolean
  fingerprint_window_days: number
  customer_lookup_per_hour: number
  max_custom_domains_per_site: number
  custom_domain_creates_per_hour: number
  visibility: Record<string, string> | null
  cloudflare_apiToken: string
  cloudflare_zone_id: string
  cloudflare_account_id: string
  cname_target: string
  fallback_origin: string
  edge_mainland_enabled?: boolean
  edge_acme_alias_suffix?: string
  edge_acme_staging?: boolean
  edge_node_ip?: string
  edge_node_ips?: string[] | null
  edge_failover_to_cf?: boolean
  edge_probe_enabled?: boolean
  edge_auto_failover?: boolean
  edge_main_site_enabled?: boolean
  edge_main_site_host?: string
  edge_main_site_origin_sni?: string
  partner_contact_telegram?: string
  partner_referral_default_rate?: number | null
  turnstile_widgets: TurnstileWidget[] | null
  turnstile_widget_secret: Record<string, string> | null
  cloudflare_token_configured: boolean
  turnstile_secret_configured: Record<string, boolean> | null
  cloudflare_configured: boolean
  resend_configured: boolean
  field_versions?: Record<string, string>
  settings_warnings?: string[]
  money_configured: boolean
  tier_configured?: boolean
  site_customer_terms_version: number
}

export type ResellerEdgeWildcardCert = {
  cert_name: string
  challenge_host: string
  alias_fqdn: string
  environment: string
  status: string
  not_after: number
  last_error: string
  last_issued_at: number
  attempts: number
}

export type ResellerEdgeNodePull = {
  pulled_at: number
  version: string
}

export type ResellerEdgeManifestPull = {
  pulled_at: number
  client_ip: string
  version: string
  nodes?: Record<string, ResellerEdgeNodePull>
}

export type ResellerEdgeDNSSync = {
  synced_at: number
  target: string
  ip: string
  ips?: string[]
  proxied: boolean
  auto_mode?: boolean
  ttl: number
  record_id: string
  record_ids?: string[]
  last_error: string
}

export type ResellerEdgeNodeProbe = {
  ip: string
  checked_at: number
  healthy: boolean
  http_status: number
  latency_ms: number
  cert_not_after: number
  consecutive_failures: number
  consecutive_successes: number
  ejected: boolean
  ejected_at?: number
  last_error: string
}

export type ResellerEdgeProbe = {
  checked_at: number
  healthy: boolean
  healthy_count?: number
  total_count?: number
  probe_host: string
  http_status: number
  latency_ms: number
  cert_not_after: number
  consecutive_failures: number
  consecutive_successes: number
  auto_failover_active?: boolean
  active_dns_ips?: string[]
  last_auto_action?: string
  last_auto_action_at?: number
  last_error: string
  nodes?: ResellerEdgeNodeProbe[]
}

export type ResellerEdgeStatus = {
  edge_mainland_enabled: boolean
  edge_acme_staging: boolean
  edge_node_ip: string
  edge_node_ips?: string[]
  edge_failover_to_cf: boolean
  edge_probe_enabled: boolean
  edge_auto_failover?: boolean
  edge_main_site_enabled?: boolean
  edge_main_site_host?: string
  edge_main_site_origin_sni?: string
  edge_token_configured: boolean
  domain_mainland_target: string
  edge_acme_alias_suffix: string
  wildcard_cert: ResellerEdgeWildcardCert | null
  main_site_cert?: ResellerEdgeWildcardCert | null
  custom_cert_counts: {
    total: number
    ready: number
    waiting_dns: number
    issuing: number
    failed: number
  }
  manifest_pull: ResellerEdgeManifestPull | null
  dns_sync: ResellerEdgeDNSSync | null
  probe: ResellerEdgeProbe | null
}

/** PUT body: only changed keys; clear_secrets empties secrets. */
export type ResellerSettingsUpdate = Partial<
  Omit<
    ResellerSettings,
    | 'cloudflare_token_configured'
    | 'turnstile_secret_configured'
    | 'cloudflare_configured'
    | 'resend_configured'
    | 'money_configured'
    | 'tier_configured'
    | 'site_customer_terms_version'
  >
> & {
  expected_versions?: Record<string, string>
  clear_secrets?: Array<
    | 'cloudflare_apiToken'
    | 'turnstile_widget_secret'
    | 'aliyun_access_key_id'
    | 'aliyun_access_key_Secret'
  >
}

export type InviteCode = {
  id: number
  code: string
  max_uses: number
  used_count: number
  expire_at: number
  cost_factor: string | null
  note: string
  created_by: number
  created_at: number
}

export type InviteCodeInput = {
  code?: string
  max_uses: number
  expire_at: number
  cost_factor?: string
  note: string
}

export type TierPrincipalAdjustMode = 'delta' | 'set_total'

export type TierPrincipalAdjustment = {
  id: number
  site_id: number
  mode: TierPrincipalAdjustMode
  delta_usd: string
  online_before_usd: string
  adjust_before_usd: string
  adjust_after_usd: string
  effective_after_usd: string
  tier_before: string
  tier_after: string
  reason: string
  operator_id: number
  created_at: number
}

export type AdminSiteTierPrincipalView = {
  site_id: number
  tier_code: string
  tier_mode: string
  online_principal_usd: string
  adjustment_usd: string
  effective_principal_usd: string
  items: TierPrincipalAdjustment[]
  total: number
  page: number
  page_size: number
}

export type AdminSiteTierPrincipalAdjustResult = {
  adjustment: TierPrincipalAdjustment
  site: AdminSite
  replayed?: boolean
}

export type PartnerLeadItem = {
  id: number
  referrer_site_id: number
  referral_code: string
  referrer_site_subdomain?: string
  referrer_site_name?: string
  contact_name: string
  contact_channel: string
  contact_value: string
  estimated_volume: string
  note: string
  status: 'pending' | 'approved' | 'rejected'
  opened_site_id: number
  opened_site_subdomain?: string
  reviewed_by: number
  reviewed_by_username?: string
  reviewed_at: number
  review_note: string
  created_at: number
}

export type OpenSitePayload = {
  owner_username: string
  owner_password: string
  owner_email?: string
  owner_display_name?: string
  subdomain: string
  root_domain?: string
  cost_factor?: string
  tier_code?: string
  tier_mode?: 'auto' | 'manual'
  contact_info?: string
  partner_lead_id?: number
  referrer_site_id?: number
  referrer_code?: string
  referrer_rate?: string
}

export type HourlySettlementRunResult = {
  hour_slot: string
  sites_settled: number
  partner_settled: number
  main_ref_settled: number
  site_ref_settled: number
}
