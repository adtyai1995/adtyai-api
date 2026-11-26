# All API Hub

All API Hub 是一款用于集中管理多个 API 平台账号的浏览器插件与面板工具，可帮助你一站式查看实时余额、管理 API 密钥、检索可用模型，并将配置一键导出到各类主流客户端。

## 重要说明：ADTY AI 采用双域名架构

在将 **ADTY AI** 添加到 All API Hub 前，请先明确两个域名的分工（详细说明见 [快速上手与双域名规则](/zh-cn/guides/overview)）：

| 用途 | 规范地址 | 作用说明 |
| --- | --- | --- |
| **站点 / 控制台地址（Site URL）** | `https://www.adtyai.com` | 网页登录、控制台数据同步、钱包余额查询、令牌管理与价格查询 |
| **API 基础地址（API Base URL）** | `https://api.adtyai.com` | 实际发起模型接口调用（`/v1/models`、`/v1/chat/completions`、`/v1/messages`）及导出到下游客户端的接口地址 |

::: warning 请勿混填两个域名
- 将 `https://api.adtyai.com` 填作 **站点地址** 会导致插件无法同步控制台账号信息，因为网页控制台位于 `https://www.adtyai.com`。
- 将 `https://www.adtyai.com` 当作 **API Base URL** 导出到客户端会导致模型请求失败，所有 API 调用必须指向 `https://api.adtyai.com`。
:::

## 1. 在 All API Hub 中添加 ADTY AI

1. 若希望通过浏览器登录态自动识别账号，请先在当前浏览器中登录 [https://www.adtyai.com](https://www.adtyai.com)。
2. 打开 **All API Hub** 插件，点击 **添加站点 / 添加账号**。
3. 在 **站点地址（Site URL）** 中填写：

   ```text
   https://www.adtyai.com
   ```

4. 选择任一种鉴权方式完成绑定：
   - **自动检测（浏览器会话）**：读取你在 `https://www.adtyai.com` 的登录态，自动识别用户 ID 并同步账号状态。
   - **手动填写访问令牌（PAT）**：输入你的 **User ID** 与控制台生成的系统访问令牌（PAT）。创建与保管规范请参考 [账号安全与访问令牌](/zh-cn/guides/security)。
5. 在该账号的高级设置或接口地址项中，将 **API Base URL**（自定义 API 地址）显式设置为：

   ```text
   https://api.adtyai.com
   ```

6. 保存后点击刷新，确认账号余额、分组与令牌列表均能正常加载。

## 2. 管理 API 密钥与查看模型列表

接入完成后，你可以在插件面板内直接完成日常高频操作：

- **管理密钥**：查看所有已创建的 API 密钥、一键复制密钥、查看单个密钥的已用额度与所属分组。
- **检索模型**：按密钥查看可用模型清单，并与 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 核对倍率与分组覆盖范围。
- **余额监控**：在浏览器侧随时掌握账号余额变化；如需充值、使用兑换码或查看赠金明细，请回到 [https://www.adtyai.com](https://www.adtyai.com) 操作（参考 [余额充值、赠金与兑换码](/zh-cn/guides/wallet-and-vouchers)）。

## 3. 一键导出配置到常用客户端

All API Hub 支持将选定的 API 密钥与接口地址一键导出至 **Cherry Studio**、**Claude Code**、**Codex CLI**、**OpenCode**、**CC Switch** 等客户端。

导出后建议快速核对目标客户端里的两项关键参数：

1. **接口地址必须是 `https://api.adtyai.com`**（而不是 `https://www.adtyai.com`），并符合目标客户端对 `/v1` 的要求：
   - OpenAI 兼容 SDK、Codex、OpenCode：`https://api.adtyai.com/v1`
   - Cherry Studio、Claude Code、Claude Desktop、Anthropic SDK、Gemini SDK：`https://api.adtyai.com`
2. **模型 ID 与该密钥所属分组一致**，以 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 为准。

## 安全使用建议

- **区分访问令牌（PAT）与 API 密钥**：PAT 具备账号级管理权限，切勿将 PAT 填进第三方聊天或编程客户端；向下游客户端导出时始终使用普通 `sk-...` API 密钥。
- **按应用拆分密钥**：为每个导出的客户端创建独立密钥并设定额度上限，即使某端泄露也可单独吊销而不影响其他工具。详见 [账号安全与访问令牌](/zh-cn/guides/security)。
