<!--
Copyright (C) 2023-2026 QuantumNous
-->
<script setup lang="ts">
import { useData } from 'vitepress'
import { computed } from 'vue'

import { useSiteBrand } from '../brand'

const { lang } = useData()
const brand = useSiteBrand()

const isZh = computed(() => lang.value.toLowerCase().startsWith('zh'))
const refInfo = computed(() => brand.value.referral)

const rateDisplay = computed(() => {
  if (refInfo.value.defaultRatePercent) {
    return `${refInfo.value.defaultRatePercent}%`
  }
  return isZh.value ? '按推广员配置' : 'Per-affiliate rate'
})

const observationDisplay = computed(() => {
  const days = refInfo.value.observationDays
  if (days <= 0) {
    return isZh.value ? '0 天（即时可用）' : '0 days (Immediate)'
  }
  return isZh.value ? `${days} 天` : `${days} days`
})

const cashMethodsDisplay = computed(() => {
  const methods = refInfo.value.withdrawMethods
  if (!methods || methods.length === 0) {
    return isZh.value ? '仅支持转入账户余额' : 'Account balance only'
  }
  const labels: string[] = []
  if (methods.includes('usdt')) {
    const nets = refInfo.value.withdrawUSDTNetworks
    labels.push(nets.length > 0 ? `USDT (${nets.join(' / ')})` : 'USDT')
  }
  if (methods.includes('alipay')) {
    labels.push(isZh.value ? '支付宝' : 'Alipay')
  }
  return labels.join(isZh.value ? '、' : ', ')
})

const cashThresholdSub = computed(() => {
  const parts: string[] = []
  if (refInfo.value.minWithdrawUSD) {
    parts.push(
      isZh.value
        ? `最低提现 $${refInfo.value.minWithdrawUSD}`
        : `Min $${refInfo.value.minWithdrawUSD}`
    )
  }
  if (refInfo.value.usdtWithdrawFeeUSD) {
    parts.push(
      isZh.value
        ? `USDT 手续费 $${refInfo.value.usdtWithdrawFeeUSD}`
        : `USDT fee $${refInfo.value.usdtWithdrawFeeUSD}`
    )
  }
  if (parts.length === 0) {
    return isZh.value
      ? '具体门槛与手续费以控制台推广页展示为准'
      : 'See the Referral page in console for live thresholds'
  }
  return parts.join(' · ')
})
</script>

<template>
  <div class="adty-card" data-adty-keep-raw="true">
    <template v-if="brand.loaded && !refInfo.enabled">
      <div class="adty-card-header">
        <h3 class="adty-card-title">
          {{ isZh ? '当前站点暂未开启推广返佣' : 'Referral Program Not Enabled' }}
        </h3>
      </div>
      <p class="adty-muted" style="margin: 0">
        {{
          isZh
            ? `${brand.name} 当前未开启推广返佣计划。如有合作意向，请通过支持中心联系站点客服。`
            : `${brand.name} does not currently have the referral program enabled. Contact site support if you have partnership questions.`
        }}
      </p>
    </template>

    <template v-else>
      <div class="adty-card-header">
        <h3 class="adty-card-title">
          {{
            isZh
              ? `${brand.name} 当前实时推广返佣规则`
              : `${brand.name} Live Referral Rules`
          }}
        </h3>
        <span class="adty-badge">
          {{ isZh ? '已与当前站点配置自动同步' : 'Synced live from site settings' }}
        </span>
      </div>

      <div class="adty-grid">
        <div class="adty-stat-box">
          <div class="adty-stat-label">
            {{ isZh ? '默认返佣比例' : 'Default Commission Rate' }}
          </div>
          <div class="adty-stat-value">{{ rateDisplay }}</div>
          <div class="adty-stat-sub">
            {{
              isZh
                ? '一级直推按被邀请人有效付费充值计算'
                : 'Direct 1st-tier commission on qualified top-ups'
            }}
          </div>
        </div>

        <div class="adty-stat-box">
          <div class="adty-stat-label">
            {{ isZh ? '佣金观察期 / 冻结期' : 'Observation Period' }}
          </div>
          <div class="adty-stat-value">{{ observationDisplay }}</div>
          <div class="adty-stat-sub">
            {{
              isZh
                ? '观察期满且无退款后自动转为可兑现佣金'
                : 'Releases to withdrawable balance after lock period'
            }}
          </div>
        </div>

        <div class="adty-stat-box">
          <div class="adty-stat-label">
            {{ isZh ? '转入账户余额' : 'Transfer to Balance' }}
          </div>
          <div class="adty-stat-value">
            {{ isZh ? '0 手续费 · 即时到账' : '0% Fee · Instant' }}
          </div>
          <div class="adty-stat-sub">
            {{
              isZh
                ? '无最低金额门槛，无需人工审核'
                : 'No minimum amount and no manual approval required'
            }}
          </div>
        </div>

        <div class="adty-stat-box">
          <div class="adty-stat-label">
            {{ isZh ? '外部现金提现方式' : 'Cash Withdrawal Methods' }}
          </div>
          <div class="adty-stat-value" style="font-size: 15px">
            {{ cashMethodsDisplay }}
          </div>
          <div class="adty-stat-sub">{{ cashThresholdSub }}</div>
        </div>
      </div>
    </template>
  </div>
</template>
