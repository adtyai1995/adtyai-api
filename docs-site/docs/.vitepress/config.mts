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
  description: 'ADTY AI API Documentation',

  locales: {
    root: {
      label: 'English',
      lang: 'en-US',
      title: 'ADTY AI',
      description: 'ADTY AI API Documentation',
      themeConfig: {
        nav: [
          { text: 'Docs', link: '/' },
          { text: 'Console', link: 'https://www.adtyai.com' },
        ],
        sidebar: [
          {
            text: 'Documentation',
            items: [{ text: 'Overview', link: '/' }],
          },
        ],
        outline: { label: 'On this page' },
      },
    },
    'zh-cn': {
      label: '简体中文',
      lang: 'zh-CN',
      link: '/zh-cn/',
      title: 'ADTY AI',
      description: 'ADTY AI 接口文档',
      themeConfig: {
        nav: [
          { text: '文档', link: '/zh-cn/' },
          { text: '控制台', link: 'https://www.adtyai.com' },
        ],
        sidebar: [
          {
            text: '使用文档',
            items: [{ text: '概览', link: '/zh-cn/' }],
          },
        ],
        outline: { label: '本页目录' },
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
