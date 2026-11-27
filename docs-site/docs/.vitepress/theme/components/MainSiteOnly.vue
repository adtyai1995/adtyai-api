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
      {{ isZh ? '该页面暂未开放' : 'Page Unavailable' }}
    </h3>
    <p class="adty-muted" style="margin: 0 0 12px">
      {{
        isZh
          ? '该站点未开放此页面，请返回文档首页查看其他指南。'
          : 'This page is not available on this site. Please return to the documentation home.'
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
