# zcoms — the comms foundation

`zcoms` is the **comms foundation** of the zcoms ecosystem: it builds the `zc` binary,
owns the single TDLib Telegram session (plus the WhatsApp Baileys sidecar), and serves
them to everything above it over a local IPC socket. Send & receive messages and media,
follow chats live, ask questions that block until you get a reply, and manage a **contacts
directory** (`comms.db`) — all from your terminal, all **scriptable** (CI, cron, AI loops).

It is a **dumb pipe**: it knows nothing about AI. The AI layer (interactive bridge, triage,
errands, session manager, personas) runs in the separate **`zcoms-agent`** process and
reaches Telegram/WhatsApp through this daemon. Install it with `zc install agent`; install
modules like team with `zc install <module>`.

**Published contract:** other tiers import `github.com/Zouriel/zcoms/client` — the wire
protocol, the IPC `Client`, the component `Harness`, and `ProtocolVersion` (advertised by
the daemon on connect; a mismatch fails loudly). They never open another tier's database.

Works on **Windows** and **Linux**.

---

## Install

### Pre-built binary (recommended)

Grab the bundle for your OS from [Releases](../../releases) — it ships with the
required TDLib library inside, so there's nothing else to install:

- **Windows** — `zcoms-win64.zip`: extract, then run `zc.exe` (or add the folder to your PATH).
- **Linux (x86-64)** — `zcoms-linux-x64.tar.gz`:
  ```sh
  tar xzf zcoms-linux-x64.tar.gz
  cd zcoms-linux-x64
  ./zc tg login
  ```
  `zc` loads the bundled `libtdjson.so` from its own folder, so just keep them together.

  > The Linux bundle is **fully self-contained** — it ships `libtdjson.so` plus its own
  > OpenSSL (`libssl.so.1.1`/`libcrypto.so.1.1`), all built against an old baseline, so it
  > runs on essentially every x86-64 Linux from ~2019 on (**glibc ≥ 2.29**) regardless of the
  > host's OpenSSL version. Keep all the files in the folder together.

### Build from source

```sh
git clone https://github.com/Zouriel/zcoms
cd zcoms
go build -o zc .
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

Then place `libtdjson.so` next to the `zc` binary, or ensure it's in your library path.

### Packaging a release

- `scripts/build-linux-portable.sh` — builds the **portable** Linux bundle in an Ubuntu 20.04
  container (TDLib + `zc` + bundled OpenSSL, old-glibc), producing `dist/zcoms-linux-x64.tar.gz`.
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
> go build -ldflags "-X zcoms/internal/config.BuildAPIID=123456 -X zcoms/internal/config.BuildAPIHash=abc123"
> ```

### 3. Login

```sh
zc tg login
```

Authenticate once with your phone number. The session is persisted locally — you won't need to log in again.

---

## Commands

