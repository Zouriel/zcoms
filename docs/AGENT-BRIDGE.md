# Agent bridge (`zc init agent`)

> **Install it first:** `zc install bridge`. The bridge is an opt-in component (so is
> `triage` and `errands` — `zc install triage` / `zc install errands`); installing one
> seeds its config, sets up the `zcoms-daemon` service, and restarts it. Each session
> type has its own agent backend — `zc agents set <bridge|triage|errands> <claude|codex>`.

`zc init agent` turns the logged-in Telegram account into a two-way bridge: allow-listed
users message the account and drive an **AI agent** (Claude Code or Codex) on this machine
(pick a project, resume a past session with a summary, chat back and forth), while your own
`zc tg send` / `zc tg ask` notifications keep working through the same account. It can also
auto-reply to strangers and DM you an hourly digest of important messages.

## How it runs

The daemon owns the single Telegram session and listens on a Unix socket
(`~/.config/zcoms/daemon.sock`, owner-only `0600`). `zc tg send`, `zc tg ask`, `zc tg send-file`,
and `zc tg chat` — **including `zc tg chat <id> --read N`** to snapshot a chat's recent
history — automatically route through that socket when the daemon is running, and fall
back to opening their own session when it isn't. So reading and replying to any chat
work normally while the daemon is up; there's no need to stop it.

Commands that need their own interactive session — `tail`, `chats`, `download`, `auth`,
`login`, `logout` — can't share the daemon's session, so while it's running they exit
with a clear message instead of a lock error. (`auth_state` in `config.json` is written
by the daemon on startup and by `auth`/`login`/`logout`, so it tracks the live session.)
Stop the daemon to use the session-owning commands:

```sh
systemctl --user stop zcoms-daemon
```

Run it as a service (stays up, restarts on crash, starts on boot):

```sh
cp scripts/zcoms-daemon.service ~/.config/systemd/user/   # edit WorkingDirectory if needed
systemctl --user enable --now zcoms-daemon
loginctl enable-linger "$USER"                          # start on boot without login
```

## Configuration

Two JSON files in the zcoms config dir (`~/.config/zcoms/`), both kept `0600`:

**`agent-allowlist.json`** — who may use the bridge, at what role, and (optionally) which
agent backend:
```json
{
  "@you":      { "role": "full",  "locations": ["*"],        "agent": "codex" },
  "@teammate": { "role": "read",  "locations": ["Docssite"]                    }
}
```
`agent` is `claude` (default) or `codex` — each user can drive either backend.

**`agent-locations.json`** — the projects, with an optional per-location ceiling:
```json
{
  "App":  "/home/you/app",
  "Prod": { "path": "/home/you/prod", "max_role": "read" }
}
```

### Roles

| Role | What the agent may do | Claude mode | Codex sandbox |
|---|---|---|---|
| `read` | inspect / plan only, never acts | `plan` | `read-only` |
| `confirm` | plans first, acts only after you reply `yes` in Telegram | `plan` → execute | `read-only` → execute |
| `edit` | read/write/run, auto-approved | `acceptEdits` | `workspace-write` |
| `full` | anything, unattended | `--dangerously-skip-permissions` | `--dangerously-bypass-approvals-and-sandbox` |

A location's `max_role` caps everyone there (effective role = the more restrictive of the
user's role and the location cap).

## Auto-reply & hourly triage (`agent-settings.json`)

Optional "secretary" features for messages from people **not** on the allow-list:

```json
{
  "main_user": "@you",
  "auto_reply_enabled": true,
  "auto_reply": "Message received — the owner will be notified shortly.",
  "triage": { "enabled": true, "every_minutes": 60, "dir": "/home/you", "agent": "claude" }
}
```

- **Auto-reply** — a non-allow-listed sender gets the canned `auto_reply` (at most once per
  hour each).
