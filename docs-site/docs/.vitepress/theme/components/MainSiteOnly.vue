<!--
Copyright (C) 2023-2026 QuantumNous
-->
<script setup lang="ts">
import { useData, withBase } from 'vitepress'
import { computed } from 'vue'

import { useSiteBrand } from '../brand'

const { lang } = useData()
const brand = useSiteBrand()

const isZh = computed(() => lang.value.toLowerCase().startsWith('zh'))
const homeHref = computed(() => withBase(isZh.value ? '/zh-cn/' : '/'))
</script>

<template>
  <div v-if="brand.isSubsite" class="adty-card" data-adty-keep-raw="true">
    <h3 class="adty-card-title" style="margin-bottom: 8px">
      {{ isZh ? '该页面仅在主站展示' : 'Main Platform Page Only' }}
    </h3>
    <p class="adty-muted" style="margin: 0 0 12px">
      {{
        isZh
          ? '当前访问的是独立品牌站点，分站合作介绍不在当前站点开放展示。'
          : 'This partnership overview is only displayed on the main platform site.'
      }}
    </p>
    <a class="adty-wizard-link" :href="homeHref">
      {{ isZh ? '← 返回文档首页' : '← Back to Documentation Home' }}
    </a>
  </div>
  <div v-else>
    <slot />
  </div>
</template>
