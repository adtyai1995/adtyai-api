# OpenAI Images API

**ADTY AI** implements the standard OpenAI Images endpoints (`POST /v1/images/generations` and `POST /v1/images/edits`) at `https://api.adtyai.com/v1`, allowing you to generate and edit images with `gpt-image-1` and other enabled OpenAI-compatible image models using the official `openai` SDK or `curl`.

::: warning Always Use `https://api.adtyai.com/v1`
Send OpenAI Images API requests to `https://api.adtyai.com/v1`. Never target the web console domain `https://www.adtyai.com`, which returns **HTTP 403**.
:::

## Prerequisites

1. Create an **ADTY AI API Key** (`sk-...`) at [https://www.adtyai.com](https://www.adtyai.com) under **API Tokens** in a group supporting OpenAI image models.
2. Check [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) for exact live image model names (such as `gpt-image-1`, `gpt-image-1.5`, or other supported image endpoints) and billing rates.

## Key Request Parameters (`gpt-image-1`)

| Parameter | Supported Values | Notes |
| --- | --- | --- |
| `model` | `"gpt-image-1"` *(or live model ID from Pricing)* | Verify active availability on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing). |
| `prompt` | Text string | Detailed scene, composition, lighting, and typography instructions. |
| `size` | `"1024x1024"`, `"1536x1024"` (landscape), `"1024x1536"` (portrait), or `"auto"` | Defaults to `"auto"` when omitted. |
| `quality` | `"low"`, `"medium"`, `"high"`, or `"auto"` | Higher quality produces sharper textures and text at higher cost and latency. |
| `n` | Integer (typically `1`–`4`) | Number of images to generate per call. |
| `background` | `"transparent"`, `"opaque"`, or `"auto"` | Use `"transparent"` for logos, icons, and UI stickers (requires `png` or `webp`). |
| `output_format` | `"png"`, `"jpeg"`, or `"webp"` | Defaults to `"png"`. Combine `"webp"` or `"jpeg"` with `output_compression` (`0`–`100`) to reduce payload size. |

::: info `gpt-image-1` Always Returns `b64_json`
Unlike older DALL-E endpoints, `gpt-image-1` returns base64-encoded image data in `data[i].b64_json` and does **not** accept `response_format="url"`. Passing `response_format` to `gpt-image-1` may trigger a `400 Bad Request` parameter error.
:::

## 1. Text-to-Image Generation (`/v1/images/generations`)

### Python (`openai` SDK)

Install the official SDK:

```bash
pip install -U openai
```

Generate an image and decode `b64_json` directly to a PNG file:

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
        "A crisp flat-lay editorial photograph of an anodized aluminum mechanical "
        "keyboard next to a notebook and fountain pen, soft studio lighting."
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
    print(f"Saved {out_path} ({len(image_bytes):,} bytes)")
```

### Raw HTTP (`curl`)

```bash
curl -sS https://api.adtyai.com/v1/images/generations \
  -H "Authorization: Bearer sk-your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-image-1",
    "prompt": "Minimalist geometric badge icon of a rising sun over ocean waves, transparent background, clean modern vector style",
    "size": "1024x1024",
    "quality": "medium",
    "n": 1,
    "background": "transparent",
    "output_format": "png"
  }' > image_resp.json
```

Decode `b64_json` from `image_resp.json` to `output.png`:

```bash
python3 -c '
import base64, json
payload = json.load(open("image_resp.json"))
if "error" in payload:
    raise SystemExit(f"API error: {payload[\"error\"]}")
img_bytes = base64.b64decode(payload["data"][0]["b64_json"])
open("output.png", "wb").write(img_bytes)
print(f"Saved output.png ({len(img_bytes):,} bytes)")
'
```

## 2. Image Editing & Variations (`/v1/images/edits`)

Use `POST https://api.adtyai.com/v1/images/edits` with `multipart/form-data` to edit an existing image—optionally passing a transparent PNG `mask` to restrict edits to a specific region.

### Python (`openai` SDK)

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
        prompt="Change the background to a warm sunlit library while keeping the foreground object unchanged.",
        size="1024x1024",
        quality="high",
    )

with open("edited.png", "wb") as f:
    f.write(base64.b64decode(result.data[0].b64_json))
print("Saved edited.png")
```

### Raw HTTP (`curl`)

```bash
curl -sS https://api.adtyai.com/v1/images/edits \
  -H "Authorization: Bearer sk-your-api-key" \
  -F "model=gpt-image-1" \
  -F "image[]=@source.png" \
  -F "prompt=Replace the sky with a dramatic golden-hour sunset" \
  -F "size=1024x1024" \
  -F "quality=medium" > edit_resp.json
```

## 3. Error Handling Best Practices

Image generation takes noticeably longer than text chat and has stricter input validation. Build resilient handling around these common error cases:

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
        prompt="Clean isometric illustration of a cloud data center",
        size="1024x1024",
        quality="medium",
    )
except BadRequestError as exc:
    # Invalid parameter combination (e.g., unsupported size/response_format or safety moderation rejection)
    print("Request rejected (400):", exc.message)
except RateLimitError as exc:
    # Back off with jitter before retrying
    print("Rate limited (429):", exc.message)
except APITimeoutError:
    # High-quality or multi-image requests may need a larger timeout (120-180s)
    print("Timed out waiting for image generation")
except APIError as exc:
    print(f"Upstream API error ({exc.status_code}):", exc.message)
```

### Quick Error Checklist

- **HTTP 400 (`unknown_parameter` / `invalid_value`)**: Remove `response_format` when calling `gpt-image-1`, verify `size` is one of `1024x1024`, `1536x1024`, `1024x1536`, or `auto`, and ensure `background="transparent"` is paired with `png` or `webp` (not `jpeg`).
- **HTTP 400 (safety / content policy)**: Rephrase prompts that trigger upstream safety filters.
- **HTTP 403 Forbidden**: Confirm `base_url` is `https://api.adtyai.com/v1` and **not** `https://www.adtyai.com`.
- **HTTP 401 / Insufficient Balance**: Verify your key in [https://www.adtyai.com](https://www.adtyai.com) and check [Wallet, Bonus & Vouchers](/guides/wallet-and-vouchers).

## Related Guides

- [Image Generation Overview](/integrations/paint/)
- [Gemini Image Generation](/integrations/paint/banana)
- [Official SDKs](/integrations/advanced/sdks)
- [API Reference](/api/)
