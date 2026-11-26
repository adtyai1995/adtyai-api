# Cherry Studio

Cherry Studio is a popular cross-platform desktop AI workspace (Windows, macOS, Linux) supporting multi-provider chat, assistant presets, MCP tools, and local RAG knowledge bases. **ADTY AI** works across Cherry Studio's OpenAI, Anthropic, and Gemini provider types.

## 1. Prepare Your API Key

1. Sign in to the [ADTY AI Console](https://www.adtyai.com).
2. Create an API key on the **API Keys** page and pick the channel group matching the models you plan to use.
3. Keep [ADTY AI Pricing](https://www.adtyai.com/pricing) handy so you can confirm exact model IDs.

## 2. Configure Providers in Cherry Studio

Open Cherry Studio, click the **Settings** (gear) icon in the bottom-left corner, and open **Model Providers**.

### OpenAI-Compatible Provider

1. Select **OpenAI** (or click **Add** at the bottom of the provider list to create a dedicated **ADTY AI** provider with provider type **OpenAI**).
2. Enable the provider toggle in the top-right corner.
3. Paste your `<YOUR_API_KEY>` into **API Key**.
4. Set **API Host** to:

   ```text
   https://api.adtyai.com
   ```

### Anthropic Provider

1. Select **Anthropic** (or add a new custom provider with type **Anthropic**).
2. Enable the provider toggle.
3. Paste your `<YOUR_API_KEY>` into **API Key**.
4. Set **API Host** to:

   ```text
   https://api.adtyai.com
   ```

### Gemini Provider

1. Select **Gemini** (or add a new custom provider with type **Gemini**).
2. Enable the provider toggle.
3. Paste your `<YOUR_API_KEY>` into **API Key**.
4. Set **API Host** to:

   ```text
   https://api.adtyai.com
   ```

## 3. Understanding Cherry Studio's `/v1` Path Handling & `#` Override

Cherry Studio displays the live preview of the full request URL right beneath the **API Host** field. Always inspect that preview to avoid a doubled `/v1/v1` path:

| What You Enter in API Host | Cherry Studio Behavior | Effective OpenAI Endpoint |
| --- | --- | --- |
| `https://api.adtyai.com` | Automatically appends `/v1/chat/completions` | `https://api.adtyai.com/v1/chat/completions` |
| `https://api.adtyai.com/v1` | Recognizes `/v1` and appends `/chat/completions` (on recent builds) | `https://api.adtyai.com/v1/chat/completions` |
| `https://api.adtyai.com/` (trailing `/`) | Skips auto-inserting `/v1` in some versions | `https://api.adtyai.com/chat/completions` (will `404`!) |
| `https://api.adtyai.com/v1/chat/completions#` | `#` disables all path auto-completion and uses the literal URL | `https://api.adtyai.com/v1/chat/completions` |

::: tip Recommended Rule
Enter `https://api.adtyai.com` with **no trailing slash** for OpenAI, Anthropic, and Gemini providers. Only use the `#` suffix if you want to lock a specific full endpoint path verbatim.
:::

## 4. Add Models & Test the Connection

1. Click **Manage** under the Models section to fetch models automatically from `https://api.adtyai.com/v1/models`, and click **+** next to the models you want to enable.
2. If a newly released model does not appear in the list, click **Add** manually and enter the exact `<model-id>` from [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing).
3. Click the model's settings icon to confirm capability tags (**Vision**, **Tool Use / Function Calling**, **Reasoning**, or **Embedding**) so Cherry Studio enables image uploads, MCP calls, or thinking panels appropriately.
4. Click **Check** next to the **API Key** input box and select one of your enabled models. A green check confirms end-to-end connectivity.

## 5. Configure Knowledge Base Embedding Models

To build local RAG knowledge bases in Cherry Studio using ADTY AI embeddings:

1. In your **ADTY AI** OpenAI-compatible provider settings, click **Add** and enter an enabled embedding model ID from [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) (for example `text-embedding-3-small` or `text-embedding-3-large`).
2. Open that model's capability settings and mark it as an **Embedding** model (ensure chat-only tags are off).
3. Navigate to **Knowledge Base** in the left sidebar and click **Add**.
4. Choose your ADTY AI embedding model under **Embedding Model**, create the knowledge base, and import documents (PDF, Markdown, DOCX, or URLs).
5. In any chat session, select the knowledge base icon in the composer toolbar to ground replies in your indexed files.

## Troubleshooting

- **`404 Not Found` when clicking Check**: Inspect the URL preview under **API Host**. Remove any trailing `/` and avoid accidental `/v1/v1` duplication.
- **Model fetch returns empty or errors**: Add the model ID manually using the exact string shown on [ADTY AI Pricing](https://www.adtyai.com/pricing).
- **Knowledge base indexing fails**: Verify that the embedding model ID is included in your API key's group and is tagged as **Embedding** in Cherry Studio.
- **Want one-click import?** You can also export ADTY AI settings straight into Cherry Studio via [All API Hub](/integrations/advanced/all-api-hub).
