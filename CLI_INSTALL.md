# LieXiu CLI — Installation Guide for AI Agents

> **Audience:** This document is designed for AI agents (Claude Code, Codex, etc.) to read and execute step-by-step. Each step includes the exact shell commands to run, expected outcomes, and error handling.

## One-Line Instruction for Users

Give this instruction to your AI agent:

```
Fetch https://github.com/kailonyang/liexiu/blob/main/CLI_INSTALL.md and follow the instructions to install LieXiu CLI, log in, and start the daemon on this machine.
```

---

## Step 1: Check if LieXiu CLI is already installed

Run:

```bash
liexiu version
```

- **If it prints a version string** (e.g. `liexiu v0.x.x`): skip to **Step 3**.
- **If command not found**: continue to **Step 2**.

---

## Step 2: Install the LieXiu CLI

> **Windows users:** Skip to [Option C: Windows (PowerShell)](#option-c-windows-powershell) below.

### Option A: Homebrew (preferred — macOS/Linux)

Check if Homebrew is available:

```bash
which brew
```

If `brew` is found, install via Homebrew:

```bash
brew install kailonyang/tap/liexiu
```

Then verify:

```bash
liexiu version
```

If the version prints successfully, skip to **Step 3**.

To upgrade later, run:

```bash
brew upgrade kailonyang/tap/liexiu
```

### Option B: Download from GitHub Releases (macOS/Linux, no Homebrew)

If Homebrew is not available, download the binary directly.

Detect OS and architecture, then download the correct archive:

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')   # "darwin" or "linux"
ARCH=$(uname -m)                                # "x86_64" or "arm64"

# Normalize architecture name
if [ "$ARCH" = "x86_64" ]; then
  ARCH="amd64"
fi

# Get the latest release tag from GitHub
LATEST=$(curl -sI https://github.com/kailonyang/liexiu/releases/latest | grep -i '^location:' | sed 's/.*tag\///' | tr -d '\r\n')

# Download and extract
VERSION="${LATEST#v}"
curl -sL "https://github.com/kailonyang/liexiu/releases/download/${LATEST}/liexiu-cli-${VERSION}-${OS}-${ARCH}.tar.gz" -o /tmp/liexiu.tar.gz
tar -xzf /tmp/liexiu.tar.gz -C /tmp liexiu
sudo mv /tmp/liexiu /usr/local/bin/liexiu
rm /tmp/liexiu.tar.gz
```

Verify:

```bash
liexiu version
```

**If this fails:**
- Check that `/usr/local/bin` is in `$PATH`.
- On Linux, you may need `chmod +x /usr/local/bin/liexiu`.
- If `sudo` is not available, install to a user-writable directory: `mv /tmp/liexiu ~/.local/bin/liexiu` and ensure `~/.local/bin` is in `$PATH`.

### Option C: Windows (PowerShell)

Run in PowerShell (no admin required):

```powershell
irm https://raw.githubusercontent.com/kailonyang/liexiu/main/scripts/install.ps1 | iex
```

This downloads the latest Windows binary from GitHub Releases, installs it to `%USERPROFILE%\.liexiu\bin\`, and adds it to your user PATH.

Verify:

```powershell
liexiu version
```

**If this fails:**
- Restart your terminal so the updated PATH takes effect.
- If you use Scoop, the installer will use it automatically: `scoop bucket add liexiu https://github.com/kailonyang/scoop-bucket.git && scoop install liexiu`
- If your execution policy blocks the script: `Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned` then re-run.

---

## Step 3: Log in

Run:

```bash
liexiu login
```

**Important:** This command opens a browser window for OAuth authentication. Tell the user:

> "A browser window will open for LieXiu login. Please complete the authentication in your browser, then come back here."

Wait for the command to complete. It will automatically discover and watch all workspaces the user belongs to.

Verify:

```bash
liexiu auth status
```

Expected output should show the authenticated user and server URL.

**If login fails:**
- If no browser is available (headless environment), the user can generate a Personal Access Token at `https://liexiu.ai/settings?tab=tokens` and run: `liexiu login --token <mul_...>` (use `--token=` with an empty value to be prompted interactively).
- If the server URL needs to be customized: `liexiu config set server_url <url>` before logging in.

---

## Step 4: Start the daemon

First, check if the daemon is already running:

```bash
liexiu daemon status
```

- **If status is "running"**: skip to **Step 5**.
- **If status is "stopped"**: start it:

```bash
liexiu daemon start
```

Wait 3 seconds, then verify:

```bash
liexiu daemon status
```

Expected output should show `running` status with detected agents (e.g. `claude`, `codex`, `copilot`, `opencode`, `openclaw`, `hermes`, `pi`, `cursor-agent`, `grok`).

**If daemon fails to start:**
- Check logs: `liexiu daemon logs`
- If a port conflict occurs, the daemon may already be running under a different profile.
- If no agents are detected, ensure at least one AI CLI (`claude`, `codex`, `copilot`, `opencode`, `openclaw`, `hermes`, `pi`, `cursor-agent`, or `grok`) is installed and on the `$PATH`.

---

## Step 5: Verify everything is working

Run:

```bash
liexiu daemon status
```

Confirm:
1. Status is `running`
2. At least one agent is listed (e.g. `claude`, `codex`, `copilot`, `opencode`, `openclaw`, `hermes`, `pi`, `cursor-agent`, or `grok`)
3. At least one workspace is being watched

If the agents list is empty, tell the user:

> "The LieXiu daemon is running but no AI agent CLIs were detected. Please install at least one supported CLI (`claude`, `codex`, `copilot`, `opencode`, `openclaw`, `hermes`, `pi`, `cursor-agent`, or `grok`), then restart the daemon with `liexiu daemon stop && liexiu daemon start`."

---

## Summary

When all steps are complete, inform the user:

> "LieXiu CLI is installed and the daemon is running. Agents in your workspaces can now execute tasks on this machine. You can manage workspaces with `liexiu workspace list` and view daemon logs with `liexiu daemon logs -f`."
