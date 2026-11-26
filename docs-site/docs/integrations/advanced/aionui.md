# AionUI

AionUI is a multi-agent desktop GUI that brings direct API chat models and local CLI coding agents (such as Claude Code, Codex CLI, and Gemini CLI) into a single workspace. You can connect **ADTY AI** both as a direct custom model provider in AionUI Settings and as the backend for the CLI agents AionUI launches underneath.

## 1. Add a Custom Provider in AionUI Settings

Open AionUI, go to **Settings → Model / Providers**, and click **Add Custom Provider** (or edit an existing provider). Match the Base URL to the protocol you select:

| Provider Protocol | Base URL | Notes |
| --- | --- | --- |
| **OpenAI-Compatible** | `https://api.adtyai.com/v1` | Works with GPT, DeepSeek, Gemini, and OpenAI-compatible channels |
| **Anthropic** | `https://api.adtyai.com` | Native Claude Messages API (do not append `/v1`) |
| **Gemini** | `https://api.adtyai.com` | Native Gemini API (do not append `/v1` or `/v1beta`) |

Fill in the fields:

1. **Name**: `ADTY AI` (or separate entries like `ADTY AI (OpenAI)` and `ADTY AI (Anthropic)`).
2. **Base URL**: Choose the exact URL from the table above.
3. **API Key**: Paste your `<YOUR_API_KEY>` created in the [ADTY AI Console](https://www.adtyai.com).
4. **Models**: Click **Fetch Models** or add exact `<model-id>` values from [ADTY AI Pricing](https://www.adtyai.com/pricing), then save.

## 2. Configure Underlying CLI Agents Inside AionUI

When you launch a CLI agent panel inside AionUI, AionUI invokes the locally installed CLI binary and inherits its local config files and environment variables. Configure each CLI agent first so AionUI routes every agent turn through **ADTY AI**:

### Claude Code in AionUI

1. Ensure Claude Code is installed and configured following [Claude Code Setup](/integrations/cli/claude-code).
2. Confirm `~/.claude/settings.json` contains:

   ```json
   {
     "env": {
       "ANTHROPIC_BASE_URL": "https://api.adtyai.com",
       "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>"
     }
   }
   ```

### Codex CLI in AionUI

1. Install and configure Codex CLI following [Codex CLI Setup](/integrations/cli/codex).
2. Confirm `~/.codex/config.toml` points to `base_url = "https://api.adtyai.com/v1"` and `~/.codex/auth.json` holds your ADTY AI key.

### Gemini CLI in AionUI

1. Install Gemini CLI and expose the ADTY AI endpoint and key in your user environment (see [Environment Preparation](/integrations/cli/environment) and [Gemini FAQ](/faq/gemini)):

   ::: code-group

   ```bash [macOS / Linux]
   export GOOGLE_GEMINI_BASE_URL="https://api.adtyai.com"
   export GEMINI_API_KEY="<YOUR_API_KEY>"
   ```

   ```powershell [Windows (PowerShell)]
   [Environment]::SetEnvironmentVariable("GOOGLE_GEMINI_BASE_URL", "https://api.adtyai.com", "User")
   [Environment]::SetEnvironmentVariable("GEMINI_API_KEY", "<YOUR_API_KEY>", "User")
   ```

   :::

::: tip Restart AionUI After Editing CLI Configs
Desktop apps snapshot environment variables at launch. After modifying `~/.claude/settings.json`, `~/.codex/config.toml`, or OS environment variables, fully quit and reopen AionUI so newly spawned CLI sessions pick up your changes.
:::

## 3. Verify Responses

1. **Direct API chat**: Open a new conversation, select an **ADTY AI** model from the model picker, and send `Reply with OK to confirm connectivity.`
2. **CLI agent session**: Open a workspace folder in AionUI, choose Claude Code, Codex, or Gemini CLI, and ask it to inspect a file in the repository. Verify the streaming response completes cleanly and tool calls execute as expected.

## Troubleshooting

- **CLI agent not detected in AionUI**: Confirm the binary (`claude`, `codex`, or `gemini`) runs in a regular terminal and that its install directory is included in your system `PATH` (see [Environment Preparation](/integrations/cli/environment)).
- **`404 Not Found` on direct provider**: Double-check that OpenAI-Compatible uses `https://api.adtyai.com/v1`, while Anthropic and Gemini use `https://api.adtyai.com`.
- **Auth errors in a specific agent**: Check [Channel Availability](/guides/channel-status) and verify the key configured for that CLI agent belongs to a group supporting the requested model.
- **Additional diagnostics**: Review [Claude Code FAQ](/faq/claude-code), [Codex FAQ](/faq/codex), or [Gemini FAQ](/faq/gemini).
