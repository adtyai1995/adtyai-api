# Gemini 图像生成

Gemini 原生多模态绘图模型（例如 `gemini-2.5-flash-image`、`gemini-3-pro-image-preview`，请以 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 实时展示的模型 ID 为准）可通过 **ADTY AI** 的原生 `:generateContent` 接口直接完成文生图与对话式图生图编辑。

::: warning 接口基地址为 `https://api.adtyai.com`（不带 `/v1`）
Gemini 原生接口路径为 `/v1beta/models/{model}:generateContent`：
- **正确 SDK `base_url`**：`https://api.adtyai.com`
- **错误**：`https://www.adtyai.com`（网页控制台域名会直接返回 **HTTP 403**）
:::

## 前置准备

1. 在 [https://www.adtyai.com](https://www.adtyai.com) 的 **令牌（API Tokens）** 页面创建一把支持 Gemini 图像模型分组的 API 密钥（`sk-...`）。
2. 在定价页 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 核对当前启用的准确模型名（如偏向快速生成的 `gemini-2.5-flash-image` 或更高质量的 `gemini-3-pro-image-preview`）。

## 1. Python SDK（`google-genai`）文生图示例

先安装官方 `google-genai` SDK 与 `pillow`：

```bash
pip install -U google-genai pillow
```

通过 `HttpOptions(base_url="https://api.adtyai.com")` 指向 **ADTY AI**，设置 `response_modalities=["TEXT", "IMAGE"]`，并将返回的图片字节保存到本地：

```python
from io import BytesIO
from pathlib import Path
from PIL import Image
from google import genai
from google.genai import types

client = genai.Client(
    api_key="sk-your-api-key",
    http_options=types.HttpOptions(base_url="https://api.adtyai.com"),
)

response = client.models.generate_content(
    model="gemini-2.5-flash-image",
    contents=(
        "清晨薄雾中的极简等轴测玻璃温室工作室插画，温暖日出光线，细腻清晰的矢量质感。"
    ),
    config=types.GenerateContentConfig(
        response_modalities=["TEXT", "IMAGE"],
    ),
)

out_dir = Path("outputs")
out_dir.mkdir(exist_ok=True)

image_count = 0
for part in response.candidates[0].content.parts:
    if part.text:
        print("模型说明:", part.text)
    elif part.inline_data:
        image_count += 1
        img = Image.open(BytesIO(part.inline_data.data))
        out_path = out_dir / f"gemini_{image_count}.png"
        img.save(out_path)
        print(f"已保存 {out_path} ({img.size[0]}x{img.size[1]})")
```

::: tip 画幅比例与清晰度参数
支持的 Gemini 绘图模型可在 `GenerateContentConfig` 中传入 `image_config`（如 `"16:9"`、`"1:1"`、`"3:4"` 等画幅比例或高分辨率参数）。具体可用参数请结合 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 与 [Gemini 排查](/zh-cn/faq/gemini) 确认。
:::

## 2. 原生 HTTP（`curl`）文生图示例

直接使用 `curl` 调用 `/v1beta/models/{model}:generateContent`，通过 `x-goog-api-key` 请求头鉴权（也兼容 `Authorization: Bearer`）：

```bash
curl -sS "https://api.adtyai.com/v1beta/models/gemini-2.5-flash-image:generateContent" \
  -H "Content-Type: application/json" \
  -H "x-goog-api-key: sk-your-api-key" \
  -d '{
    "contents": [
      {
        "role": "user",
        "parts": [
          {
            "text": "一张极简棚拍产品照：浅色橡木桌上的哑光陶瓷手冲咖啡滤杯，柔和自然漫射光。"
          }
        ]
      }
    ],
    "generationConfig": {
      "responseModalities": ["TEXT", "IMAGE"]
    }
  }' > response.json
```

从 `response.json` 解码并保存生成的 PNG 图片：

```bash
python3 -c '
import base64, json
data = json.load(open("response.json"))
for idx, part in enumerate(data["candidates"][0]["content"]["parts"], 1):
    inline = part.get("inlineData") or part.get("inline_data")
    if inline and inline.get("data"):
        with open(f"output_{idx}.png", "wb") as f:
            f.write(base64.b64decode(inline["data"]))
        print(f"已保存 output_{idx}.png")
'
```

## 3. 图生图与自然语言改图（Inline Base64）

在同一条请求的 `parts` 数组中同时传入原图与修改指令，即可完成图生图与局部编辑。

### Python（`google-genai`）

```python
from io import BytesIO
from pathlib import Path
from PIL import Image
from google import genai
from google.genai import types

client = genai.Client(
    api_key="sk-your-api-key",
    http_options=types.HttpOptions(base_url="https://api.adtyai.com"),
)

src_bytes = Path("input.png").read_bytes()

response = client.models.generate_content(
    model="gemini-2.5-flash-image",
    contents=[
        types.Part.from_text(
            text="保持咖啡滤杯主体不变，将桌面替换为深色石板材质，并增加柔和的晨间热气。"
        ),
        types.Part.from_bytes(
            data=src_bytes,
            mime_type="image/png",
        ),
    ],
    config=types.GenerateContentConfig(
        response_modalities=["TEXT", "IMAGE"],
    ),
)

for idx, part in enumerate(response.candidates[0].content.parts, 1):
    if part.inline_data:
        Image.open(BytesIO(part.inline_data.data)).save(f"edited_{idx}.png")
        print(f"已保存 edited_{idx}.png")
```

### 原生 HTTP（`curl`）传入 Base64 `inlineData`

```bash
IMG_B64=$(base64 -w 0 input.png 2>/dev/null || base64 -i input.png | tr -d '\n')

curl -sS "https://api.adtyai.com/v1beta/models/gemini-2.5-flash-image:generateContent" \
  -H "Content-Type: application/json" \
  -H "x-goog-api-key: sk-your-api-key" \
  -d "{
    \"contents\": [
      {
        \"role\": \"user\",
        \"parts\": [
          { \"text\": \"将这张线稿转化为精致的水彩建筑表现图。\" },
          {
            \"inlineData\": {
              \"mimeType\": \"image/png\",
              \"data\": \"${IMG_B64}\"
            }
          }
        ]
      }
    ],
    \"generationConfig\": {
      \"responseModalities\": [\"TEXT\", \"IMAGE\"]
    }
  }" > edited_response.json
```

## 常见问题排查

- **只返回文字没有图片**：确认 `responseModalities` 已包含 `"IMAGE"`，且所调用的是支持图像输出的模型（如 `gemini-2.5-flash-image` 或 `gemini-3-pro-image-preview`），而非纯文本 Gemini 模型。
- **返回 HTTP 403 Forbidden**：检查 `base_url` 是否为 `https://api.adtyai.com`（不可填写 `https://www.adtyai.com`）。
- **返回 404 或模型不可用**：前往 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 核对准确模型名，并确认当前令牌所属分组包含该模型。
- **高分辨率生图超时**：建议调大客户端超时时间（例如在 `HttpOptions` 中设置 `timeout=120_000` 毫秒）。
- 更多排查项请阅读 [Gemini 排查](/zh-cn/faq/gemini) 或返回 [图像生成概览](/zh-cn/integrations/paint/)。
