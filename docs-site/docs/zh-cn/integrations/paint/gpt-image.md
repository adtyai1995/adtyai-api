# OpenAI Images 绘图

**ADTY AI** 在 `https://api.adtyai.com/v1` 提供标准 OpenAI Images 接口（`POST /v1/images/generations` 与 `POST /v1/images/edits`），可直接配合官方 `openai` SDK 或 `curl` 调用 `gpt-image-1` 以及定价页中其他 OpenAI 兼容绘图模型。

::: warning 务必指向 `https://api.adtyai.com/v1`
所有 OpenAI Images 请求均须发往 `https://api.adtyai.com/v1`。切勿填写网页控制台域名 `https://www.adtyai.com`，否则接口会直接返回 **HTTP 403**。
:::

## 前置准备

1. 在 [https://www.adtyai.com](https://www.adtyai.com) 的 **令牌（API Tokens）** 页面创建一把支持 OpenAI 绘图模型分组的 API 密钥（`sk-...`）。
2. 在定价页 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 核对实时可用的图像模型名称（例如 `gpt-image-1`、`gpt-image-1.5` 等）及单价。

## 核心参数说明（以 `gpt-image-1` 为例）

| 参数 | 可选值 | 说明 |
| --- | --- | --- |
| `model` | `"gpt-image-1"`（或定价页中的其他绘图模型） | 调用前请在 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 确认实时名称。 |
| `prompt` | 字符串 | 描述画面主体、构图、光线与文字细节。 |
| `size` | `"1024x1024"`、`"1536x1024"`（横图）、`"1024x1536"`（竖图）或 `"auto"` | 省略时默认为 `"auto"`。 |
| `quality` | `"low"`、`"medium"`、`"high"` 或 `"auto"` | 清晰度越高细节与文字越锐利，耗时与计费也会相应上升。 |
| `n` | 整数（通常 `1`～`4`） | 单次请求生成的图片数量。 |
| `background` | `"transparent"`、`"opaque"` 或 `"auto"` | 生成透明底 Logo、图标或贴纸时设为 `"transparent"`（需配合 `png` 或 `webp`）。 |
| `output_format` | `"png"`、`"jpeg"` 或 `"webp"` | 默认 `"png"`；选择 `"webp"` 或 `"jpeg"` 时还可配合 `output_compression`（`0`～`100`）压缩体积。 |

::: info `gpt-image-1` 默认返回 `b64_json`
与早期 DALL-E 接口不同，`gpt-image-1` 固定在 `data[i].b64_json` 中返回 Base64 图像数据，**不接受** `response_format="url"` 参数；强行传入 `response_format` 可能导致 `400 Bad Request`。
:::

## 1. 文生图（`/v1/images/generations`）

### Python（`openai` SDK）

先安装官方 SDK：

```bash
pip install -U openai
```

调用生图接口并将 `b64_json` 直接解码保存为 PNG：

```python
import base64
from pathlib import Path
from openai import OpenAI

client = OpenAI(
    base_url="https://api.adtyai.com/v1",
    api_key="sk-your-api-key",
    timeout=120.0,
)

result = client.images.generate(
    model="gpt-image-1",
    prompt=(
        "一张俯拍杂志风格照片：阳极氧化铝合金机械键盘旁放着笔记本与钢笔，柔和棚拍光线。"
    ),
    size="1536x1024",
    quality="high",
    n=1,
    background="opaque",
    output_format="png",
)

out_dir = Path("outputs")
out_dir.mkdir(exist_ok=True)

for idx, item in enumerate(result.data, start=1):
    image_bytes = base64.b64decode(item.b64_json)
    out_path = out_dir / f"gpt_image_{idx}.png"
    out_path.write_bytes(image_bytes)
    print(f"已保存 {out_path}（{len(image_bytes):,} 字节）")
```

### 原生 HTTP（`curl`）

```bash
curl -sS https://api.adtyai.com/v1/images/generations \
  -H "Authorization: Bearer sk-your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-image-1",
    "prompt": "极简几何风格的海上升旭日徽章图标，透明背景，干净现代矢量风",
    "size": "1024x1024",
    "quality": "medium",
    "n": 1,
    "background": "transparent",
    "output_format": "png"
  }' > image_resp.json
```

将 `image_resp.json` 中的 `b64_json` 解码为 `output.png`：

```bash
python3 -c '
import base64, json
payload = json.load(open("image_resp.json"))
if "error" in payload:
    raise SystemExit(f"接口报错: {payload[\"error\"]}")
img_bytes = base64.b64decode(payload["data"][0]["b64_json"])
open("output.png", "wb").write(img_bytes)
print(f"已保存 output.png（{len(img_bytes):,} 字节）")
'
```

## 2. 图像编辑与局部重绘（`/v1/images/edits`）

通过 `POST https://api.adtyai.com/v1/images/edits`（`multipart/form-data`）上传原图即可按提示词改图；需要限定修改区域时，还可额外传入带透明通道的 `mask` PNG 蒙版。

### Python（`openai` SDK）

```python
import base64
from openai import OpenAI

client = OpenAI(
    base_url="https://api.adtyai.com/v1",
    api_key="sk-your-api-key",
    timeout=120.0,
)

with open("source.png", "rb") as img_file:
    result = client.images.edit(
        model="gpt-image-1",
        image=img_file,
        prompt="保持前景主体不变，将背景替换为温暖阳光洒入的木质书房。",
        size="1024x1024",
        quality="high",
    )

with open("edited.png", "wb") as f:
    f.write(base64.b64decode(result.data[0].b64_json))
print("已保存 edited.png")
```

### 原生 HTTP（`curl`）

```bash
curl -sS https://api.adtyai.com/v1/images/edits \
  -H "Authorization: Bearer sk-your-api-key" \
  -F "model=gpt-image-1" \
  -F "image[]=@source.png" \
  -F "prompt=将天空替换为戏剧化的金色黄昏晚霞" \
  -F "size=1024x1024" \
  -F "quality=medium" > edit_resp.json
```

## 3. 错误处理与健壮性建议

图像生成耗时明显高于普通文本对话，且参数校验较严格，建议在客户端明确设置较长超时并区分异常类型：

```python
from openai import APIError, APITimeoutError, BadRequestError, OpenAI, RateLimitError

client = OpenAI(
    base_url="https://api.adtyai.com/v1",
    api_key="sk-your-api-key",
    timeout=150.0,
    max_retries=2,
)

try:
    resp = client.images.generate(
        model="gpt-image-1",
        prompt="干净的等轴测云计算数据中心插画",
        size="1024x1024",
        quality="medium",
    )
except BadRequestError as exc:
    # 参数组合非法（如传入不支持的 size / response_format）或触发内容安全审核
    print("请求参数被拒 (400):", exc.message)
except RateLimitError as exc:
    # 建议配合随机退避延迟重试
    print("触发限流 (429):", exc.message)
except APITimeoutError:
    # high 质量或多图并发时建议设置 120-180 秒超时
    print("等待图像生成超时")
except APIError as exc:
    print(f"上游服务异常 ({exc.status_code}):", exc.message)
```

### 常见报错速查

- **HTTP 400（参数错误）**：调用 `gpt-image-1` 时不要传 `response_format`；确认 `size` 属于 `1024x1024`、`1536x1024`、`1024x1536`、`auto` 之一；使用 `background="transparent"` 时需搭配 `png` 或 `webp`（不可用 `jpeg`）。
- **HTTP 400（内容安全拦截）**：上游审核拒绝了提示词，请调整表述后重试。
- **HTTP 403 Forbidden**：检查 `base_url` 是否为 `https://api.adtyai.com/v1`，不可误填网页控制台域名 `https://www.adtyai.com`。
- **HTTP 401 / 余额不足**：在 [https://www.adtyai.com](https://www.adtyai.com) 检查密钥状态与余额，详见 [余额充值、赠金与兑换码](/zh-cn/guides/wallet-and-vouchers)。

## 相关阅读

- [图像生成概览](/zh-cn/integrations/paint/)
- [Gemini 图像生成](/zh-cn/integrations/paint/banana)
- [官方 SDK 接入](/zh-cn/integrations/advanced/sdks)
- [API 参考](/zh-cn/api/)
