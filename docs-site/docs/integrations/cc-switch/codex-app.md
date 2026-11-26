# Configuring Codex Desktop App in CC Switch

The **Codex Desktop App** shares the exact same configuration directory (`~/.codex/`) as **Codex CLI**. Because of this shared architecture, activating an **ADTY AI** provider inside **CC Switch** configures both Codex CLI and Codex Desktop App from a single profile.

## How Shared Configuration Works

Both Codex CLI and Codex Desktop App read their active model provider and credentials from two files in your user home directory:

- **macOS / Linux**: `~/.codex/config.toml` and `~/.codex/auth.json`
- **Windows**: `%USERPROFILE%\.codex\config.toml` and `%USERPROFILE%\.codex\auth.json`

When you switch profiles under the **Codex** tab in CC Switch, CC Switch atomically rewrites both files. Any new Codex CLI invocation picks up the change immediately, while Codex Desktop App loads the updated configuration on its next full startup.

## Step 1: Configure & Activate ADTY AI in CC Switch

If you have not already created an ADTY AI Codex profile, follow [Codex CLI Setup](/integrations/cc-switch/codex) to add one under CC Switch's **Codex** tab with:

- **Base URL**: `https://api.adtyai.com/v1`
- **API Key**: `sk-your-api-key`
- **Wire API**: `responses`
- **Model**: e.g., `gpt-5.4` (verify live model IDs at [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing))

Confirm that `~/.codex/config.toml` contains `requires_openai_auth = true` under `[model_providers.adty]` and that `~/.codex/auth.json` contains your `OPENAI_API_KEY`:

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

Click **Enable** / **Switch** on the `ADTY AI` profile in CC Switch.

## Step 2: Fully Restart Codex Desktop App

Closing the desktop window is usually **not** enough—desktop apps often keep background workers alive with the previous configuration loaded in memory.

- **macOS**: Press `Cmd + Q` while Codex Desktop App is focused (or right-click its Dock icon and select **Quit**). Wait a couple of seconds, then reopen the app.
- **Windows**: Exit Codex Desktop App from the system tray icon, or open **Task Manager** and end any lingering `Codex` processes before relaunching.

## Step 3: Verify API-Key Mode & Live Routing

1. Launch **Codex Desktop App**.
2. Confirm that it opens directly into workspace / API-key mode without forcing a consumer browser login flow.
3. Open a workspace thread and send a quick prompt (for example, `List the top-level files in this repository.`).
4. Open [https://www.adtyai.com/logs](https://www.adtyai.com/logs) in your browser and verify that the request appears under your ADTY AI API key with the expected model name.

::: tip Switching Profiles Later
Whenever you switch between personal and team ADTY AI profiles (or change the default model) in CC Switch, perform a full quit and relaunch of Codex Desktop App so all background agent sessions pick up the new `~/.codex/` files.
:::

## Troubleshooting: Codex App Ignores Custom Endpoints

If Codex Desktop App continues hitting official endpoints, prompts you to log in again, or fails with authentication errors after switching in CC Switch, work through these checks in order:

### 1. Stale Consumer OAuth Tokens in `auth.json`

If you previously signed in to Codex with a browser account, `~/.codex/auth.json` may contain cached OAuth tokens (`tokens`, `access_token`, or `refresh_token`) that take priority over `OPENAI_API_KEY`.

- Sign out inside Codex Desktop App first, **quit** the app completely, then re-activate your `ADTY AI` profile in CC Switch so `~/.codex/auth.json` contains only your `OPENAI_API_KEY`.

### 2. Lingering Background Helper Processes

Even after closing the UI window, a background Codex worker can keep using old endpoints.

- **macOS**: Run `pkill -f Codex` in Terminal (or force-quit in Activity Monitor), then relaunch.
- **Windows**: End all Codex tasks in Task Manager, then relaunch.

### 3. GUI / System Environment Variables Overriding `~/.codex/`

Desktop applications inherit OS-level environment variables (not just your terminal's `.zshrc` / `.bashrc`).

- **Windows**: Check both User and System Environment Variables for `OPENAI_API_KEY` or `OPENAI_BASE_URL` and remove stale values, then sign out/in or restart the app.
- **macOS**: Check `launchctl getenv OPENAI_BASE_URL` and `launchctl getenv OPENAI_API_KEY`. Clear any stale values with `launchctl unsetenv <VAR_NAME>`.

### 4. Model Name Unavailable on Your API Key Group

If Codex Desktop App connects to `https://api.adtyai.com/v1` but returns a model-not-found or permission error, confirm that the model selected in `~/.codex/config.toml` (and inside the app's model dropdown) is available on [https://www.adtyai.com/pricing](https://www.adtyai.com/pricing) and allowed by your API key's model restrictions.

For additional Codex-specific diagnostics, see the [Codex FAQ](/faq/codex).