- **Triage** — every `every_minutes`, the buffered stranger messages are run through the
  `agent` (read-only) in `dir`; it decides which are important and DMs `main_user` a bullet
  digest. Nothing is sent if none are important.
- **`chat`** — a full general-purpose agent session in your home directory at your
  allow-listed role (full for the owner): it can create/edit files, run shell commands,
  SSH into servers, etc. It is a normal Claude Code session (no directive protocol, no
  sandbox at `full`), resuming across turns until `new`/`end`. ⚠️ This is unrestricted —
  only grant it to fully-trusted allow-listed users.
- **`interact triage`** — talk to the triage agent (shared brain) and have it read or reply
  to chats. The agent is sandboxed and never touches Telegram directly; instead it emits
  directives the daemon executes on its behalf:
  - `READ <@username|chat_id> [count]` — the daemon fetches that chat's recent history and
    feeds it back, so the agent can answer questions like "what did X last say?" for **any**
    chat (not just the unread batch), even while the daemon is running.
  - `SEND <index|@username|chat_id> | message` — reply to someone in the batch by index, or
    to **anyone** by @username / chat id. The daemon sends as you; the agent only drafts.
  - `SENDFILE <index|@username|chat_id> | <path> [| caption]` — upload a local file (photo/
    document). The agent's working dir is a writable scratch space (`<configdir>/agent-staging`,
    network off) so it can produce a file (e.g. a screenshot) and then SENDFILE it; the daemon
    does the upload. Telegram only (WhatsApp file send isn't supported yet).

These run even with an empty allow-list ("secretary-only" mode).

## WhatsApp triage (optional Baileys sidecar)

Triage and replies can cover **WhatsApp** alongside Telegram, via an in-process
WhatsApp client (whatsmeow). It is **off by
default** — with `whatsapp.enabled:false` (or the block absent) behavior is
identical to a Telegram-only build.

```json
{
  "whatsapp": { "enabled": false, "socket": "/home/you/.config/zcoms/wa.sock", "mark_read_on_reply": false }
}
```

When enabled and the sidecar is running:

- **Unified digest** — each triage pass merges unread WhatsApp 1:1s with the
  Telegram ones into the **same** read-only AI pass and the **same** digest DM.
  Each line is tagged `[WhatsApp]` / `[Telegram]` so you know where to look. On
  any sidecar error, triage logs `[triage]` and continues with Telegram only.
- **`interact triage`** — send this to the bridge to start a session seeded with
  the **last triage batch** (persisted to `~/.config/zcoms/last-triage.json`, so it
  survives restarts and works long after the digest). Tell the agent who to
  reply to; it can only reply to people **in that batch, by index**, and the
  daemon (never the agent) sends — over WhatsApp or Telegram as appropriate.
  `end` to finish.

> ⚠️ Baileys is an **unofficial** WhatsApp client and violates WhatsApp's ToS;
> the paired number carries a ban risk. Prefer a **secondary number** and enable
> 2FA. `mark_read_on_reply`
> defaults **off** for WhatsApp (triage already marks threads read at digest
> time). Check status with `zc wa status`.

## ⚠️ Security model — read before adding anyone

- **Roles gate writes, not access.** Even `read` lets Claude open any file this Unix user
  can and send the contents back over Telegram. `edit`/`full` can touch the **whole
  filesystem** — `locations` choose where a session *starts*, they do **not** sandbox it.
- **Allow-listing someone ≈ giving them shell-level access** to this machine as your user.
  Only add people you'd trust with that. For untrusted users, isolate properly (a separate
  Unix user, a container, or a VM) — this tool does not sandbox.
- **`full` runs `--dangerously-skip-permissions`.** Whoever controls an allow-listed
  Telegram account can run arbitrary commands here. **Enable two-factor auth** on the
  Telegram account the daemon logs in as, and keep the allowlist as small as possible.
- The IPC socket is `0600` (owner-only), but any process running **as your user** can talk
  to it and send messages as the account — inherent to local IPC.
