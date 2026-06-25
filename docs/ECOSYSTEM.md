# Zcoms Ecosystem Map

> A reference survey of all zcoms repos: structure, build, runtime topology, and
> agent docs. Written so this discovery doesn't have to be redone. Covers repos
> under `/home/zouriel/personal/`. Last surveyed: 2026-06-24.

## TL;DR

`zcoms` is the main app (Go, module `zcoms`, binary `zc`) — a Telegram + WhatsApp
CLI built on TDLib. Everything else is an **opt-in component** that runs as its
own process and talks to the app over local Unix sockets in `~/.config/zcoms/`.
All components share one library, `zcoms-sdk`. Nothing is network-exposed; the
core daemon owns the single TDLib Telegram session and everything else is a
stateless IPC client.

## Repos

| Repo | Module | Role |
|---|---|---|
| **zcoms** | `zcoms` | Main app + daemon. Owns the TDLib Telegram session; serves `daemon.sock`. Binary: `zc`. |
| **zcoms-sdk** | `github.com/Zouriel/zcoms-sdk` | Shared library every component imports. Packages: `ipc`, `agent`, `whatsapp`. Tags v0.1.0–v0.1.2. |
| **zcoms-bridge** | `github.com/Zouriel/zcoms-bridge` | Interactive agent-driving: per-user session state, locations, resume, file uploads. Subscribes to daemon `"bridge"` stream. |
| **zcoms-errands** | `github.com/Zouriel/zcoms-errands` | Autonomous interviewer→producer agents at a contact. Serves `errands.sock`; subscribes to `"errands"` stream. |
| **zcoms-team** | `github.com/Zouriel/zcoms-team` | Team coordination/standups; SQLite + PDF reports; `gh`-based GitHub Projects sync (disabled at runtime). Serves `team.sock`. |
| **zcoms-triage** | `github.com/Zouriel/zcoms-triage` | Scheduled AI digest of unread TG/WA. Stateless polling IPC client; no socket of its own. |
| **zcoms-skill** | (Claude Skill, not Go) | Agent Skill wrapping the `zc` CLI — `skills/zc/SKILL.md`. |
| **zc-commerce** | `github.com/Zouriel/zc-commerce` | (NEW) Commerce control plane — serves `commerce.sock`. **Not installed/running yet.** |
| **zcoms-commerce-runtime** | `github.com/Zouriel/zcoms-commerce-runtime` | (NEW) Commerce data plane — VPS bot-hosting runtime (Docker/Postgres/MinIO/Caddy). **Not installed/running yet.** |

All GitHub repos are under the `Zouriel` account. `zc-commerce` and
`zcoms-commerce-runtime` are **private**; the others are public.

## Main app (`zcoms`) layout

- **`cmd/`** — Cobra subcommands (one file each). Core: `tg` (login/auth/send/send-file/ask/chat/tail/chats/download), `wa status`. Component-gated: `init agent`, `allowlist`, `agents`, `locations`, `triage`, `errand`, `team`, `commerce`. Infra: `install`, `uninstall`.
- **`internal/agent/`** — the heart: `daemon.go` (`RunDaemon`), `daemon_ipc.go`/`ipc.go` (socket protocol), `runner.go`/`runner_codex.go` (spawn Claude/Codex), `triage*.go`, `errand*.go`, `settings.go`, `agents.go`, `subscribe.go`, `claims.go`, `sessions.go`.
- **`internal/components/`** — `components.go`: the canonical component registry (Bridge, Triage, Errands, Team, Commerce) with dependency closure + `components.json` (de)serialization.
- **`internal/config/`** — config dir resolution (`DefaultAppDir` → `~/.config/zcoms`), `config.json`, credentials (`TG_API_ID`/`TG_API_HASH`).
- **`internal/tdlib/`** — TDLib FFI (login, chats, media, updates, unread). Only place with cgo.
- **`internal/authentication/`**, **`internal/whatsapp/`** — auth-state helpers; WhatsApp sidecar stubs.
- **`scripts/`** — `build-linux-portable.sh` (bundles TDLib+OpenSSL → `dist/zcoms-linux-x64.tar.gz`), `package.sh`, systemd unit templates.
- **`whatsapp-bridge/`** — Node.js Baileys sidecar (optional).
- **`docs/`** — `AGENT-BRIDGE.md` (see below) and this file.

## Component / daemon / IPC model

