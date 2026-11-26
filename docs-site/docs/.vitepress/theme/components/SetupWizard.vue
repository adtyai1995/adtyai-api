<!--
Copyright (C) 2023-2026 QuantumNous
-->
<script setup lang="ts">
import { useData, withBase } from 'vitepress'
import { computed, ref } from 'vue'

import { useSiteBrand } from '../brand'

type ClientId =
  | 'claude-code'
  | 'codex'
  | 'kimi-code'
  | 'grok-build'
  | 'opencode'
  | 'openclaw'
  | 'cherry-studio'
  | 'openai-sdk'
  | 'anthropic-sdk'
  | 'gemini-sdk'

type OsId = 'unix' | 'windows'

interface ClientOption {
  id: ClientId
  label: string
  supportsOs: boolean
  guideEn: string
  guideZh: string
}

const CLIENTS: ClientOption[] = [
  {
    id: 'claude-code',
    label: 'Claude Code',
    supportsOs: true,
    guideEn: '/integrations/cli/claude-code',
    guideZh: '/zh-cn/integrations/cli/claude-code',
  },
  {
    id: 'codex',
    label: 'Codex CLI',
    supportsOs: true,
    guideEn: '/integrations/cli/codex',
    guideZh: '/zh-cn/integrations/cli/codex',
  },
  {
    id: 'kimi-code',
    label: 'Kimi Code',
    supportsOs: true,
    guideEn: '/integrations/cli/kimi-code',
    guideZh: '/zh-cn/integrations/cli/kimi-code',
  },
  {
    id: 'grok-build',
    label: 'Grok Build CLI',
    supportsOs: true,
    guideEn: '/integrations/cli/grok-build',
    guideZh: '/zh-cn/integrations/cli/grok-build',
  },
  {
    id: 'opencode',
    label: 'OpenCode',
    supportsOs: false,
    guideEn: '/integrations/advanced/opencode',
    guideZh: '/zh-cn/integrations/advanced/opencode',
  },
  {
    id: 'openclaw',
    label: 'OpenClaw',
    supportsOs: false,
    guideEn: '/integrations/advanced/openclaw',
    guideZh: '/zh-cn/integrations/advanced/openclaw',
  },
  {
    id: 'cherry-studio',
    label: 'Cherry Studio',
    supportsOs: false,
    guideEn: '/integrations/advanced/cherry-studio',
    guideZh: '/zh-cn/integrations/advanced/cherry-studio',
  },
  {
    id: 'openai-sdk',
    label: 'OpenAI SDK',
    supportsOs: false,
    guideEn: '/integrations/advanced/sdks',
    guideZh: '/zh-cn/integrations/advanced/sdks',
  },
  {
    id: 'anthropic-sdk',
    label: 'Anthropic SDK',
    supportsOs: false,
    guideEn: '/integrations/advanced/sdks',
    guideZh: '/zh-cn/integrations/advanced/sdks',
  },
  {
    id: 'gemini-sdk',
    label: 'Gemini SDK',
    supportsOs: false,
    guideEn: '/integrations/advanced/sdks',
    guideZh: '/zh-cn/integrations/advanced/sdks',
  },
]

const { lang } = useData()
const brand = useSiteBrand()

const isZh = computed(() => lang.value.toLowerCase().startsWith('zh'))
const selectedClient = ref<ClientId>('claude-code')
const selectedOs = ref<OsId>('unix')
const apiKeyInput = ref('')
const copied = ref(false)

const activeOption = computed(
  () => CLIENTS.find((c) => c.id === selectedClient.value) || CLIENTS[0]
)

const effectiveKey = computed(
  () => apiKeyInput.value.trim() || 'sk-your-api-key'
)

const apiBase = computed(() => brand.value.apiBase || 'https://api.adtyai.com')

const guideHref = computed(() =>
  withBase(isZh.value ? activeOption.value.guideZh : activeOption.value.guideEn)
)

