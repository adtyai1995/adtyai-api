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
import { lazy } from 'react'

import { parseCurrencyDisplayType } from '@/lib/currency'

import type { BillingSettings } from '../types'
import { createSectionRegistry } from '../utils/section-registry'

const PricingSection = lazy(() =>
  import('../general/pricing-section').then((module) => ({
    default: module.PricingSection,
  }))
)
const QuotaSettingsSection = lazy(() =>
  import('../general/quota-settings-section').then((module) => ({
    default: module.QuotaSettingsSection,
  }))
)
const PaymentSettingsSection = lazy(() =>
  import('../integrations/payment-settings-section').then((module) => ({
    default: module.PaymentSettingsSection,
  }))
)
const RechargeBonusSection = lazy(() =>
  import('./recharge-bonus-section').then((module) => ({
    default: module.RechargeBonusSection,
  }))
)
const RatioSettingsCard = lazy(() =>
  import('../models/ratio-settings-card').then((module) => ({
    default: module.RatioSettingsCard,
  }))
)

const getModelDefaults = (settings: BillingSettings) => ({
  ModelPrice: settings.ModelPrice,
  ModelRatio: settings.ModelRatio,
  CacheRatio: settings.CacheRatio,
  CreateCacheRatio: settings.CreateCacheRatio,
  CompletionRatio: settings.CompletionRatio,
  ImageRatio: settings.ImageRatio,
  AudioRatio: settings.AudioRatio,
  AudioCompletionRatio: settings.AudioCompletionRatio,
  ExposeRatioEnabled: settings.ExposeRatioEnabled,
  BillingMode: settings['billing_setting.billing_mode'],
  BillingExpr: settings['billing_setting.billing_expr'],
  PluginBillingExpr: settings['billing_setting.plugin_billing_expr'],
})

const getGroupDefaults = (settings: BillingSettings) => ({
  TopupGroupRatio: settings.TopupGroupRatio,
  GroupRatio: settings.GroupRatio,
  UserUsableGroups: settings.UserUsableGroups,
  GroupGroupRatio: settings.GroupGroupRatio,
  AutoGroups: settings.AutoGroups,
  MaxTokenAutoGroups: settings.MaxTokenAutoGroups,
  DefaultUseAutoGroup: settings.DefaultUseAutoGroup,
  GroupSpecialUsableGroup:
    settings['group_ratio_setting.group_special_usable_group'],
  GroupModelAllow: settings.GroupModelAllow ?? '{}',
})

const BILLING_SECTIONS = [
  {
    id: 'quota',
    titleKey: 'Quota Settings',
    build: (settings: BillingSettings) => (
      <QuotaSettingsSection
        defaultValues={{
          QuotaForNewUser: settings.QuotaForNewUser,
          quota_setting: {
            trust_quota_usd: settings['quota_setting.trust_quota_usd'],
            pre_consume_multiplier:
              settings['quota_setting.pre_consume_multiplier'],
            enable_free_model_pre_consume:
              settings['quota_setting.enable_free_model_pre_consume'],
          },
        }}
      />
    ),
  },
  {
    id: 'currency',
    titleKey: 'Currency & Display',
    build: (settings: BillingSettings) => (
      <PricingSection
        defaultValues={{
          QuotaPerUnit: settings.QuotaPerUnit,
          USDExchangeRate: settings.USDExchangeRate,
          DisplayInCurrencyEnabled: settings.DisplayInCurrencyEnabled,
          DisplayTokenStatEnabled: settings.DisplayTokenStatEnabled,
          general_setting: {
            quota_display_type: parseCurrencyDisplayType(
              settings['general_setting.quota_display_type']
            ),
            custom_currency_symbol:
              settings['general_setting.custom_currency_symbol'] ?? '¤',
            custom_currency_exchange_rate:
              settings['general_setting.custom_currency_exchange_rate'] ?? 1,
          },
        }}
      />
    ),
  },
  {
    id: 'model-pricing',
    titleKey: 'Model Pricing',
    build: (settings: BillingSettings) => (
      <RatioSettingsCard
        titleKey='Model Pricing'
        modelDefaults={getModelDefaults(settings)}
        groupDefaults={getGroupDefaults(settings)}
        toolPricesDefault={settings['tool_price_setting.prices']}
        visibleTabs={['models', 'unset-models', 'tool-prices', 'upstream-sync']}
      />
    ),
  },
  {
    id: 'group-pricing',
    titleKey: 'Group Pricing',
    build: (settings: BillingSettings) => (
      <RatioSettingsCard
        titleKey='Group Pricing'
        modelDefaults={getModelDefaults(settings)}
        groupDefaults={getGroupDefaults(settings)}
        toolPricesDefault={settings['tool_price_setting.prices']}
        visibleTabs={['groups']}
      />
    ),
  },
  {
    id: 'payment',
    titleKey: 'Payment Gateway',
    build: (settings: BillingSettings) => (
      <PaymentSettingsSection
        defaultValues={{
          Price: settings.Price,
          MinTopUp: settings.MinTopUp,
          CustomCallbackAddress: settings.CustomCallbackAddress,
          AmountOptions: settings['payment_setting.amount_options'],
        }}
        epayDefaultValues={{
          orderMinutes: settings['adty_topup_setting.epay_order_minutes'],
          channelTimeoutSeconds:
            settings['adty_epay_setting.channel_timeout_seconds'],
          totalTimeoutSeconds:
            settings['adty_epay_setting.total_timeout_seconds'],
          queryAfterSeconds: settings['adty_epay_setting.query_after_seconds'],
          expiredQuerySeconds:
            settings['adty_epay_setting.expired_query_seconds'],
          unpaidThreshold: settings['adty_epay_setting.unpaid_threshold'],
          probeEnabled: settings['adty_epay_setting.probe_enabled'],
          probeIntervalMinutes:
            settings['adty_epay_setting.probe_interval_minutes'],
        }}
        usdtDefaultValues={{
          enabled: settings['adty_bepusdt.enabled'],
          baseUrl: settings['adty_bepusdt.base_url'],
          tradeTypes: settings['adty_bepusdt.trade_types'],
          orderMinutes: settings['adty_topup_setting.usdt_order_minutes'],
        }}
      />
    ),
  },
  {
    id: 'recharge-bonus',
    titleKey: 'Recharge Bonus',
    build: () => <RechargeBonusSection />,
  },
] as const

export type BillingSectionId = (typeof BILLING_SECTIONS)[number]['id']

const billingRegistry = createSectionRegistry<
  BillingSectionId,
  BillingSettings
>({
  sections: BILLING_SECTIONS,
  defaultSection: 'quota',
  basePath: '/system-settings/billing',
  urlStyle: 'path',
})

export const BILLING_SECTION_IDS = billingRegistry.sectionIds
export const BILLING_DEFAULT_SECTION = billingRegistry.defaultSection
export const getBillingSectionNavItems = billingRegistry.getSectionNavItems
export const getBillingSectionContent = billingRegistry.getSectionContent
export const getBillingSectionMeta = billingRegistry.getSectionMeta
