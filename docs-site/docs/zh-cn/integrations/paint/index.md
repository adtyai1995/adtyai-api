# 图像生成概览

**ADTY AI** 同时支持 **Gemini 原生图像生成** 与 **OpenAI Images API**，使用同一把 API Key 和账户余额即可按需调用。你可以根据业务需求在“多轮对话式精修（Gemini 原生）”与“标准 OpenAI 绘图生态（`/v1/images/generations`）”之间自由选择。

::: warning 务必使用专用 API 域名
所有图像生成接口请求均须发送至 `https://api.adtyai.com`。切勿请求网页控制台域名 `https://www.adtyai.com`，否则会直接返回 **HTTP 403**。
:::

## 两种绘图协议对比

| 对比维度 | Gemini 原生图像生成 | OpenAI Images API |
| --- | --- | --- |
| **请求端点** | `POST https://api.adtyai.com/v1beta/models/{model}:generateContent` | `POST https://api.adtyai.com/v1/images/generations` 与 `/v1/images/edits` |
| **常见模型示例** | `gemini-2.5-flash-image`、`gemini-3-pro-image-preview`（*以定价页实时列表为准*） | `gpt-image-1`（*以及定价页中列出的其他绘图模型*） |
| **核心优势** | 原生多模态图文混排、自然语言多轮改图、图生图局部编辑与高精度文字渲染 | 与现有 OpenAI SDK 无缝兼容、支持透明背景、尺寸/清晰度档位与批量 `n` 产出 |
| **推荐 SDK** | Python `google-genai`（`base_url="https://api.adtyai.com"`） | Python / Node `openai`（`base_url="https://api.adtyai.com/v1"`） |
| **返回载体** | `candidates[].content.parts[].inlineData` 中的 Base64 数据 | `b64_json`（`gpt-image-1` 默认）或临时 `url`（视模型而定） |
| **详细教程** | [Gemini 图像生成](/zh-cn/integrations/paint/banana) | [OpenAI Images 绘图](/zh-cn/integrations/paint/gpt-image) |

## 先在定价页确认实时可用模型

不同令牌分组支持的绘图模型、分辨率及计费方式（按张或按 Token 计费）会随上游渠道动态更新。写入业务代码前建议先确认：

1. 访问定价页 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing)。
2. 搜索 `image`、`gemini` 或 `gpt-image` 查看当前在线的准确模型名称。
3. 确认你在 [https://www.adtyai.com](https://www.adtyai.com) 创建的 API 密钥已归属支持对应模型的分组（详见 [渠道可用性与分组选择](/zh-cn/guides/channel-status)）。

## 处理 `base64` 与 `url` 两种返回格式

接入生产系统时，建议针对两种返回形态做好统一封装：

- **Base64 内联数据（`inlineData.data` 或 `b64_json`）**：
  - 图像字节以 Base64 字符串直接嵌入 JSON 响应体中。
  - **优点**：自包含、无链接过期风险、无需二次拉取外链图片。
  - **实践建议**：收到响应后立即解码并写入本地磁盘或对象存储（OSS / S3 / R2），切勿把整段 Base64 原文打印到应用日志中。
- **临时链接（`data[].url`）**：
  - 部分传统 OpenAI 兼容绘图模型在指定 `response_format="url"` 时会返回短期签名地址。
  - **实践建议**：生成后立即下载转存至自有存储，避免上游签名 URL 过期导致前端图片失效。

## 深入阅读

- 使用 Gemini 原生多模态生图与改图：[Gemini 图像生成](/zh-cn/integrations/paint/banana)
- 使用 OpenAI 标准接口（`gpt-image-1`）绘图与编辑：[OpenAI Images 绘图](/zh-cn/integrations/paint/gpt-image)
- 查看完整协议规范：[API 参考](/zh-cn/api/)
- 遇到模型报错或参数问题：[Gemini 排查](/zh-cn/faq/gemini) 与 [通用常见问题](/zh-cn/faq/)