| Command | Description |
|---|---|
| `zc tg login` | Sign in to Telegram |
| `zc tg logout [--hard]` | Sign out (`--hard` wipes the local session) |
| `zc tg auth` | Show the currently logged-in account |
| `zc tg send @username <message>` | Send a message to a user |
| `zc tg send-file <@username\|chat_id> <path> [caption]` | Send a file (photo/video/audio/document) |
| `zc tg download <@username\|chat_id>` | List recent received media and download a chosen one |
| `zc tg ask @username <message>` | Send a message and **wait for their reply** |
| `zc tg chat <@username\|chat_id> [message]` | One round-trip: send and/or wait for the next reply (scriptable) |
| `zc tg tail <@username\|chat_id>` | Follow a chat live; type to send, paste a path to send a file |
| `zc tg chats` | List recent chats |
| `zc wa status` | Ping the optional WhatsApp sidecar (paired/connected) |
| `zc install [bridge\|triage\|errands]` | Show component status, or install one (see [Components](#components)) |
| `zc uninstall <component>` | Remove a component (and anything that depends on it) |

The Telegram/WhatsApp commands above work standalone. The **agent features** are
opt-in [components](#components) — once installed, these commands light up:

| Command | Component | Description |
|---|---|---|
| `zc init agent` | bridge | Run the agent bridge (drive Claude/Codex from Telegram) — see [Agent bridge](#agent-bridge--drive-claudecodex-from-telegram) |
| `zc allowlist [add\|remove …]` | bridge | List/add/remove who may drive the agent bridge |
| `zc agents [set <type> <agent>]` | bridge | Show/set which agent (claude/codex) handles each session type (bridge/triage/errands) |
| `zc locations [add\|remove …]` | bridge | List/add/remove agent-bridge project locations |
| `zc triage [30m\|1h\|…\|twice-daily\|on\|off]` | triage | Show/set the incoming-message triage schedule |
| `zc errand [start\|list\|cancel …]` | errands | Dispatch/manage an agent that questions a contact and produces a deliverable |

Until a component is installed, its commands are hidden from `zc --help`; running
one prints an install hint.

## Components

zcoms ships lean — just Telegram + WhatsApp comms. The agent features run inside a
single long-running daemon (only one process can hold the Telegram session), so
they're opt-in **components** you add with `zc install`:

| Component | What it adds | Needs |
|---|---|---|
| `bridge` | Interactive agent sessions — locations, session management, `chat` | — |
| `triage` | Scheduled AI digest of incoming Telegram/WhatsApp messages | bridge |
| `errands` | Dispatch autonomous interviewer→producer agents to a contact | bridge |

```sh
zc install                 # show what's installed
zc install triage          # installs triage (pulls in bridge automatically)
zc agents set triage codex # pick the agent per session type
zc uninstall errands       # remove a component
```

Installing a component seeds its config, enables/refreshes the `zcoms-daemon`
service, and restarts it so the component is active immediately. `zc agents`
configures a separate backend (claude/codex) per session type.

### Examples

```sh
zc tg send @alice "deploy finished successfully"

zc tg send-file @alice ./report.pdf "here's the report"

zc tg ask @alice "should I use postgres or sqlite for this?" 
# blocks until @alice replies, prints their answer

zc tg tail @mygroup
```

### Media

Send a file from any chat by **pasting its path** while tailing, or with `zc tg send-file`.
The file type is chosen automatically from the extension — images go as photos, clips as
videos, audio as audio, everything else as a document.

Incoming media is **never downloaded automatically** — downloading an arbitrary file
just because it arrived is a security risk. Instead, fetch media deliberately with
`zc tg download`, which lists the most recent media in a chat (newest first) and lets you
pick one:

```sh
zc tg download @alice
# Recent media in "alice" (newest first):
#   [0] photo     sunset.jpg  — look at this  (1.2 MB)
#   [1] document  report.pdf  (340.0 KB)
# Enter number to download (Enter = newest [0], q to cancel):
```

Downloads land in a per-chat folder:

```
~/Downloads/zcoms/<chat name>/
```

| Flag | Description |
|---|---|
| `-n, --limit <N>` | How many recent messages to scan for media (default 30) |
| `-p, --pick <i>` | Download index `i` non-interactively (`0` = newest) |
| `--json` | List available media as JSON and exit (no download) |

---

## Scriptable back-and-forth: `zc tg chat`

`tail` is the interactive REPL. `zc tg chat` is its non-interactive counterpart — each
invocation does **one round-trip and exits**, so it composes cleanly in scripts and
agent loops (no long-lived process holding the session lock).

```sh
# Send and wait for the reply (prints the reply to stdout)
reply=$(zc tg chat @you "deploy to prod now or wait?")

# Just wait for the next incoming message (let the other side start)
zc tg chat @you

# Catch up: snapshot the last 10 messages and exit
zc tg chat @you --read 10

# Structured output for programmatic use; bound the wait
zc tg chat @you "still there?" --json --timeout 2m
```

| Flag | Description |
|---|---|
| `-w, --wait` | Wait for the next reply after sending (default `true`) |
| `-t, --timeout <dur>` | Max time to wait, e.g. `90s`, `5m` (`0` = no limit) |
| `-r, --read <N>` | Snapshot mode: print the last N messages and exit |
| `--json` | Emit each message as a JSON line (`message_id`, `sender`, `kind`, `text`, `file`, …) |
| `--download` | Download media in the reply (off by default) |

Media in replies is **not** downloaded by default. Pass `--download` to fetch it (only
for trusted senders), or use `zc tg download` to pick a specific file. With `--download` +
`--json`, the saved path comes back in the `file` field.

---

## Using zc for programmatic notifications

`zc tg send` and `zc tg ask` are designed to be scripted. You can use them in CI, cron jobs, or AI agent workflows to stay in the loop when you're away from your desk.

**One-way notification:**
```sh
zc tg send @you "build passed ✅"
```

**Ask a question and use the answer:**
```sh
answer=$(zc tg ask @you "deploy to prod now or wait?")
echo "User said: $answer"
```

**In a script:**
```sh
#!/bin/bash
run_tests
if [ $? -eq 0 ]; then
  zc tg send @you "tests passed — deploying"
  deploy
  zc tg send @you "deployment done ✅"
else
  choice=$(zc tg ask @you "tests failed — retry or abort?")
  # act on $choice
fi
```

---

## Agent bridge — drive Claude/Codex from Telegram

> Install it first: **`zc install bridge`** (triage and errands below are their own
> components — `zc install triage` / `zc install errands`). See [Components](#components).

`zc init agent` turns the logged-in account into a two-way bridge: **allow-listed users
message it to drive an AI agent** (Claude Code or Codex) on the host — pick a project,
resume a past session with an auto summary, and chat back and forth — with per-user,
role-based permissions. With the **triage** component it can also **auto-reply** to people
who aren't on the allow-list and DM you an **AI-triaged digest** of the important ones.
`zc tg send`/`ask`/`chat`/`send-file` keep working alongside it (they route through the
daemon's socket).

> Full reference and the security model: **[docs/AGENT-BRIDGE.md](docs/AGENT-BRIDGE.md)**.

### Prerequisites

Install at least one agent CLI and log it in: [`claude`](https://code.claude.com) and/or
[`codex`](https://developers.openai.com/codex/cli/). Check what `zc` sees:

```sh
zc agents          # Installed agents: claude, codex …
```

If neither is installed, the bridge and triage are unavailable (auto-reply still works).

### Configuration

Four JSON files in the config dir (`~/.config/zcoms/`), all `0600`, auto-created on first run:

**`agent-allowlist.json`** — who may drive it, their role, and (optionally) their agent
(also via `zc allowlist add <@user> <role> [locations...] [--agent claude|codex]` /
`zc allowlist remove <@user>`):
```json
{
  "@you":      { "role": "full", "locations": ["*"], "agent": "claude" },
  "@teammate": { "role": "read", "locations": ["Docs"] }
}
```

**`agent-locations.json`** — the projects, with an optional per-location ceiling (also via
`zc locations add <name> <path> [max_role]` / `zc locations remove <name>`):
```json
{
  "App":  "/home/you/app",
  "Prod": { "path": "/home/you/prod", "max_role": "read" }
}
```

**`agents.json`** — which backend runs each session type (also via
`zc agents set <bridge|triage|errands> <claude|codex>`):
```json
{ "default": "claude", "tasks": { "bridge": "claude", "triage": "codex", "errands": "claude" } }
```

**`agent-settings.json`** — auto-reply + triage (also via `zc triage`):
```json
{
  "main_user": "@you",
  "auto_reply_enabled": true,
  "auto_reply": "Message received — the owner will be notified shortly.",
  "triage": { "enabled": true, "schedule": "1h", "dir": "/home/you" },
  "whatsapp": { "enabled": false, "socket": "/home/you/.config/zcoms/wa.sock", "mark_read_on_reply": false }
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
errand …    — dispatch/approve/manage an errand (see Errands below)
```
Anything else you type is sent to the agent. You can also just *ask* in a `chat` session —
e.g. "ask my wife what's needed for her CV, make it, send it to her, and ping me when done" —
and it'll dispatch an errand for you.

### Run it as a service

`zc install bridge` already sets up and starts the `zcoms-daemon` user service for you.
To also start it on boot:

```sh
loginctl enable-linger "$USER"                  # start on boot without logging in
# while the daemon runs: `zc tg tail`/`chats`/`download`/`auth`/`login`/`logout` aren't
# available (they need their own session) — stop the daemon to use them.
```

### Auto-reply & triage of incoming messages

For people **not** on the allow-list, the daemon can:

- **Auto-reply** once (per hour, per sender) with `auto_reply` — set `auto_reply_enabled`.
- **Triage** their **unread** messages on a schedule: an agent (read-only) decides which are
  important and DMs `main_user` a digest of just those (nothing if none). It reads Telegram's
  unread state directly — so it catches messages received while the daemon was down, skips
  ones you've already read elsewhere, and marks the ones it processes as read.

Change the schedule on the fly (no restart) with `zc triage`:

```sh
zc triage                # show current
zc triage 30m            # or 1h, 2h, 3h, 6h, 12h
zc triage twice-daily    # ~08:00 and ~22:00
zc triage off            # / on
```

**WhatsApp (optional, off by default):** an unofficial in-process WhatsApp client (whatsmeow)
lets triage merge unread WhatsApp 1:1s into the **same** digest, and the `interact triage`
bridge command lets the agent reply to whoever wrote in — on WhatsApp or Telegram. Enable via
the `whatsapp` block in `agent-settings.json`; check it with `zc wa status`. ⚠️ Baileys
violates WhatsApp's ToS (ban risk) — prefer a secondary number.

### Errands — send an agent to question a contact

Dispatch an autonomous agent to message someone, ask them what's needed **one question at a
time** (with a running count), and turn their answers into a finished file — e.g. *"ask my wife
what's needed for her CV, make it, send it to her, and ping me when done."* Kick one off in
plain language from a `chat` / `interact triage` session, or directly:

```sh
zc errand start @alice "collect what's needed and draft a 1-page bio"
zc errand start --deliver --go wa:9607XXXXXXX "make a CV and send it to her"
zc errand list           # active errands
zc errand cancel <id>    # stop one
```

- `--deliver` also sends the finished file to the contact; `--go` skips the approval step.
- By default the agent first DMs **you** the question plan to approve (`errand yes|edit|no <id>`),
  then runs on its own — greeting the contact, asking one question at a time, and collecting any
  files they send.
- It runs as **two sandboxed agents**: an **interviewer** with no filesystem/shell access (it only
  chats and records answers to a single file), then a **producer** that treats those answers as
  untrusted third-party data, does only your original brief, flags anything suspicious or
  mismatched, builds the deliverable, and sends **you** the file(s) plus a summary when done.
- Works over Telegram or WhatsApp; the contact is excluded from triage/auto-reply while their
  errand is running, and errands resume after a daemon restart.

### Security — read before adding anyone

Allow-listing someone is roughly **shell-level access** as your user: roles gate *writes*,
not *reads*, and locations aren't a sandbox. Keep the allow-list tiny, enable **2FA** on the
account, and isolate (separate user / container / VM) for anyone you don't fully trust.
Details in [docs/AGENT-BRIDGE.md](docs/AGENT-BRIDGE.md).

## Use with Claude / AI agents

There's a companion [**Claude Agent Skill**](https://github.com/Zouriel/zcoms-skill) that teaches
Claude Code (and any agent supporting the skills standard) how to drive `zc` — so it can notify you,
ask a question and wait for your reply, converse, and send/receive files on its own:

**→ [github.com/Zouriel/zcoms-skill](https://github.com/Zouriel/zcoms-skill)**

```sh
git clone https://github.com/Zouriel/zcoms-skill
cp -r zcoms-skill/skills/zc ~/.claude/skills/zc
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
