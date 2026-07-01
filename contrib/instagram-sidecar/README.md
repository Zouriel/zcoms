# zcoms Instagram sidecar

A small FastAPI service that wraps [instagrapi](https://github.com/subzeroid/instagrapi)
(the private Instagram API) so the zcoms daemon can send and receive Instagram
DMs through the standard `Transport` interface. The Go side lives in
`internal/comms/instagram/`; this is the only network sidecar (WhatsApp is
in-process via whatsmeow, so the total sidecar count stays one).

## ⚠️ Read this first

Instagram has **no official API**. This uses the private mobile API, which
**violates Instagram's Terms of Service** and carries a **real ban risk** — the
most fragile transport by nature of the platform. Mitigations:

- Use a **secondary account** you can afford to lose, not your main.
- Run behind a **residential proxy** (set `proxy` in `instagram.json`).
- Keep polling **gentle** (`poll_seconds` ≥ 20; default 45).
- Expect **session expiry** and periodic **challenges** (SMS/email code).

## Run

```sh
./run.sh            # docker compose up --build, waits for /health
./run.sh --local    # local Python venv instead of Docker
```

It listens on `127.0.0.1:8099` (loopback only). To use a different address,
start it elsewhere and set `ZC_INSTAGRAM_SIDECAR=http://host:port` for the
daemon.

## Configure the account (daemon side)

Seed `~/.config/zcoms/instagram.json` (mode 0600):

```json
{
  "username": "your_handle",
  "password": "…",
  "proxy": "http://user:pass@host:port",
  "poll_seconds": 45
}
```

The daemon then restores the encrypted session if one exists, otherwise logs in
(parking on `needs_2fa` / `needs_challenge` in the console connectors page until
you enter the code). The session is dumped from the sidecar and stored
**encrypted** at `~/.config/zcoms/instagram/session.enc` (AES-256-GCM, key in a
sibling `session.key`), so restarts rarely re-login.

## Endpoints

See the module docstring in `app.py`. All JSON, loopback only. The Go client in
`internal/comms/instagram/client.go` is the source of truth for the shapes.

## Notes

- Challenge resolution varies by account and instagrapi version; the 2FA path
  (authenticator/SMS `verification_code`) is the well-trodden one. If a challenge
  loops, resolve it once in a throwaway instagrapi script to prime the session,
  then restore that session here.
- Single account, single process: one daemon owns one Instagram login.
