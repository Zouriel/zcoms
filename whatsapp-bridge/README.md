# TgCli WhatsApp bridge (Baileys sidecar)

A small standalone Node service that owns a WhatsApp Web (multi-device) session
and exposes **unread / read / send** to the Go `tg` daemon over a Unix-domain
socket. With it running and enabled, triage digests and `interact triage` cover
WhatsApp alongside Telegram.

It is **optional and off by default** — if you never enable it, `tg` behaves
exactly as a Telegram-only build.

## ⚠️ ToS / ban risk — read this first

Baileys is an **unofficial** WhatsApp client. Using it **violates WhatsApp's
Terms of Service**, and the paired phone number carries a real (if low, for
read-only polling) risk of being banned.

Recommended precautions:

- Pair a **secondary number**, not your primary one.
- At minimum, enable **two-step verification** on the number.
- Keep volume low. The sidecar stays low-footprint: it does not flip your
  presence online (`markOnlineOnConnect: false`) and only sends when you tell it
  to via `interact triage`.

## Install

Requires Node ≥ 20.

```sh
cd whatsapp-bridge
npm install
```

## Pair (first run)

Run it in a terminal. On first run it prints a QR code:

```sh
node index.js
```

On your phone: **WhatsApp → Settings → Linked devices → Link a device**, then
scan the QR. Auth is saved to `~/.config/tg/wa-auth/` so subsequent runs connect
without a QR. (If you ever get "logged out", delete that folder and re-pair.)

## Run

```sh
node index.js
```

It listens on the Unix socket (default `~/.config/tg/wa.sock`) and logs
connection drops/reconnects. Override paths with env vars:

| Env       | Default                      | Meaning                       |
|-----------|------------------------------|-------------------------------|
| `WA_SOCK` | `~/.config/tg/wa.sock`       | Unix socket the daemon talks to |
| `WA_AUTH` | `~/.config/tg/wa-auth`       | Baileys multi-device auth state |
| `WA_LOG`  | `info`                       | log level (`debug`/`info`/`warn`/`silent`) |

## Wire protocol

Newline-delimited JSON, one request line → one response line:

```
{"op":"status"}                                 -> {"ok":true,"ready":true,"chats":12}
{"op":"unread"}                                 -> {"ok":true,"unread":[{chatId,sender,text,ts,msgId}]}
{"op":"read","chatId":"..","msgIds":["..",..]}  -> {"ok":true}
{"op":"send","chatId":"..","text":".."}         -> {"ok":true}
```

`unread` returns **1:1 chats only** — groups, broadcasts, status/"stories" and
newsletters are filtered out. Before pairing, `unread`/`read`/`send` return
`{"ok":false,"error":"whatsapp not authenticated / not connected yet"}`;
`status` always answers.

### Unread is best-effort

WhatsApp has no "fetch my unread inbox" API, so the sidecar mirrors state from
the event stream: it tracks each chat's unread count and buffers recent incoming
text (up to 50 messages/chat). `unread` returns the newest *N* buffered messages
for each chat whose unread count is *N*. Messages received before the sidecar
started may not be available. This is sufficient for periodic triage.

## Run under systemd (`--user`)

A unit is provided at `../scripts/wa-bridge.service`. Pair once interactively
first (so the QR scan is done), then:

```sh
cp scripts/wa-bridge.service ~/.config/systemd/user/
# adjust WorkingDirectory / node path inside the unit if needed
systemctl --user daemon-reload
systemctl --user enable --now wa-bridge.service
```

`tg-daemon.service` is ordered `After=`/`Wants=` `wa-bridge.service`, so the
socket exists before triage runs. Check it end-to-end with:

```sh
tg wa status
```
