# 通用基础配置

在 **CC Switch** 中为 Claude Code、Codex 或桌面客户端添加具体配置之前，建议先了解下面这些通用最佳实践。理清接口域名规则、多档案管理方式和 Shell 环境变量优先级，能帮你避开绝大多数「配置改了却不生效」或 `403`/`404` 报错。

## API Base URL 规则（双域名机制）

**ADTY AI** 将网页控制台与模型中转网关严格分离：

- **网页控制台域名**：`https://www.adtyai.com` —— 仅用于登录管理钱包、创建 API Key、查看日志与团队管理。**切勿**将其填入任何客户端或 CC Switch 的 API 地址栏（向控制台域名发中转请求会返回 HTTP `403`）。
- **API 接口域名**：`https://api.adtyai.com` —— 所有模型调用与账单查询的专用网关。

不同协议家族在 `https://api.adtyai.com` 后是否拼接 `/v1` 有明确区别：

| CC Switch 中的目标客户端 | 协议类型 | 正确的 Base URL | 常见错误写法 |
| --- | --- | --- | --- |
| Claude Code | Anthropic Messages | `https://api.adtyai.com` | 多填 `/v1` 会拼成 `/v1/v1/messages`（`404`） |
| Claude Desktop | Anthropic Messages | `https://api.adtyai.com` | 多填 `/v1`，或误填 `https://www.adtyai.com` |
| Codex CLI | OpenAI Responses | `https://api.adtyai.com/v1` | 漏填 `/v1` 会导致 `/responses` 返回 `404` |
| Codex 桌面应用 | OpenAI Responses | `https://api.adtyai.com/v1` | 漏填 `/v1`，或残留旧的 OAuth 登录缓存 |
| Gemini CLI | Gemini 原生协议 | `https://api.adtyai.com` | 多填 `/v1` 会破坏 `/v1beta/models/...` 路径 |

::: warning 不要保留末尾斜杠
填写 Base URL 时请去掉末尾的 `/`，例如填写 `https://api.adtyai.com` 而非 `https://api.adtyai.com/`，避免部分客户端直接拼接路径后出现双斜杠异常。
:::

## 管理多个 ADTY AI 配置档案

CC Switch 最大的价值在于可以同时维护多个互不干扰的 ADTY AI 档案。建议不要把同一个无限额主 Key 到处复用，而是在 [https://www.adtyai.com](https://www.adtyai.com) 按场景创建多个专用 API Key，并用清晰的命名区分：

- **`ADTY AI - 个人日常`**：绑定个人钱包并设置合理额度上限，用于日常编码。
- **`ADTY AI - 团队项目`**：使用团队 Key，消耗自动计入团队共享钱包（详见 [团队协作与共享钱包](/zh-cn/guides/teams)）。
- **`ADTY AI - 高速渠道` / `ADTY AI - 经济分组`**：分别绑定不同分组的 API Key，按任务复杂度一键切换（详见 [渠道可用性与分组选择](/zh-cn/guides/channel-status)）。
- **`ADTY AI - 外包 / 专项核账`**：配置独立额度和过期时间，方便项目结项后核对消耗与随时停用（详见 [账号安全与访问令牌](/zh-cn/guides/security)）。

## 避免与 Shell 环境变量发生冲突

CC Switch 切换供应商的原理，是帮你将选中的配置写入各个客户端的原生配置文件（例如 `~/.claude/settings.json`、`~/.codex/config.toml`、`~/.codex/auth.json`）。

**但在绝大多数命令行客户端中，Shell 环境变量的优先级高于本地配置文件。** 如果你之前在 `~/.zshrc`、`~/.bashrc` 或 Windows 系统环境变量里手动写过 `ANTHROPIC_BASE_URL`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_API_KEY`、`OPENAI_BASE_URL` 或 `OPENAI_API_KEY`，即使在 CC Switch 里点击了切换，终端里的工具依然会读取旧的环境变量，导致切换看似无效。

### 第一步：排查现存冲突变量

**macOS / Linux（Bash / Zsh）：**

```bash
env | grep -E '^(ANTHROPIC|OPENAI|GEMINI|CLAUDE)_'
```

**Windows PowerShell：**

```powershell
Get-ChildItem Env: | Where-Object { $_.Name -match '^(ANTHROPIC|OPENAI|GEMINI|CLAUDE)_' }
```

### 第二步：清理持久化配置

- **macOS / Linux**：检查 `~/.zshrc`、`~/.bashrc`、`~/.bash_profile` 和 `~/.profile`，删除或注释掉旧的 `export ANTHROPIC_*` 与 `export OPENAI_*` 语句，然后重新打开终端窗口。
- **Windows**：按 `Win + S` 搜索「编辑账户的环境变量」（以及「编辑系统环境变量」），删除遗留的 `ANTHROPIC_*` / `OPENAI_*` 条目，并完全关闭重开所有终端与 IDE。
- **IDE 内置终端**：VS Code、Cursor、JetBrains 系列 IDE 会在启动时缓存一份环境变量；清理系统环境变量后务必完全退出并重启 IDE。

::: tip 最佳实践原则
同一客户端的凭据管理，建议在 **CC Switch 管理** 与 **手动 Shell 导出** 之间二选一，不要混用。
:::

## 配置备份、导出与换机迁移

重装系统、升级客户端或同步配置到另一台电脑前，可以先做一次完整备份：

1. 打开 CC Switch 的 **设置（Settings）**，选择 **导出配置（Export）** 或 **备份（Backup）**，生成配置文件快照。
2. 将导出文件保存到加密密码管理器或本地私密目录中。
3. 在新设备安装 CC Switch 后点击 **导入配置（Import）**，再重新启用对应的 ADTY AI 档案，即可自动重建客户端配置文件。

::: danger 妥善保管导出文件
CC Switch 导出的配置包内包含明文 API Key（`sk-...`）。**切勿**将其提交到 Git 仓库、放入多人共享网盘或发给他人排查问题。如不慎泄露，请立即登录 [https://www.adtyai.com](https://www.adtyai.com) 吊销并重建对应 Key。
:::

## 网络与代理设置

在绝大多数网络环境下，直接连接 `https://api.adtyai.com` 即可获得低延迟与稳定响应，无需额外代理。如果你处于企业内网或必须经由本地代理访问外网：

- 仅在无法直连 `https://api.adtyai.com` 时，再在 CC Switch 的通用/网络设置中填写本地代理地址（例如 `http://127.0.0.1:7890` 或 `socks5://127.0.0.1:1080`）。
- 避免叠加冲突的代理规则（如同时开启系统级 TUN 模式又保留了指向失效端口的 `HTTPS_PROXY`）。
- 随时可在终端验证到 ADTY AI 网关的直连连通性：

  ```bash
  curl -I https://api.adtyai.com
  ```

  只要正常返回 HTTP 响应头（无 DNS 超时或 TLS 握手失败），即说明网络链路通畅。

## 下一步

- 准备接入 Claude Code？继续阅读 [配置 Claude Code](/zh-cn/integrations/cc-switch/claude-code)。
- 使用 Codex 系列工具？前往 [配置 Codex CLI](/zh-cn/integrations/cc-switch/codex) 或 [配置 Codex 桌面应用](/zh-cn/integrations/cc-switch/codex-app)。
- 想在 CC Switch 里实时看额度？查看 [额度与余额查询](/zh-cn/integrations/cc-switch/usage-query)。
