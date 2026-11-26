# CC Switch 简介与安装

**CC Switch** 是一款跨平台的桌面应用与命令行管理工具，专门用于统一管理各类 AI 编程客户端的供应商配置，涵盖 **Claude Code**、**Codex CLI**、**Codex 桌面应用**、**Claude Desktop** 以及 **Gemini CLI**。

借助 CC Switch，你无需每次更换模型分组、轮换 API Key 或在个人钱包与团队共享钱包之间切换时反复手动编辑 JSON、TOML 或环境变量文件，只需在界面中点一下即可完成完整切换。

## 为什么推荐配合 ADTY AI 使用

将 CC Switch 与 **ADTY AI** 搭配使用，可以显著提升日常开发与多项目协作的效率：

- **一键切换配置方案**：在个人 Key、团队共享 Key、不同渠道分组或不同默认模型之间瞬间切换，无需手工修改 `~/.claude/settings.json` 或 `~/.codex/config.toml`。
- **避免手工编辑出错**：自动生成规范的 JSON 与 TOML 结构，清晰区分 Anthropic 协议地址（`https://api.adtyai.com`）与 OpenAI/Codex 协议地址（`https://api.adtyai.com/v1`）。
- **便捷备份与跨机迁移**：可将已整理好的供应商档案导出备份，换机或在远程 Linux 开发机中通过 [CC Switch CLI 工具](/zh-cn/integrations/cc-switch/cli) 快速恢复。
- **内置用量与余额查看**：支持接入 ADTY AI 的账单与令牌用量接口，在客户端卡片上直接查看已用与剩余额度。

::: tip 开始前准备
请先注册并登录 **ADTY AI** 控制台（[https://www.adtyai.com](https://www.adtyai.com)），准备好至少一个可用的 API Key（`sk-...`）。如果你首次了解 ADTY AI 的双域名机制，建议先阅读 [快速上手与双域名规则](/zh-cn/guides/overview)。
:::

## 支持的客户端一览

| 客户端 | 接口协议 | ADTY AI 接口地址 | 对应配置教程 |
| --- | --- | --- | --- |
| Claude Code | Anthropic Messages | `https://api.adtyai.com`（不带 `/v1`） | [配置 Claude Code](/zh-cn/integrations/cc-switch/claude-code) |
| Codex CLI | OpenAI Responses | `https://api.adtyai.com/v1` | [配置 Codex CLI](/zh-cn/integrations/cc-switch/codex) |
| Codex 桌面应用 | OpenAI Responses | `https://api.adtyai.com/v1` | [配置 Codex 桌面应用](/zh-cn/integrations/cc-switch/codex-app) |
| Claude Desktop | Anthropic Messages | `https://api.adtyai.com`（不带 `/v1`） | [配置 Claude Desktop](/zh-cn/integrations/cc-switch/claude-desktop) |
| Gemini CLI | Gemini 原生协议 | `https://api.adtyai.com`（不带 `/v1`） | [通用基础配置](/zh-cn/integrations/cc-switch/common) |

## 下载与安装

前往 CC Switch 官方 GitHub Releases 页面，选择与你的操作系统和芯片架构对应的最新安装包。

### macOS

1. 根据芯片类型下载对应的 `.dmg` 安装镜像：
   - **Apple Silicon（M1 / M2 / M3 / M4）**：选择 `aarch64` / `arm64` 版本
   - **Intel Mac**：选择 `x86_64` 版本
2. 双击打开 `.dmg`，将 **CC Switch** 拖入 **应用程序（Applications）** 文件夹。
3. 从「应用程序」或聚焦搜索启动 CC Switch。

::: info macOS 首次打开提示「无法验证开发者」
可在 **系统设置 → 隐私与安全性** 底部点击 **仍要打开**，或在终端中移除隔离属性：

```bash
xattr -cr "/Applications/CC Switch.app"
```
:::

### Windows

1. 下载 Windows 安装包（`.msi` 或 `-setup.exe`，按架构选择 `x64` 或 `arm64`）。
2. 双击运行安装向导并完成安装。
3. 从开始菜单打开 **CC Switch**。

若 Windows SmartScreen 对新版本弹出未知发布者提示，确认安装包来自官方发布页后，点击 **更多信息 → 仍要运行** 即可。

### Linux

根据你的发行版选择合适的格式：

- **AppImage（通用）**：

  ```bash
  chmod +x CC-Switch-*.AppImage
  ./CC-Switch-*.AppImage
  ```

- **Debian / Ubuntu（`.deb`）**：

  ```bash
  sudo apt install ./cc-switch_*.deb
  ```

- **Fedora / RHEL（`.rpm`）**：

  ```bash
  sudo dnf install ./cc-switch-*.rpm
  ```

如果你使用的是无图形界面的远程 Linux 服务器、SSH 会话或 WSL2，可以直接跳到 [CC Switch CLI 工具](/zh-cn/integrations/cc-switch/cli) 安装命令行版本。

## 本专题阅读路线

建议按以下顺序完成配置，整个过程清晰无踩坑：

1. [通用基础配置](/zh-cn/integrations/cc-switch/common) — 掌握双域名与 Base URL 规范、多档案规划、备份导出、代理配置，以及彻底清理冲突的 Shell 环境变量。
2. [配置 Claude Code](/zh-cn/integrations/cc-switch/claude-code) — 在 CC Switch 中添加 ADTY AI 供应商、核对生成的 `~/.claude/settings.json` 并在终端验证。
3. [配置 Codex CLI](/zh-cn/integrations/cc-switch/codex) — 配置 `https://api.adtyai.com/v1`、`wire_api = "responses"` 与 `~/.codex/auth.json`。
4. [配置 Codex 桌面应用](/zh-cn/integrations/cc-switch/codex-app) — 了解 Codex 桌面端与 CLI 共用 `~/.codex/` 目录的机制，彻底重启加载并解决自定义地址不生效问题。
5. [配置 Claude Desktop](/zh-cn/integrations/cc-switch/claude-desktop) — 配合 CC Switch 配置 Claude Desktop 本地编程环境，处理 macOS/Windows 完全重启及代理证书问题。
6. [额度与余额查询](/zh-cn/integrations/cc-switch/usage-query) — 通过 ADTY AI 控制台实时追踪消费，并在 CC Switch 中配置用量查询脚本与安全规范。
7. [CC Switch CLI 工具](/zh-cn/integrations/cc-switch/cli) — 在服务器、SSH、WSL2 和自动化脚本中完成配置列表、切换、导入导出与环境冲突排查。

## 更习惯手动配置？

CC Switch 并非强制依赖。如果你想不借助切换工具直接配置客户端，可前往 [交互式配置向导](/zh-cn/integrations/cli/)，或直接查看 [Claude Code](/zh-cn/integrations/cli/claude-code)、[Codex CLI](/zh-cn/integrations/cli/codex) 与 [Claude Desktop](/zh-cn/integrations/advanced/claude-desktop) 的独立接入教程。
