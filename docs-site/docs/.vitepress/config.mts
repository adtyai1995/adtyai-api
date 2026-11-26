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

import { defineConfig } from 'vitepress'

export default defineConfig({
  base: process.env.VITEPRESS_BASE || '/docs/',
  outDir: process.env.VITEPRESS_OUT || '../dist',
  cleanUrls: true,
  lastUpdated: false,

  title: 'ADTY AI',
  description: 'ADTY AI API & Client Integration Documentation',

  locales: {
    root: {
      label: 'English',
      lang: 'en-US',
      title: 'ADTY AI',
      description: 'ADTY AI API & Client Integration Documentation',
      themeConfig: {
        nav: [
          { text: 'Guides', link: '/guides/overview' },
          { text: 'Integrations', link: '/integrations/cli/' },
          { text: 'API Reference', link: '/api/' },
          { text: 'FAQ', link: '/faq/' },
          { text: 'Subsite Partnership', link: '/guides/partner' },
          { text: 'Console', link: 'https://www.adtyai.com' },
        ],
        sidebar: [
          {
            text: 'Platform Guides',
            collapsed: false,
            items: [
              { text: 'Quick Start & Dual Domains', link: '/guides/overview' },
              { text: 'Wallet, Bonus & Vouchers', link: '/guides/wallet-and-vouchers' },
              { text: 'Account Security & PAT', link: '/guides/security' },
              { text: 'Team Collaboration', link: '/guides/teams' },
              { text: 'Channel Availability', link: '/guides/channel-status' },
              { text: 'Referral Program', link: '/guides/referral' },
              { text: 'Support Center', link: '/guides/support' },
              { text: 'Subsite Partnership', link: '/guides/partner' },
            ],
          },
          {
            text: 'CLI & Quick Setup',
            collapsed: false,
            items: [
              { text: 'Interactive Setup Wizard', link: '/integrations/cli/' },
              { text: 'Environment Preparation', link: '/integrations/cli/environment' },
              { text: 'Claude Code', link: '/integrations/cli/claude-code' },
              { text: 'Codex CLI', link: '/integrations/cli/codex' },
              { text: 'Kimi Code', link: '/integrations/cli/kimi-code' },
              { text: 'Grok Build CLI', link: '/integrations/cli/grok-build' },
              { text: 'Claude Code Cache Fix', link: '/integrations/cli/cache-fix' },
            ],
          },
          {
            text: 'Desktop, Agents & SDKs',
            collapsed: false,
            items: [
              { text: 'Overview', link: '/integrations/advanced/' },
              { text: 'OpenCode', link: '/integrations/advanced/opencode' },
              { text: 'OpenClaw', link: '/integrations/advanced/openclaw' },
              { text: 'Hermes Agent', link: '/integrations/advanced/hermes' },
              { text: 'Cherry Studio', link: '/integrations/advanced/cherry-studio' },
              { text: 'Claude Desktop', link: '/integrations/advanced/claude-desktop' },
              { text: 'WorkBuddy', link: '/integrations/advanced/workbuddy' },
              { text: 'AionUI', link: '/integrations/advanced/aionui' },
              { text: 'All API Hub', link: '/integrations/advanced/all-api-hub' },
              { text: 'Official SDKs', link: '/integrations/advanced/sdks' },
              { text: 'ChatGPT via Claude Code', link: '/integrations/advanced/chatgpt-claude-code' },
              { text: 'DeepSeek via Claude Code', link: '/integrations/advanced/deepseek-claude-code' },
              { text: 'DeepSeek via Codex', link: '/integrations/advanced/deepseek-codex' },
            ],
          },
          {
            text: 'CC Switch',
            collapsed: true,
            items: [
              { text: 'CC Switch Overview', link: '/integrations/cc-switch/' },
              { text: 'Common Configuration', link: '/integrations/cc-switch/common' },
              { text: 'Claude Code Setup', link: '/integrations/cc-switch/claude-code' },
              { text: 'Codex CLI Setup', link: '/integrations/cc-switch/codex' },
              { text: 'Codex Desktop App', link: '/integrations/cc-switch/codex-app' },
              { text: 'Claude Desktop Setup', link: '/integrations/cc-switch/claude-desktop' },
              { text: 'Usage & Balance Query', link: '/integrations/cc-switch/usage-query' },
              { text: 'CC Switch CLI', link: '/integrations/cc-switch/cli' },
            ],
          },
          {
            text: 'Image Generation',
            collapsed: true,
            items: [
              { text: 'Image Generation Overview', link: '/integrations/paint/' },
              { text: 'Gemini Image Generation', link: '/integrations/paint/banana' },
              { text: 'OpenAI Images API', link: '/integrations/paint/gpt-image' },
            ],
          },
          {
            text: 'API & Troubleshooting',
            collapsed: false,
            items: [
              { text: 'API Reference', link: '/api/' },
              { text: 'General FAQ', link: '/faq/' },
              { text: 'Claude Code FAQ', link: '/faq/claude-code' },
              { text: 'Codex FAQ', link: '/faq/codex' },
              { text: 'Gemini FAQ', link: '/faq/gemini' },
              { text: 'Legal & Policies', link: '/legal' },
            ],
          },
        ],
        outline: { label: 'On this page', level: [2, 3] },
      },
    },
    'zh-cn': {
      label: '简体中文',
      lang: 'zh-CN',
      link: '/zh-cn/',
      title: 'ADTY AI',
      description: 'ADTY AI 接口与客户端接入文档',
      themeConfig: {
        nav: [
          { text: '使用指南', link: '/zh-cn/guides/overview' },
          { text: '客户端接入', link: '/zh-cn/integrations/cli/' },
          { text: 'API 参考', link: '/zh-cn/api/' },
          { text: '常见问题', link: '/zh-cn/faq/' },
          { text: '分站合作', link: '/zh-cn/guides/partner' },
          { text: '进入控制台', link: 'https://www.adtyai.com' },
        ],
        sidebar: [
          {
            text: '平台使用指南',
            collapsed: false,
            items: [
              { text: '快速上手与双域名规则', link: '/zh-cn/guides/overview' },
              { text: '余额充值、赠金与兑换码', link: '/zh-cn/guides/wallet-and-vouchers' },
              { text: '账号安全与访问令牌', link: '/zh-cn/guides/security' },
              { text: '团队协作与共享钱包', link: '/zh-cn/guides/teams' },
              { text: '渠道可用性与分组选择', link: '/zh-cn/guides/channel-status' },
              { text: '推广返佣计划', link: '/zh-cn/guides/referral' },
              { text: '支持中心（工单）', link: '/zh-cn/guides/support' },
              { text: '分站合作介绍', link: '/zh-cn/guides/partner' },
            ],
          },
          {
            text: 'CLI 与快速开始',
            collapsed: false,
            items: [
              { text: '交互式配置向导', link: '/zh-cn/integrations/cli/' },
              { text: '基础环境准备', link: '/zh-cn/integrations/cli/environment' },
              { text: 'Claude Code', link: '/zh-cn/integrations/cli/claude-code' },
              { text: 'Codex CLI', link: '/zh-cn/integrations/cli/codex' },
              { text: 'Kimi Code', link: '/zh-cn/integrations/cli/kimi-code' },
              { text: 'Grok Build CLI', link: '/zh-cn/integrations/cli/grok-build' },
              { text: 'Claude Code 缓存命中修复', link: '/zh-cn/integrations/cli/cache-fix' },
            ],
          },
          {
            text: '进阶、桌面端与 SDK',
            collapsed: false,
            items: [
              { text: '客户端总览', link: '/zh-cn/integrations/advanced/' },
              { text: 'OpenCode', link: '/zh-cn/integrations/advanced/opencode' },
              { text: 'OpenClaw', link: '/zh-cn/integrations/advanced/openclaw' },
              { text: 'Hermes Agent', link: '/zh-cn/integrations/advanced/hermes' },
              { text: 'Cherry Studio', link: '/zh-cn/integrations/advanced/cherry-studio' },
              { text: 'Claude Desktop', link: '/zh-cn/integrations/advanced/claude-desktop' },
              { text: 'WorkBuddy', link: '/zh-cn/integrations/advanced/workbuddy' },
              { text: 'AionUI', link: '/zh-cn/integrations/advanced/aionui' },
              { text: 'All API Hub', link: '/zh-cn/integrations/advanced/all-api-hub' },
              { text: '官方 SDK 接入', link: '/zh-cn/integrations/advanced/sdks' },
              { text: 'Claude Code 调用 ChatGPT', link: '/zh-cn/integrations/advanced/chatgpt-claude-code' },
              { text: 'Claude Code 调用 DeepSeek', link: '/zh-cn/integrations/advanced/deepseek-claude-code' },
              { text: 'Codex 调用 DeepSeek', link: '/zh-cn/integrations/advanced/deepseek-codex' },
            ],
          },
          {
            text: 'CC Switch 专题',
            collapsed: true,
            items: [
              { text: 'CC Switch 简介与安装', link: '/zh-cn/integrations/cc-switch/' },
              { text: '通用基础配置', link: '/zh-cn/integrations/cc-switch/common' },
              { text: '配置 Claude Code', link: '/zh-cn/integrations/cc-switch/claude-code' },
              { text: '配置 Codex CLI', link: '/zh-cn/integrations/cc-switch/codex' },
              { text: '配置 Codex 桌面应用', link: '/zh-cn/integrations/cc-switch/codex-app' },
              { text: '配置 Claude Desktop', link: '/zh-cn/integrations/cc-switch/claude-desktop' },
              { text: '额度与余额查询', link: '/zh-cn/integrations/cc-switch/usage-query' },
              { text: 'CC Switch CLI 工具', link: '/zh-cn/integrations/cc-switch/cli' },
            ],
          },
          {
            text: '图像生成',
            collapsed: true,
            items: [
              { text: '图像生成概览', link: '/zh-cn/integrations/paint/' },
              { text: 'Gemini 图像生成', link: '/zh-cn/integrations/paint/banana' },
              { text: 'OpenAI Images 绘图', link: '/zh-cn/integrations/paint/gpt-image' },
            ],
          },
          {
            text: 'API 与问题排查',
            collapsed: false,
            items: [
              { text: 'API 参考', link: '/zh-cn/api/' },
              { text: '通用常见问题', link: '/zh-cn/faq/' },
              { text: 'Claude Code 排查', link: '/zh-cn/faq/claude-code' },
              { text: 'Codex 排查', link: '/zh-cn/faq/codex' },
              { text: 'Gemini 排查', link: '/zh-cn/faq/gemini' },
              { text: '法律与条款', link: '/zh-cn/legal' },
            ],
          },
        ],
        outline: { label: '本页目录', level: [2, 3] },
        returnToTopLabel: '回到顶部',
        sidebarMenuLabel: '菜单',
        darkModeSwitchLabel: '主题',
        docFooter: {
          prev: '上一页',
          next: '下一页',
        },
      },
    },
  },

  themeConfig: {
    search: {
      provider: 'local',
    },
  },
})
