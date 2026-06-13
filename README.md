# TgCli

A Telegram CLI built on [TDLib](https://github.com/tdlib/td) — send & receive messages and
media, follow chats live, and ask questions that block until you get a reply, all from your
terminal. It's built to be **scripted** (CI, cron, AI-agent loops), and ships an optional
**agent bridge** that lets you drive **Claude Code / Codex** from Telegram and get an
AI-triaged digest of incoming messages.

Works on **Windows** and **Linux**.

---

## Install

### Pre-built binary (recommended)

Grab the bundle for your OS from [Releases](../../releases) — it ships with the
required TDLib library inside, so there's nothing else to install:

- **Windows** — `TgCli-win64.zip`: extract, then run `tg.exe` (or add the folder to your PATH).
- **Linux (x86-64)** — `TgCli-linux-x64.tar.gz`:
  ```sh
  tar xzf TgCli-linux-x64.tar.gz
  cd TgCli-linux-x64
  ./tg login
  ```
  `tg` loads the bundled `libtdjson.so` from its own folder, so just keep them together.

  > The Linux bundle is **fully self-contained** — it ships `libtdjson.so` plus its own
  > OpenSSL (`libssl.so.1.1`/`libcrypto.so.1.1`), all built against an old baseline, so it
  > runs on essentially every x86-64 Linux from ~2019 on (**glibc ≥ 2.29**) regardless of the
  > host's OpenSSL version. Keep all the files in the folder together.

### Build from source

```sh
git clone https://github.com/Zouriel/TgCli
cd TgCli
go build -o tg .
```

#### Linux — TDLib dependency

The repo bundles the Windows TDLib DLLs (`bin/`) but **not** the Linux `libtdjson.so`
(it's distro-specific). Install it from your package manager:

```sh
# Debian / Ubuntu
sudo apt install libtdjson-dev

# Arch
sudo pacman -S tdlib
```

Then place `libtdjson.so` next to the `tg` binary, or ensure it's in your library path.

### Packaging a release

- `scripts/build-linux-portable.sh` — builds the **portable** Linux bundle in an Ubuntu 20.04
  container (TDLib + `tg` + bundled OpenSSL, old-glibc), producing `dist/TgCli-linux-x64.tar.gz`.
  Requires Docker and a local Go toolchain.
- `scripts/package.sh` — quick bundles from a locally available `libtdjson.so` (handy for
  Windows, or a Linux build that only needs to run on the build host).

---

## Setup

### 1. Get Telegram API credentials

Go to [my.telegram.org](https://my.telegram.org), create an app, and grab your **API ID** and **API Hash**.

### 2. Configure credentials

Copy `example.env` to `.env` and fill in your values:

```sh
cp example.env .env
```

```env
TG_API_ID   = "your_api_id"
TG_API_HASH = "your_api_hash"
```

> **Tip:** If you're distributing a pre-built binary, embed credentials at build time so end users don't need to supply them:
> ```sh
> go build -ldflags "-X tg/internal/config.BuildAPIID=123456 -X tg/internal/config.BuildAPIHash=abc123"
> ```

### 3. Login

```sh
tg login
```

Authenticate once with your phone number. The session is persisted locally — you won't need to log in again.

---

## Commands

| Command | Description |
|---|---|
| `tg login` | Sign in to Telegram |
| `tg logout [--hard]` | Sign out (`--hard` wipes the local session) |
| `tg auth` | Show the currently logged-in account |
| `tg send @username <message>` | Send a message to a user |
| `tg send-file <@username\|chat_id> <path> [caption]` | Send a file (photo/video/audio/document) |
| `tg download <@username\|chat_id>` | List recent received media and download a chosen one |
| `tg ask @username <message>` | Send a message and **wait for their reply** |
| `tg chat <@username\|chat_id> [message]` | One round-trip: send and/or wait for the next reply (scriptable) |
| `tg tail <@username\|chat_id>` | Follow a chat live; type to send, paste a path to send a file |
| `tg chats` | List recent chats |
| `tg init agent` | Run the agent bridge (drive Claude/Codex from Telegram) — see [Agent bridge](#agent-bridge--drive-claudecodex-from-telegram) |
| `tg allowlist [add\|remove …]` | List/add/remove who may drive the agent bridge |
| `tg agents [set <task> <agent>]` | Show/set which agent (claude/codex) handles which task |
| `tg locations [add\|remove …]` | List/add/remove agent-bridge project locations |
| `tg triage [30m\|1h\|…\|twice-daily\|on\|off]` | Show/set the incoming-message triage schedule |

The last three configure the optional **agent bridge** (below); the rest work standalone.

### Examples

```sh
tg send @alice "deploy finished successfully"

tg send-file @alice ./report.pdf "here's the report"

tg ask @alice "should I use postgres or sqlite for this?" 
# blocks until @alice replies, prints their answer

tg tail @mygroup
```

### Media

Send a file from any chat by **pasting its path** while tailing, or with `tg send-file`.
The file type is chosen automatically from the extension — images go as photos, clips as
videos, audio as audio, everything else as a document.

Incoming media is **never downloaded automatically** — downloading an arbitrary file
just because it arrived is a security risk. Instead, fetch media deliberately with
`tg download`, which lists the most recent media in a chat (newest first) and lets you
pick one:

```sh
tg download @alice
# Recent media in "alice" (newest first):
#   [0] photo     sunset.jpg  — look at this  (1.2 MB)
#   [1] document  report.pdf  (340.0 KB)
# Enter number to download (Enter = newest [0], q to cancel):
```

Downloads land in a per-chat folder:

```
~/Downloads/telegramcli/<chat name>/
```

| Flag | Description |
|---|---|
| `-n, --limit <N>` | How many recent messages to scan for media (default 30) |
| `-p, --pick <i>` | Download index `i` non-interactively (`0` = newest) |
| `--json` | List available media as JSON and exit (no download) |

---

## Scriptable back-and-forth: `tg chat`

`tail` is the interactive REPL. `tg chat` is its non-interactive counterpart — each
invocation does **one round-trip and exits**, so it composes cleanly in scripts and
agent loops (no long-lived process holding the session lock).

```sh
# Send and wait for the reply (prints the reply to stdout)
reply=$(tg chat @you "deploy to prod now or wait?")

# Just wait for the next incoming message (let the other side start)
tg chat @you

# Catch up: snapshot the last 10 messages and exit
tg chat @you --read 10

# Structured output for programmatic use; bound the wait
tg chat @you "still there?" --json --timeout 2m
```

| Flag | Description |
|---|---|
| `-w, --wait` | Wait for the next reply after sending (default `true`) |
| `-t, --timeout <dur>` | Max time to wait, e.g. `90s`, `5m` (`0` = no limit) |
| `-r, --read <N>` | Snapshot mode: print the last N messages and exit |
| `--json` | Emit each message as a JSON line (`message_id`, `sender`, `kind`, `text`, `file`, …) |
| `--download` | Download media in the reply (off by default) |

Media in replies is **not** downloaded by default. Pass `--download` to fetch it (only
for trusted senders), or use `tg download` to pick a specific file. With `--download` +
`--json`, the saved path comes back in the `file` field.

---

## Using tg for programmatic notifications

`tg send` and `tg ask` are designed to be scripted. You can use them in CI, cron jobs, or AI agent workflows to stay in the loop when you're away from your desk.

**One-way notification:**
```sh
tg send @you "build passed ✅"
```

**Ask a question and use the answer:**
```sh
answer=$(tg ask @you "deploy to prod now or wait?")
echo "User said: $answer"
```

**In a script:**
```sh
#!/bin/bash
run_tests
if [ $? -eq 0 ]; then
  tg send @you "tests passed — deploying"
  deploy
  tg send @you "deployment done ✅"
else
  choice=$(tg ask @you "tests failed — retry or abort?")
  # act on $choice
fi
```

---

## Agent bridge — drive Claude/Codex from Telegram

`tg init agent` turns the logged-in account into a two-way bridge: **allow-listed users
message it to drive an AI agent** (Claude Code or Codex) on the host — pick a project,
resume a past session with an auto summary, and chat back and forth — with per-user,
role-based permissions. It can also **auto-reply** to people who aren't on the allow-list
and DM you an **AI-triaged digest** of the important ones. `tg send`/`ask`/`chat`/`send-file`
keep working alongside it (they route through the daemon's socket).

> Full reference and the security model: **[docs/AGENT-BRIDGE.md](docs/AGENT-BRIDGE.md)**.

### Prerequisites

Install at least one agent CLI and log it in: [`claude`](https://code.claude.com) and/or
[`codex`](https://developers.openai.com/codex/cli/). Check what `tg` sees:

```sh
tg agents          # Installed agents: claude, codex …
```

If neither is installed, the bridge and triage are unavailable (auto-reply still works).

### Configuration

Four JSON files in the config dir (`~/.config/tg/`), all `0600`, auto-created on first run:

**`agent-allowlist.json`** — who may drive it, their role, and (optionally) their agent
(also via `tg allowlist add <@user> <role> [locations...] [--agent claude|codex]` /
`tg allowlist remove <@user>`):
```json
{
  "@you":      { "role": "full", "locations": ["*"], "agent": "claude" },
  "@teammate": { "role": "read", "locations": ["Docs"] }
}
```

**`agent-locations.json`** — the projects, with an optional per-location ceiling (also via
`tg locations add <name> <path> [max_role]` / `tg locations remove <name>`):
```json
{
  "App":  "/home/you/app",
  "Prod": { "path": "/home/you/prod", "max_role": "read" }
}
```

**`agents.json`** — which backend runs which task (also via `tg agents set`):
```json
{ "default": "claude", "tasks": { "triage": "codex" } }
```

**`agent-settings.json`** — auto-reply + triage (also via `tg triage`):
```json
{
  "main_user": "@you",
  "auto_reply_enabled": true,
  "auto_reply": "Message received — the owner will be notified shortly.",
  "triage": { "enabled": true, "schedule": "1h", "dir": "/home/you" }
}
```

### Roles

| Role | What the agent may do | Claude mode | Codex sandbox |
|---|---|---|---|
| `read` | inspect / plan only, never acts | `plan` | `read-only` |
| `confirm` | plans first, acts only after you reply `yes` in Telegram | `plan` → execute | `read-only` → execute |
| `edit` | read/write/run, auto-approved | `acceptEdits` | `workspace-write` |
| `full` | anything, unattended | `--dangerously-skip-permissions` | `--dangerously-bypass-approvals-and-sandbox` |

A location's `max_role` caps everyone there (effective role = the more restrictive).

### Using it from Telegram

Message the account; it understands plain-text commands:

```
help        — command list
locations   — list projects; reply with a number to pick one
resume      — list that project's past sessions; pick one to resume (with a summary)
new         — start a fresh session in the current project
status      — show current location / session / role
end         — detach from the current session
```
Anything else you type is sent to the agent.

### Run it as a service

```sh
cp tg-daemon.service ~/.config/systemd/user/    # edit WorkingDirectory if needed
systemctl --user enable --now tg-daemon         # stays up, restarts on crash
loginctl enable-linger "$USER"                  # start on boot without logging in
# while it runs: `tail`/`chats`/`download`/`auth`/`login`/`logout` aren't available
# (they need their own session) — stop the daemon to use them.
```

### Auto-reply & triage of incoming messages

For people **not** on the allow-list, the daemon can:

- **Auto-reply** once (per hour, per sender) with `auto_reply` — set `auto_reply_enabled`.
- **Triage** their **unread** messages on a schedule: an agent (read-only) decides which are
  important and DMs `main_user` a digest of just those (nothing if none). It reads Telegram's
  unread state directly — so it catches messages received while the daemon was down, skips
  ones you've already read elsewhere, and marks the ones it processes as read.

Change the schedule on the fly (no restart) with `tg triage`:

```sh
tg triage                # show current
tg triage 30m            # or 1h, 2h, 3h, 6h, 12h
tg triage twice-daily    # ~08:00 and ~22:00
tg triage off            # / on
```

### Security — read before adding anyone

Allow-listing someone is roughly **shell-level access** as your user: roles gate *writes*,
not *reads*, and locations aren't a sandbox. Keep the allow-list tiny, enable **2FA** on the
account, and isolate (separate user / container / VM) for anyone you don't fully trust.
Details in [docs/AGENT-BRIDGE.md](docs/AGENT-BRIDGE.md).

## Use with Claude / AI agents

There's a companion [**Claude Agent Skill**](https://github.com/Zouriel/tgcli-skill) that teaches
Claude Code (and any agent supporting the skills standard) how to drive `tg` — so it can notify you,
ask a question and wait for your reply, converse, and send/receive files on its own:

**→ [github.com/Zouriel/tgcli-skill](https://github.com/Zouriel/tgcli-skill)**

```sh
git clone https://github.com/Zouriel/tgcli-skill
cp -r tgcli-skill/skills/tg ~/.claude/skills/tg
```

Then tell the agent your Telegram `@username`, and it will reach you on Telegram when it finishes a
task or gets stuck.

---

## Environment variables

| Variable | Description |
|---|---|
| `TG_API_ID` | Telegram API ID |
| `TG_API_HASH` | Telegram API hash |
| `TDLIB_BIN` | Path to the directory containing TDLib binaries (optional) |

Variables can be set in a `.env` file in the current directory or exported in the shell.

---

## License

[MIT](LICENSE)
