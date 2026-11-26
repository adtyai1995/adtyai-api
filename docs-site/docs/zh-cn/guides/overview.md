# 快速上手与双域名规则

几分钟内完成 ADTY AI 账号注册、钱包充值、创建 API Key，并跑通你的第一条大模型接口请求。

## 四步快速接入

1. **注册并登录控制台**
   访问 [https://www.adtyai.com](https://www.adtyai.com) 完成注册或登录。建议尽早前往[账号安全与访问令牌](/zh-cn/guides/security)开启双因素认证（2FA）。
2. **为钱包充值余额**
   进入控制台**钱包**页面，通过当前站点支持的在线支付方式充值美元余额，或直接兑换额度兑换码。详见[余额充值、赠金与兑换码](/zh-cn/guides/wallet-and-vouchers)。
3. **选择具体模型分组并创建 API Key**
   先在控制台**价格**页面与[渠道可用性](/zh-cn/guides/channel-status)确认所需模型对应的具体分组、计费倍率及稳定性，再前往**令牌 / API Key** 页面新建密钥，绑定该具体模型分组并复制生成的 `sk-...` 密钥。
4. **将客户端指向专用 API 域名**
   在 CLI 工具、桌面客户端或 SDK 中将接口地址配置为 `https://api.adtyai.com`（是否追加 `/v1` 取决于所用协议，详见下方速查表与[API 参考](/zh-cn/api/)）。

## 双域名分离架构

ADTY AI 将网页控制台域名与模型接口网关域名明确分离，各自承担不同流量职责：

| 域名 | 核心定位 | 模型 API 调用（`/v1/*`、`/v1beta/*`） | 控制台网页访问 |
| :--- | :--- | :--- | :--- |
| `https://www.adtyai.com` | 网页控制台与文档中心 | **拒绝访问，返回 HTTP `403`**（`api_domain_required`） | 支持 |
| `https://api.adtyai.com` | 专用大模型 API 网关 | **完整支持**（OpenAI / Anthropic / Gemini 全协议） | 同样支持 |

::: danger 为什么 `www` 域名会拦截 API 请求？
向 `https://www.adtyai.com/v1/...` 或 `https://www.adtyai.com/v1beta/...` 发送模型调用请求时，网关会直接返回 HTTP `403` 及错误码 `api_domain_required`。

将 API 流量与网页前端、静态资源及加速策略隔离，可显著提升长连接 SSE 流式响应的稳定性。配置任何客户端或代码时，请务必填写 `https://api.adtyai.com`。
:::

### 常见客户端 Base URL 速查

不同客户端对路径后缀的约定略有区别：

- **OpenAI 兼容客户端、SDK 与 Codex CLI**：`https://api.adtyai.com/v1`
- **Anthropic SDK 与 Claude Code**：`https://api.adtyai.com` *（请勿自行加 `/v1`，客户端会自动拼接 `/v1/messages`）*
- **Gemini 原生 SDK（`google-genai`）**：`https://api.adtyai.com` *（SDK 会自动拼接 `/v1beta/models/...`）*

## 使用 `curl` 快速连通性测试

将命令中的 `<YOUR_API_KEY>` 替换为你刚创建的密钥，测试 `https://api.adtyai.com/v1/chat/completions` 是否通畅。

### macOS / Linux

```bash
curl -sS https://api.adtyai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -d '{
    "model": "gpt-5.4",
    "messages": [
      { "role": "user", "content": "请只回复 OK 两个字母。" }
    ],
    "max_tokens": 32
  }'
```

### Windows PowerShell

```powershell
curl.exe -sS https://api.adtyai.com/v1/chat/completions `
  -H "Content-Type: application/json" `
  -H "Authorization: Bearer <YOUR_API_KEY>" `
  -d "{\"model\":\"gpt-5.4\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with OK only.\"}],\"max_tokens\":32}"
```

正常情况下将返回 HTTP `200` 及包含回复文本与 `usage` 用量统计的 JSON：

```json
{
  "id": "chatcmpl-01J9EXAMPLE",
  "object": "chat.completion",
  "model": "gpt-5.4",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "OK"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 14,
    "completion_tokens": 2,
    "total_tokens": 16
  }
}
```

::: tip 选择测试模型的小提示
请确保请求体里的 `model` 属于该 API Key 所绑定具体模型分组内已开放的模型。你可以先使用同一个 `Authorization: Bearer` 请求头调用 `GET https://api.adtyai.com/v1/models` 查看当前密钥可调用的全部模型 ID。
:::

## 下一步

- 前往[交互式配置向导](/zh-cn/integrations/cli/)一键生成各类主流工具的配置。
- 阅读[API 参考](/zh-cn/api/)查看全部 6 类协议接口与状态码说明。
- 遇到报错可先查阅[通用常见问题](/zh-cn/faq/)。
