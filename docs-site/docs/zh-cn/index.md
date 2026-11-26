---
layout: home

hero:
  name: ADTY AI
  text: 多协议统一大模型接口网关
  tagline: 一个 API Key 与美元余额钱包，畅连 OpenAI、Anthropic 与 Gemini 主流模型——双域名隔离架构、团队协作与实时渠道监测一步到位。
  actions:
    - theme: brand
      text: 快速上手指南
      link: /zh-cn/guides/overview
    - theme: alt
      text: 交互式客户端接入
      link: /zh-cn/integrations/cli/
    - theme: alt
      text: API 参考文档
      link: /zh-cn/api/

features:
  - title: 多协议统一网关
    details: 同时兼容 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、OpenAI Images 与 Gemini 原生协议，一把密钥全协议通用。
    link: /zh-cn/api/
    linkText: 查看全部接口
  - title: 双域名隔离架构
    details: 网页控制台（https://www.adtyai.com）与模型接口网关（https://api.adtyai.com）职责分离，保障高并发流式输出与低延迟稳定性。
    link: /zh-cn/guides/overview
    linkText: 了解双域名规则
  - title: 交互式客户端配置
    details: 一键生成 Claude Code、Codex CLI、Kimi Code、OpenCode、Cherry Studio 及主流官方 SDK 配置片段，自动带入当前站点接口地址。
    link: /zh-cn/integrations/cli/
    linkText: 进入配置向导
  - title: 团队协作与共享钱包
    details: 团队所有者统一充值美元共享钱包，支持邀请团队成员与创建独立团队员工子账号，发放团队专属 API Key 并统一审计用量。
    link: /zh-cn/guides/teams
    linkText: 查看团队指南
  - title: 实时渠道状态监测
    details: 创建 API Key 前可查看各具体模型分组的 24 小时心跳柱、响应延迟与成功率，按实际业务场景按需选线。
    link: /zh-cn/guides/channel-status
    linkText: 查看渠道可用性
  - title: 账号安全与双因素认证
    details: 支持 TOTP 双因素认证、离线恢复码、高危操作二次验证、活跃会话管理与细粒度个人访问令牌（PAT）。
    link: /zh-cn/guides/security
    linkText: 了解安全能力
---

<div class="adty-home-wizard"><SetupWizard /></div>
