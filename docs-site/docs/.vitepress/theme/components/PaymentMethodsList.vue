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

const LABELS: Record<string, { zh: string; en: string }> = {
  alipay: { zh: '支付宝（Alipay）', en: 'Alipay' },
  wxpay: { zh: '微信支付（WeChat Pay）', en: 'WeChat Pay' },
  usdt: {
    zh: 'USDT（TRC20 / BEP20 / Polygon / X Layer）',
    en: 'USDT (TRC20 / BEP20 / Polygon / X Layer)',
  },
  bepusdt: {
    zh: 'USDT（TRC20 / BEP20 / Polygon / X Layer）',
    en: 'USDT (TRC20 / BEP20 / Polygon / X Layer)',
  },
}

const activeMethods = computed(() => {
  const raw = brand.value.paymentMethods
  const seen = new Set<string>()
  const out: string[] = []
  for (const id of raw) {
    const key = id.toLowerCase()
    const mapped = LABELS[key]
    const label = mapped ? (isZh.value ? mapped.zh : mapped.en) : id
    if (!seen.has(label)) {
      seen.add(label)
      out.push(label)
    }
  }
  return out
})
</script>

<template>
  <div class="adty-card" data-adty-keep-raw="true">
    <div class="adty-card-header" style="margin-bottom: 8px">
      <h3 class="adty-card-title">
        {{
          isZh
            ? `${brand.name} 当前已启用的在线支付方式`
            : `Active Online Payment Methods on ${brand.name}`
        }}
      </h3>
      <span class="adty-badge">
        {{ isZh ? '实时同步' : 'Live' }}
      </span>
    </div>

    <div v-if="activeMethods.length > 0" class="adty-chip-list">
      <span v-for="m in activeMethods" :key="m" class="adty-chip">
        {{ m }}
      </span>
    </div>
    <p v-else class="adty-muted" style="margin: 6px 0 0">
      {{
        isZh
          ? '以控制台「钱包」页面当前实际展示的在线充值通道（如支付宝、微信支付、USDT 多链）或额度兑换码为准。'
          : 'Check the Wallet page in the console for currently available online payment channels (such as Alipay, WeChat Pay, or multi-chain USDT) and quota voucher redemption.'
      }}
    </p>
  </div>
</template>
