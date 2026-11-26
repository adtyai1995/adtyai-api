# WorkBuddy

WorkBuddy is a desktop AI workspace designed for both software engineering and day-to-day office productivity. You can connect WorkBuddy to **ADTY AI** by adding a custom OpenAI-compatible or Anthropic provider.

## 1. Create a Dedicated API Key

1. Log in to the [ADTY AI Console](https://www.adtyai.com) and go to **API Keys**.
2. Click **Create API Key** and name it clearly (for example `workbuddy-coding` or `workbuddy-office`).
3. Choose the channel group that matches your intended models and set an optional spend limit. For security best practices, see [Account Security & PAT](/guides/security).

## 2. Add ADTY AI as a Custom Provider

Open WorkBuddy, navigate to **Settings → Model Providers** (or **API Configuration**), and click **Add Custom Provider**.

Choose the protocol tab that matches your target models:

| Protocol Option | API Base URL | Best For |
| --- | --- | --- |
| **OpenAI-Compatible** | `https://api.adtyai.com/v1` | GPT, DeepSeek, Gemini, and multi-model access through one endpoint |
| **Anthropic** | `https://api.adtyai.com` | Native Claude models (Sonnet, Opus, Haiku) |

Fill in the connection fields:

- **Provider Name**: `ADTY AI`
- **API Base URL**: `https://api.adtyai.com/v1` (OpenAI-compatible) or `https://api.adtyai.com` (Anthropic)
- **API Key**: `<YOUR_API_KEY>`

::: tip Check Auto-Appended `/v1`
Some desktop builds automatically append `/v1` to OpenAI-compatible URLs. If WorkBuddy shows a live endpoint preview beneath the URL input, make sure the final path resolves to `https://api.adtyai.com/v1/chat/completions` (or `https://api.adtyai.com/v1/messages` for Anthropic) without repeating `/v1/v1`.
:::

## 3. Add Model IDs & Verify Connection

1. Use **Fetch Models** (if available) or click **Add Model** manually.
2. Copy the exact `<model-id>` strings from [ADTY AI Pricing](https://www.adtyai.com/pricing) (for example `claude-sonnet-4-6`, `gpt-5.4`, `deepseek-chat`, or `gemini-2.5-pro`).
3. Click **Test Connection** to verify credentials, then open a new workspace chat and send a brief prompt such as `Reply with OK and the name of today's task`.

## 4. Picking the Right Model Group: Coding vs. Daily Office Tasks

Because coding sessions and routine office tasks have very different latency, context, and cost profiles, we recommend separating them by model (and optionally by API key):

| Workload | What to Prioritize | Recommended Model Tier |
| --- | --- | --- |
| **Coding & Repository Tasks** | Long-context accuracy, reliable tool/file operations, strong code reasoning | Flagship Claude Sonnet / Opus or GPT-5 series on a dedicated coding channel group |
| **Daily Office Tasks** (drafting, translation, meeting notes, spreadsheets) | Fast first-token latency and lower per-token cost | Lightweight Claude Haiku, GPT mini, Gemini Flash, or `deepseek-chat` |

Check live health and group coverage anytime on [Channel Availability](/guides/channel-status) and [ADTY AI Pricing](https://www.adtyai.com/pricing).

## Troubleshooting

- **`401 Unauthorized`**: Verify the key is copied cleanly from [https://www.adtyai.com](https://www.adtyai.com) and has not hit its configured quota cap.
- **`404 Not Found`**: Adjust whether `/v1` is included in the Base URL based on WorkBuddy's protocol selector.
- **Model unavailable**: Confirm the exact model ID is enabled for your API key's group on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing), or check [General FAQ](/faq/).
