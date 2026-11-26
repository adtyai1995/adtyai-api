# 在 CC Switch 中配置 Claude Desktop

当你在 **Claude Desktop** 中开展本地编程工作流（例如在桌面端内置的 Claude Code / 本地代码任务中使用自定义模型接口）时，可以借助 **CC Switch** 统一管理底层的 **ADTY AI** 供应商配置，告别手动修改配置文件和环境变量的繁琐流程。

::: info 配置生效范围说明
Claude Desktop 的官方云端网页聊天标签默认绑定消费者账号；而桌面端发起的本地 Claude Code / 代码任务会读取本机 Anthropic 配置（`~/.claude/settings.json`）。通过 CC Switch 管理该层配置，即可让本地会话稳定走 `https://api.adtyai.com`。如需了解不依赖 CC Switch 的桌面端接入细节或 MCP 配置，另请参阅 [Claude Desktop](/zh-cn/integrations/advanced/claude-desktop)。
:::

## 准备工作

- 已在 macOS 或 Windows 上安装 **Claude Desktop** 与 **CC Switch**。
- 已在 [https://www.adtyai.com](https://www.adtyai.com) 获取支持 Claude 模型的 **ADTY AI** API Key（`sk-...`）。
- 已按 [通用基础配置](/zh-cn/integrations/cc-switch/common) 清理可能冲突的系统环境变量。

## 第一步：在 CC Switch 中配置 ADTY AI 档案

1. 启动 **CC Switch**，进入 **Claude Code** 供应商管理页面。
2. 新建或编辑 **ADTY AI** 配置档案，填写 Anthropic 协议参数：

| 字段 | 填写内容 | 说明 |
| --- | --- | --- |
| **供应商名称** | `ADTY AI` | 如 `ADTY AI - Desktop` |
| **API Base URL** | `https://api.adtyai.com` | **不加 `/v1`**，切勿填写 `https://www.adtyai.com` |
| **API Key**（`ANTHROPIC_AUTH_TOKEN`） | `sk-your-api-key` | 粘贴你的 ADTY AI API Key |
| **默认模型** | `claude-sonnet-4-6` | 请在 [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) 确认可用模型 ID |

3. 检查 JSON 配置预览无误：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-your-api-key",
    "ANTHROPIC_MODEL": "claude-sonnet-4-6",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  }
}
```

4. 点击 **保存** 后再点击 **启用 / 切换**，让 CC Switch 将配置写入 `~/.claude/settings.json`（Windows 路径为 `%USERPROFILE%\.claude\settings.json`）。

## 第二步：完全重启 Claude Desktop

Electron 桌面应用在启动子进程或初始化环境时会缓存配置。每次在 CC Switch 中首次启用或切换档案后，请对 Claude Desktop 执行**完全退出并重启**：

### macOS

1. 切换到 **Claude Desktop** 窗口并按 `Cmd + Q`（仅点击左上角红点只是隐藏窗口，后台进程仍在运行）。
2. 检查屏幕右上角菜单栏，若仍显示 Claude 图标，点击后选择 **退出（Quit）**。
3. 从「应用程序」重新启动 **Claude Desktop**。

### Windows

1. 关闭 Claude Desktop 主窗口。
2. 在任务栏右下角**系统托盘**中找到 **Claude** 图标，右键点击 **退出（Quit）**。
3. 必要时打开 **任务管理器**，确认已无残留 `Claude.exe` 进程后再重新启动。

## 第三步：验证配置生效

1. 重启后在 Claude Desktop 中开启一个新的本地编程会话。
2. 发送一条简短测试消息，确认可以正常收到响应。
3. 打开 [https://www.adtyai.com/logs](https://www.adtyai.com/logs)，检查是否已记录对应的 API Key 与模型调用日志。

## 干净地切换多个配置档案

如果你需要在多个 ADTY AI 档案（例如个人钱包 Key 与团队共享钱包 Key）之间来回切换：

1. 先在 CC Switch 中点击切换到目标档案。
2. 新建会话，或在旧会话持续占用原连接时彻底重启一次 Claude Desktop。
3. 尽量不要在开启 CC Switch 的同时手工改写 `~/.claude/settings.json`，保持 CC Switch 为单一配置入口。

## 环境冲突与证书问题排查

### 系统环境变量覆盖

图形界面应用会读取系统级环境变量，若存在旧值会覆盖 `~/.claude/settings.json`：

- **Windows**：在系统搜索框打开「环境变量」，删除用户变量与系统变量中遗留的 `ANTHROPIC_BASE_URL`、`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`，然后彻底重启 Claude Desktop。
- **macOS**：在终端运行 `launchctl getenv ANTHROPIC_BASE_URL` 与 `launchctl getenv ANTHROPIC_AUTH_TOKEN` 检查是否返回旧值，若有请用 `launchctl unsetenv <VAR_NAME>` 清除。

### 企业代理或 TLS 证书报错

如果遇到 `UNABLE_TO_VERIFY_LEAF_SIGNATURE`、`SELF_SIGNED_CERT_IN_CHAIN` 或 TLS 握手失败：

1. 检查本机系统时间与时区是否准确。
2. 若身处启用了 HTTPS 流量审计的企业代理网络，请导出企业根证书（PEM 格式），在系统环境变量中配置 `NODE_EXTRA_CA_CERTS` 指向该证书路径，并重启 Claude Desktop。
3. 不建议全局设置 `NODE_TLS_REJECT_UNAUTHORIZED=0`，关闭证书校验会降低所有基于 Node.js 的桌面应用安全性。
4. 可在终端通过以下命令排查系统层面的 TLS 连通性：

   ```bash
   curl -v https://api.adtyai.com
   ```

### 仍未解决？

- 关于 Anthropic 协议报错、登录循环或模型名称问题，可查阅 [Claude Code 排查](/zh-cn/faq/claude-code)。
- 更多桌面端集成说明与工作流技巧，请参阅 [Claude Desktop](/zh-cn/integrations/advanced/claude-desktop)。
