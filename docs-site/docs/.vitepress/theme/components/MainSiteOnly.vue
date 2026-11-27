<!--
Copyright (C) 2023-2026 QuantumNous
-->
<script setup lang="ts">
import { useData, withBase } from 'vitepress'
import { computed, watchEffect } from 'vue'

import { useSiteBrand } from '../brand'

const props = defineProps<{
  pageTitle?: string
}>()

const { lang } = useData()
const brand = useSiteBrand()

const isZh = computed(() => lang.value.toLowerCase().startsWith('zh'))
const homeHref = computed(() => withBase(isZh.value ? '/zh-cn/' : '/'))
const isConfirmedMainSite = computed(
  () => brand.value.loaded && brand.value.isMainSite && !brand.value.isSubsite
)
const isUnavailable = computed(
  () =>
    brand.value.isSubsite ||
    brand.value.loadFailed ||
    (brand.value.loaded && !brand.value.isMainSite)
)

watchEffect(() => {
  if (
    typeof document === 'undefined' ||
    !isConfirmedMainSite.value ||
    !props.pageTitle
  ) {
    return
  }
  document.title = `${props.pageTitle} | ${brand.value.name}`
})
</script>

<template>
  <div v-if="isConfirmedMainSite">
    <slot />
  </div>
  <div v-else-if="isUnavailable" class="adty-card" data-adty-keep-raw="true">
    <h3 class="adty-card-title" style="margin-bottom: 8px">
      {{ isZh ? '该页面暂未开放' : 'This page is not available.' }}
    </h3>
    <p class="adty-muted" style="margin: 0 0 12px">
      {{
        isZh
          ? '该页面暂未开放，请返回文档首页查看其他指南。'
          : 'This page is not available. Please return to the documentation home.'
      }}
    </p>
    <a class="adty-wizard-link" :href="homeHref">
      {{ isZh ? '← 返回文档首页' : '← Back to Documentation Home' }}
    </a>
  </div>
  <div v-else class="adty-card" data-adty-keep-raw="true" aria-busy="true">
    <p class="adty-muted" style="margin: 0">
      {{ isZh ? '正在加载页面...' : 'Loading...' }}
    </p>
  </div>
</template>