const snippetTitle = computed(() => {
  switch (selectedClient.value) {
    case 'claude-code':
      return selectedOs.value === 'windows'
        ? 'PowerShell — Claude Code'
        : 'Bash / Zsh — Claude Code'
    case 'codex':
      return '~/.codex/config.toml & auth.json'
    case 'kimi-code':
      return selectedOs.value === 'windows'
        ? 'PowerShell — Kimi Code'
        : 'Bash / Zsh — Kimi Code'
    case 'grok-build':
      return selectedOs.value === 'windows'
        ? 'PowerShell — Grok Build CLI'
        : 'Bash / Zsh — Grok Build CLI'
    case 'opencode':
      return '~/.config/opencode/opencode.json'
    case 'openclaw':
      return '~/.openclaw/openclaw.json'
    case 'cherry-studio':
      return isZh.value ? 'Cherry Studio 供应商参数' : 'Cherry Studio Provider Settings'
    case 'openai-sdk':
      return 'Python — openai'
    case 'anthropic-sdk':
      return 'Python — anthropic'
    case 'gemini-sdk':
      return 'Python — google-genai'
  }
})

const snippet = computed(() => {
  const base = apiBase.value
  const key = effectiveKey.value
  const unix = selectedOs.value === 'unix'

  switch (selectedClient.value) {
    case 'claude-code':
      return unix
        ? `# 1. Install Claude Code\nnpm install -g @anthropic-ai/claude-code\n\n# 2. Configure API endpoint & key\nexport ANTHROPIC_BASE_URL="${base}"\nexport ANTHROPIC_AUTH_TOKEN="${key}"\n\n# 3. Launch in your project\nclaude`
        : `# 1. Install Claude Code\nnpm install -g @anthropic-ai/claude-code\n\n# 2. Configure API endpoint & key (PowerShell)\n$env:ANTHROPIC_BASE_URL="${base}"\n$env:ANTHROPIC_AUTH_TOKEN="${key}"\n\n# 3. Launch in your project\nclaude`

    case 'codex':
      return unix
        ? `# ~/.codex/config.toml\nmodel_provider = "adty"\nmodel = "gpt-5.4"\n\n[model_providers.adty]\nname = "ADTY"\nbase_url = "${base}/v1"\nwire_api = "responses"\nrequires_openai_auth = true\n\n# ~/.codex/auth.json\n# { "OPENAI_API_KEY": "${key}" }`
        : `# $HOME\\.codex\\config.toml\nmodel_provider = "adty"\nmodel = "gpt-5.4"\n\n[model_providers.adty]\nname = "ADTY"\nbase_url = "${base}/v1"\nwire_api = "responses"\nrequires_openai_auth = true\n\n# $HOME\\.codex\\auth.json\n# { "OPENAI_API_KEY": "${key}" }`

    case 'kimi-code':
      return unix
        ? `export ANTHROPIC_BASE_URL="${base}"\nexport ANTHROPIC_AUTH_TOKEN="${key}"\n# Or configure OpenAI-compatible provider URL: ${base}/v1`
        : `$env:ANTHROPIC_BASE_URL="${base}"\n$env:ANTHROPIC_AUTH_TOKEN="${key}"\n# Or configure OpenAI-compatible provider URL: ${base}/v1`

    case 'grok-build':
      return unix
        ? `export XAI_BASE_URL="${base}/v1"\nexport XAI_API_KEY="${key}"\ngrok`
        : `$env:XAI_BASE_URL="${base}/v1"\n$env:XAI_API_KEY="${key}"\ngrok`

    case 'opencode':
      return `{\n  "$schema": "https://opencode.ai/config.json",\n  "provider": {\n    "adty": {\n      "npm": "@ai-sdk/openai-compatible",\n      "name": "${brand.value.name}",\n      "options": {\n        "baseURL": "${base}/v1",\n        "apiKey": "${key}"\n      }\n    }\n  }\n}`

    case 'openclaw':
      return `{\n  "models": {\n    "providers": {\n      "adty": {\n        "baseUrl": "${base}/v1",\n        "apiKey": "${key}",\n        "api": "openai-completions"\n      }\n    }\n  }\n}`

    case 'cherry-studio':
      return isZh.value
        ? `提供商名称：${brand.value.name}\n接口类型：OpenAI（或 Anthropic / Gemini 按需选择）\nAPI 地址：${base}\nAPI 密钥：${key}\n\n提示：Cherry Studio 填写 OpenAI 类型时填 ${base} 即可自动补全 /v1；如客户端不自动补全，请填 ${base}/v1。`
        : `Provider Name: ${brand.value.name}\nAPI Type: OpenAI (or Anthropic / Gemini as needed)\nAPI Host: ${base}\nAPI Key: ${key}\n\nNote: Cherry Studio appends /v1 automatically for OpenAI providers; if a client does not append it, use ${base}/v1.`

    case 'openai-sdk':
      return `from openai import OpenAI\n\nclient = OpenAI(\n    base_url="${base}/v1",\n    api_key="${key}",\n)\n\nresp = client.chat.completions.create(\n    model="gpt-5.4",\n    messages=[{"role": "user", "content": "Hello!"}],\n)\nprint(resp.choices[0].message.content)`

    case 'anthropic-sdk':
      return `import anthropic\n\nclient = anthropic.Anthropic(\n    base_url="${base}",\n    api_key="${key}",\n)\n\nmsg = client.messages.create(\n    model="claude-sonnet-4-6",\n    max_tokens=1024,\n    messages=[{"role": "user", "content": "Hello!"}],\n)\nprint(msg.content[0].text)`

    case 'gemini-sdk':
      return `from google import genai\nfrom google.genai import types\n\nclient = genai.Client(\n    api_key="${key}",\n    http_options=types.HttpOptions(base_url="${base}"),\n)\n\nresp = client.models.generate_content(\n    model="gemini-2.5-pro",\n    contents="Hello!",\n)\nprint(resp.text)`
  }
})

