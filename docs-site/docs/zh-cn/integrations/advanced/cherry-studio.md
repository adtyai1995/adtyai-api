# Cherry Studio

Cherry Studio 是一款广受欢迎的跨平台桌面端 AI 客户端（支持 Windows、macOS、Linux），内置多供应商对话、智能体助手、MCP 工具以及本地 RAG 知识库。**ADTY AI** 同时兼容 Cherry Studio 中的 OpenAI、Anthropic 与 Gemini 三种协议供应商。

## 1. 准备 API 密钥

1. 登录 [ADTY AI 控制台](https://www.adtyai.com)。
2. 前往「令牌 / API 密钥」页面创建密钥，并选择包含目标模型的分组。
3. 打开 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 备用，以便核对精确的 `<model-id>`。

## 2. 图形界面配置步骤

打开 Cherry Studio，点击左下角的 **设置**（齿轮图标），进入 **模型服务**。

### OpenAI 兼容供应商

1. 选择内置的 **OpenAI**，或点击列表底部 **添加** 新建一个名为 `ADTY AI`、提供商类型为 **OpenAI** 的自定义服务商。
2. 打开右上角开关启用该服务商。
3. 在 **API 密钥** 中填入 `<YOUR_API_KEY>`。
4. 在 **API 地址** 中填入：

   ```text
   https://api.adtyai.com
   ```

### Anthropic 供应商

1. 选择 **Anthropic**（或新建提供商类型为 **Anthropic** 的服务商）。
2. 开启右上角开关。
3. 在 **API 密钥** 中填入 `<YOUR_API_KEY>`。
4. 在 **API 地址** 中填入：

   ```text
   https://api.adtyai.com
   ```

### Gemini 供应商

1. 选择 **Gemini**（或新建提供商类型为 **Gemini** 的服务商）。
2. 开启右上角开关。
3. 在 **API 密钥** 中填入 `<YOUR_API_KEY>`。
4. 在 **API 地址** 中填入：

   ```text
   https://api.adtyai.com
   ```

## 3. 理解 Cherry Studio 的 `/v1` 自动补全与 `#` 强制路径规则

在 Cherry Studio 中，**API 地址** 输入框下方会实时预览最终发起请求的完整 URL。请务必核对预览结果，避免出现 `/v1/v1` 重复或漏掉 `/v1`：

| 输入的 API 地址 | Cherry Studio 的路径处理规则 | 实际发起的 OpenAI 请求地址 |
| --- | --- | --- |
| `https://api.adtyai.com` | 自动补全 `/v1/chat/completions` | `https://api.adtyai.com/v1/chat/completions` |
| `https://api.adtyai.com/v1` | 新版本可识别已有 `/v1`，直接补全 `/chat/completions` | `https://api.adtyai.com/v1/chat/completions` |
| `https://api.adtyai.com/`（末尾带 `/`） | 部分版本会跳过自动补全 `/v1` | `https://api.adtyai.com/chat/completions`（会报 `404`） |
| `https://api.adtyai.com/v1/chat/completions#` | 末尾 `#` 表示禁用所有自动补全，严格按原文请求 | `https://api.adtyai.com/v1/chat/completions` |

::: tip 推荐填写方式
在 OpenAI、Anthropic 与 Gemini 服务商中，统一填写不带末尾斜杠的 `https://api.adtyai.com` 即可。仅当你希望完全锁定某条固定接口路径时，再在完整路径末尾添加 `#`。
:::

## 4. 获取或手动添加模型并测试连通性

1. 点击模型区域的 **管理** 按钮，Cherry Studio 会向 `https://api.adtyai.com/v1/models` 拉取模型列表，点击对应模型右侧的 **+** 即可启用。
2. 若新上线的模型暂未出现在自动拉取列表中，点击 **添加** 手动填入 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 上对应的精确 `<model-id>`。
3. 点击已添加模型右侧的设置图标，按模型能力勾选 **视觉（Vision）**、**工具调用（Function Calling）**、**推理（Reasoning）** 或 **嵌入（Embedding）** 标签，以确保图片上传、MCP 或思考折叠面板正常工作。
4. 点击 **API 密钥** 右侧的 **检测** 按钮并选择一个已启用模型，出现绿色对勾即表示连接成功。

## 5. 配置本地知识库（RAG）向量嵌入模型

如需在 Cherry Studio 中通过 ADTY AI 构建本地知识库：

1. 在 OpenAI 兼容类型的 **ADTY AI** 服务商下点击 **添加**，填入 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 中当前分组已启用的嵌入模型 ID（例如 `text-embedding-3-small` 或 `text-embedding-3-large`）。
2. 打开该模型的设置，将其类型勾选为 **嵌入（Embedding）**。
3. 点击左侧导航栏的 **知识库**，点击 **添加** 新建知识库。
4. 在 **嵌入模型** 下拉菜单中选中刚配置的 ADTY AI 嵌入模型，保存后即可导入 PDF、Markdown、Word 文档或网页进行索引。
5. 在对话窗口底部工具栏勾选该知识库，即可基于索引文档进行问答。

## 常见问题排查

- **点击「检测」报 `404 Not Found`**：检查 **API 地址** 下方的完整路径预览，去掉多余的末尾 `/` 或重复的 `/v1/v1`。
- **拉取模型列表为空或失败**：直接点击 **添加** 手动录入 [ADTY AI 模型价格页](https://www.adtyai.com/pricing) 中的模型 ID 即可。
- **知识库文档向量化失败**：确认所选嵌入模型在当前 API 密钥所属分组内已开启，且已在 Cherry Studio 中标记为 **嵌入（Embedding）** 类型。
- **希望一键导入配置？** 可以通过 [All API Hub](/zh-cn/integrations/advanced/all-api-hub) 直接将 ADTY AI 密钥与模型配置导出至 Cherry Studio。