- The **daemon** (`zc init agent`) owns the single TDLib session (non-shareable lock) and listens on `daemon.sock` (newline-delimited JSON RPC: `send`/`ask`/`chat`/`read`/`errand_*`). When the daemon runs, plain `zc tg …` commands route through it; otherwise they open their own TDLib client.
- **Components subscribe** to the daemon's event stream by role (`bridge`, `errands`) and reply via the IPC client. Some also **serve their own socket** for command-style input: `errands.sock`, `team.sock`, `commerce.sock` — all speaking `{text, actor} → {ok, reply, error}`.
- **`zcoms-sdk`** is the shared contract:
  - `ipc`: `Client` (`Send`, `SendFile`, `Read`, `Unread`, `MarkRead`, `Resolve`, `Subscribe`), `DefaultSocketPath()`, protocol types.
  - `agent`: `DefaultAppDir`, config loaders (`LoadOrSeed{Locations,Allowlist,Settings,Agents}`), role model (`RoleRead<RoleConfirm<RoleEdit<RoleFull`), `Backend` (Claude/Codex), `RunAgent`, `ListSessions`, `TriageSettings`/`NextRun`, `Claims`, triage-brain flock.
  - `whatsapp`: Baileys sidecar client over `wa.sock`.

## Build & release model

- All components are **pure Go** (`go 1.25.6`, no cgo). Only the core `zc` binary links TDLib. Triage pins SDK v0.1.1; others v0.1.2.
- `zc install <component>`:
  1. Seeds config files, marks `components.json`.
  2. Downloads a **prebuilt release binary** from `Zouriel/<repo>` (mapping in `cmd/install.go` `componentArtifact`) into `~/.local/bin/`.
  3. Installs + enables a systemd **user** service (each repo ships `contrib/<name>.service`).
- The bridge install also ensures the daemon service exists first.

## Config files (`~/.config/zcoms/`, mode 0600)

| File | Governs |
|---|---|
| `config.json` | TDLib dir, `auth_state`, phone number. *(Note: daemon-written; auth_state can be stale — check daemon log for real status.)* |
| `components.json` | `{installed: {bridge,errands,team,triage: true}}` — commerce NOT installed. |
| `agents.json` | Backend per task: `bridge→claude`, `errands/triage/chat→codex`, default `claude`. |
| `agent-allowlist.json` | Telegram users allowed to drive the bridge + their role + locations. |
| `agent-locations.json` | Project name → filesystem path (where agent sessions start). |
| `agent-settings.json` | `main_user`, auto-reply, triage schedule (hourly), WhatsApp sidecar config. |

## Runtime topology (as observed 2026-06-24)

Five systemd **user** services, all running; live sockets `daemon.sock`,
`errands.sock`, `team.sock`, `wa.sock` (WhatsApp sidecar paired & enabled).

| Service | ExecStart |
|---|---|
| zcoms-daemon | `~/.local/bin/zc init agent` (owns TDLib session) |
| zcoms-bridge | `~/.local/bin/zcoms-bridge` |
| zcoms-errands | `~/.local/bin/zcoms-errands` |
| zcoms-team | `~/.local/bin/zcoms-team` |
| zcoms-triage | `~/.local/bin/zcoms-triage` |

All `Restart=on-failure`; bridge/errands/team/triage depend on the daemon. No
Docker / Caddy — bare user-space binaries over local IPC. Backends present:
`~/.local/bin/claude` and `/usr/bin/codex`.

> ⚠️ Restarting the daemon kills its child component/bridge sessions. Don't
> restart it from inside a bridge session.

## Agent / AI documentation (important)

There is **no `AGENTS.md`, `CLAUDE.md`, or repo-level `agents.json`** anywhere in
the ecosystem. The two agent-facing docs are:

1. **`zcoms/docs/AGENT-BRIDGE.md`** — daemon session-routing, role model, location
   caps, auto-reply/triage pipeline, WhatsApp sidecar, security model
   ("allow-list ≈ shell access; roles gate writes, not reads").
2. **`zcoms-skill/skills/zc/SKILL.md`** — Skill manual: when to use `zc tg send`
   (done/blocker) vs `ask` (one decision) vs `chat` (conversation), plus
   file send/download and WhatsApp.

(`agents.json` exists only as **runtime config** in `~/.config/zcoms/`, not in any repo.)

## Commerce status (the in-progress work)

- `zc-commerce` + `zcoms-commerce-runtime` were scaffolded and pushed (private).
- Integration into `zc` lives on branch **`commerce-integration`** (not merged to
  `master`): `cmd/commerce.go`, registry entry, install mapping, post-install hints.
- **Not installed/running**: no `commerce.sock`, no service, and no prebuilt
  release artifact exists for `zc install commerce` to fetch. Go is **not
  installed** on this machine, so binaries can't be built here.
- Architectural note: the runtime is the one component designed for a VPS
  (Docker + Postgres + MinIO + Caddy), unlike every existing user-space component.