async function copySnippet() {
  try {
    await navigator.clipboard.writeText(snippet.value)
    copied.value = true
    window.setTimeout(() => {
      copied.value = false
    }, 1800)
  } catch {
    copied.value = false
  }
}
</script>

<template>
  <div class="adty-wizard" data-adty-keep-raw="true">
    <div class="adty-card-header">
      <div>
        <h3 class="adty-card-title">
          {{ isZh ? '交互式接入配置生成器' : 'Interactive Setup Generator' }}
        </h3>
        <div class="adty-muted">
          {{
            isZh
              ? '选择客户端与系统，自动带入当前站点专属 API 地址，一键复制即可使用。'
              : 'Pick your client and OS to generate a ready-to-copy config using this site’s live API Base URL.'
          }}
        </div>
      </div>
      <span class="adty-badge">API: {{ apiBase }}</span>
    </div>

    <div class="adty-wizard-tabs" role="tablist">
      <button
        v-for="item in CLIENTS"
        :key="item.id"
        type="button"
        role="tab"
        :aria-selected="selectedClient === item.id"
        :class="['adty-wizard-tab', { active: selectedClient === item.id }]"
        @click="selectedClient = item.id"
      >
        {{ item.label }}
      </button>
    </div>

    <div class="adty-wizard-controls">
      <div v-if="activeOption.supportsOs" class="adty-wizard-seg">
        <button
          type="button"
          :class="{ active: selectedOs === 'unix' }"
          @click="selectedOs = 'unix'"
        >
          macOS / Linux
        </button>
        <button
          type="button"
          :class="{ active: selectedOs === 'windows' }"
          @click="selectedOs = 'windows'"
        >
          Windows PowerShell
        </button>
      </div>
      <input
        v-model="apiKeyInput"
        class="adty-wizard-input"
        type="password"
        autocomplete="off"
        spellcheck="false"
        :placeholder="
          isZh
            ? '可选：粘贴你的 API Key 仅在本地预览替换（不上传）'
            : 'Optional: paste your API key locally for preview (never sent)'
        "
      />
    </div>

    <div class="adty-wizard-code-wrap">
      <div class="adty-wizard-code-bar">
        <span>{{ snippetTitle }}</span>
        <button type="button" class="adty-wizard-copy-btn" @click="copySnippet">
          {{
            copied
              ? isZh
                ? '已复制 ✓'
                : 'Copied ✓'
              : isZh
                ? '一键复制'
                : 'Copy Config'
          }}
        </button>
      </div>
      <pre class="adty-wizard-pre"><code>{{ snippet }}</code></pre>
    </div>

    <div class="adty-wizard-footer">
      <span class="adty-muted">
        {{
          isZh
            ? '提示：模型请求必须填写 API 域名，不可填写网页控制台域名（否则会返回 403）。'
            : 'Tip: Always point clients to the dedicated API domain above—not the web console domain (which returns HTTP 403).'
        }}
      </span>
      <a class="adty-wizard-link" :href="guideHref">
        {{
          isZh
            ? `查看 ${activeOption.label} 完整教程 →`
            : `Read full ${activeOption.label} guide →`
        }}
      </a>
    </div>
  </div>
</template>
