# 在 CC Switch 中配置 Codex 桌面应用

**Codex 桌面应用（Codex Desktop App）** 与 **Codex CLI** 共用同一套用户配置目录（`~/.codex/`）。这意味着只要在 **CC Switch** 中启用 **ADTY AI** 的 Codex 配置，命令行与桌面客户端便会同步生效，无需重复维护两份配置。

## 共用配置机制说明

Codex CLI 与 Codex 桌面应用都从以下两个文件读取当前供应商与 API Key：

- **macOS / Linux**：`~/.codex/config.toml` 与 `~/.codex/auth.json`
- **Windows**：`%USERPROFILE%\.codex\config.toml` 与 `%USERPROFILE%\.codex\auth.json`

当你在 CC Switch 的 **Codex** 标签页切换供应商时，CC Switch 会原子化更新这两个文件。新启动的 Codex CLI 会立即读取最新内容，而 Codex 桌面应用则需要在**完全退出并重新启动**后加载新配置。

## 第一步：在 CC Switch 中启用 ADTY AI 配置

如果尚未创建 ADTY AI 的 Codex 档案，请先按 [配置 Codex CLI](/zh-cn/integrations/cc-switch/codex) 的步骤在 CC Switch 的 **Codex** 标签页新建并启用档案，核心参数如下：

- **Base URL**：`https://api.adtyai.com/v1`
- **API Key**：`sk-your-api-key`
- **Wire API**：`responses`
- **模型**：如 `gpt-5.4`（请以 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 实时模型列表为准）

确认 `~/.codex/config.toml` 中包含 `requires_openai_auth = true`，且 `~/.codex/auth.json` 已写入你的 API Key：

```toml
model_provider = "adty"
model = "gpt-5.4"

[model_providers.adty]
name = "ADTY AI"
base_url = "https://api.adtyai.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

```json
{
  "OPENAI_API_KEY": "sk-your-api-key"
}
```

在 CC Switch 中点击 **启用 / 切换** 激活 `ADTY AI`。

## 第二步：完全重启 Codex 桌面应用

仅点击桌面窗口右上角的关闭按钮通常**不会**重新加载配置，因为桌面端往往会在后台保留常驻进程。

- **macOS**：选中 Codex 桌面应用后按 `Cmd + Q` 彻底退出（或在程序坞图标上右键选择 **退出**），等待 2 秒后再重新打开。
- **Windows**：在右下角系统托盘右键退出 Codex 桌面应用，或打开 **任务管理器** 确认已结束所有 `Codex` 相关后台进程，然后再重新启动。

## 第三步：验证 API Key 模式与请求路由

1. 重新打开 **Codex 桌面应用**。
2. 确认应用直接以 API Key 模式进入工作区，不再强制要求通过网页登录个人消费者账号。
3. 在项目会话中发送一条简单指令（例如：`列出当前仓库根目录的主要文件`）。
4. 访问 [https://www.adtyai.com/logs](https://www.adtyai.com/logs)，确认对应 API Key 下出现本次调用记录且模型名称无误。

::: tip 后续切换档案提醒
日后在 CC Switch 中切换个人 Key / 团队 Key 或更改默认模型后，请同样对 Codex 桌面应用执行一次**完全退出并重启**，以确保所有后台代理线程同步切换到新的 `~/.codex/` 配置。
:::

## 排查：Codex 桌面应用忽略自定义接口怎么办

如果启用了 CC Switch 的 ADTY AI 配置后，Codex 桌面应用依然连向官方地址、反复弹出网页登录，或提示鉴权失败，请依次排查以下四点：

### 1. `auth.json` 中残留旧的 OAuth 登录态

如果你此前在 Codex 桌面端登录过网页账号，`~/.codex/auth.json` 里可能残留 `tokens`、`access_token` 或 `refresh_token` 字段，它们的优先级往往高于 `OPENAI_API_KEY`。

- 先在 Codex 桌面应用内退出登录，再**完全退出**应用；
- 回到 CC Switch 重新点击启用 `ADTY AI` 档案，确保 `~/.codex/auth.json` 中只保留干净的 `OPENAI_API_KEY` 字段。

### 2. 后台辅助进程尚未退出

主窗口关闭后，后台工作进程可能仍在内存里沿用旧地址：

- **macOS**：在终端执行 `pkill -f Codex`（或在活动监视器强制退出），再重新打开应用。
- **Windows**：在任务管理器中结束所有 Codex 相关进程后再重新启动。

### 3. 系统级 / GUI 环境变量覆盖了 `~/.codex/`

桌面应用会继承操作系统级的环境变量（而不仅仅是终端里的 `.zshrc` / `.bashrc`）：

- **Windows**：检查「用户变量」和「系统变量」里是否有旧的 `OPENAI_API_KEY` 或 `OPENAI_BASE_URL`，删除后重启应用或重新登录系统。
- **macOS**：在终端检查 `launchctl getenv OPENAI_BASE_URL` 与 `launchctl getenv OPENAI_API_KEY`；若非空，使用 `launchctl unsetenv <VAR_NAME>` 清除。

### 4. 所选模型不在当前 API Key 权限范围内

如果请求已经抵达 `https://api.adtyai.com/v1`，但界面报错提示模型不存在或无权访问，请核对 `~/.codex/config.toml` 以及桌面端下拉框中的模型名称是否与 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 一致，且未被 API Key 的模型白名单排除。

更多 Codex 客户端排查建议请参阅 [Codex 排查](/zh-cn/faq/codex)。
