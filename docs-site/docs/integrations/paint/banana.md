# Gemini Image Generation

Gemini's native multimodal models (such as `gemini-2.5-flash-image` and `gemini-3-pro-image-preview`—verify exact active IDs on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing)) generate and edit images directly through the native Gemini `:generateContent` endpoint on **ADTY AI**.

::: warning Use `https://api.adtyai.com` (No `/v1` Prefix)
The native Gemini endpoint lives under `/v1beta/models/{model}:generateContent` on `https://api.adtyai.com`.
- **Correct SDK `base_url`**: `https://api.adtyai.com`
- **Wrong**: `https://www.adtyai.com` (web console domain returns **HTTP 403**)
:::

## Prerequisites

1. Create an **ADTY AI API Key** (`sk-...`) at [https://www.adtyai.com](https://www.adtyai.com) under **API Tokens** in a group that supports Gemini image models.
2. Confirm the exact live model name on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) (for example `gemini-2.5-flash-image` for fast iteration or `gemini-3-pro-image-preview` for higher fidelity).

## 1. Python SDK (`google-genai`) — Text-to-Image

Install the official Google GenAI Python SDK and Pillow:

```bash
pip install -U google-genai pillow
```

Point `HttpOptions(base_url="https://api.adtyai.com")` to **ADTY AI**, request `response_modalities=["TEXT", "IMAGE"]`, and save every returned image part to disk:

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
        "A minimalist isometric illustration of a glass greenhouse studio "
        "on a misty morning, warm sunrise lighting, clean vector-inspired shading."
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
        print("Model note:", part.text)
    elif part.inline_data:
        image_count += 1
        img = Image.open(BytesIO(part.inline_data.data))
        out_path = out_dir / f"gemini_{image_count}.png"
        img.save(out_path)
        print(f"Saved {out_path} ({img.size[0]}x{img.size[1]})")
```

::: tip Controlling Aspect Ratio & Resolution
Supported Gemini image models also accept `image_config` options (such as aspect ratio like `"16:9"`, `"1:1"`, `"3:4"` or image size on pro preview tiers) inside `GenerateContentConfig`. Check the model's exact capabilities in [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) and the [Gemini FAQ](/faq/gemini).
:::

## 2. Raw HTTP (`curl`) — Text-to-Image

You can call `/v1beta/models/{model}:generateContent` directly with `curl` and authenticate via the `x-goog-api-key` header (or `Authorization: Bearer`):

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
            "text": "Create a clean product studio photo of a matte ceramic coffee dripper on pale oak wood, soft diffused daylight."
          }
        ]
      }
    ],
    "generationConfig": {
      "responseModalities": ["TEXT", "IMAGE"]
    }
  }' > response.json
```

Extract and save the base64 PNG from `response.json`:

```bash
python3 -c '
import base64, json
data = json.load(open("response.json"))
for idx, part in enumerate(data["candidates"][0]["content"]["parts"], 1):
    inline = part.get("inlineData") or part.get("inline_data")
    if inline and inline.get("data"):
        with open(f"output_{idx}.png", "wb") as f:
            f.write(base64.b64decode(inline["data"]))
        print(f"Saved output_{idx}.png")
'
```

## 3. Image-to-Image Editing (Inline Base64)

Gemini excels at conversational editing: pass an input image alongside natural-language edit instructions in the same `parts` array.

### Python (`google-genai`)

```python
import base64
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
            text="Keep the coffee dripper identical, replace the wooden surface with dark slate stone, and add gentle morning steam."
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
        print(f"Saved edited_{idx}.png")
```

### Raw HTTP (`curl`) with Base64 `inlineData`

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
          { \"text\": \"Convert this sketch into a crisp watercolor architectural rendering.\" },
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

## Troubleshooting

- **Text returned with no image**: Confirm `responseModalities` includes `"IMAGE"` and that you are calling an image-capable Gemini model (such as `gemini-2.5-flash-image` or `gemini-3-pro-image-preview`) rather than a text-only Gemini model.
- **HTTP 403 Forbidden**: Verify `base_url` is `https://api.adtyai.com` (never `https://www.adtyai.com`).
- **Model unavailable / 404**: Check [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) to confirm the exact model ID and ensure your API key's group includes it.
- **Timeout on high-resolution image edits**: Increase your HTTP client timeout (for example `timeout=120_000` ms in `HttpOptions`) because multi-megapixel generation takes longer than plain text completions.
- Need more help? See the [Gemini FAQ](/faq/gemini) or [Image Generation Overview](/integrations/paint/).
