// TgCli WhatsApp bridge — a small Baileys sidecar.
//
// It owns its own WhatsApp Web (multi-device) session and exposes three
// operations to the Go daemon over a Unix-domain socket, mirroring the tdlib
// calls triage already uses:
//
//   {"op":"unread"}                                  -> {"ok":true,"unread":[{chatId,sender,text,ts,msgId}]}
//   {"op":"read","chatId":"..","msgIds":["..",..]}   -> {"ok":true}
//   {"op":"send","chatId":"..","text":".."}          -> {"ok":true}
//
// Protocol: newline-delimited JSON, one request line -> one response line.
//
// ⚠️  Baileys is an UNOFFICIAL WhatsApp client and using it violates WhatsApp's
//     Terms of Service; the paired number carries a ban risk. Prefer a secondary
//     number, enable 2FA, and keep volume low.

import { createServer } from 'node:net'
import { existsSync, unlinkSync, mkdirSync, writeFileSync, readFileSync } from 'node:fs'
import { homedir } from 'node:os'
import { join, dirname, basename } from 'node:path'

import makeWASocket, {
  useMultiFileAuthState,
  DisconnectReason,
  fetchLatestBaileysVersion,
  downloadMediaMessage,
} from 'baileys'
import pino from 'pino'
import qrcode from 'qrcode-terminal'

const HOME = homedir()
const SOCK_PATH = process.env.WA_SOCK || join(HOME, '.config', 'tg', 'wa.sock')
const AUTH_DIR = process.env.WA_AUTH || join(HOME, '.config', 'tg', 'wa-auth')
// Where incoming media is downloaded so triage/the agent can open or forward it.
const MEDIA_DIR = process.env.WA_MEDIA || join(HOME, '.config', 'tg', 'wa-media')
// Disk snapshot of the chat mirror, so chats/unread survive a sidecar restart
// (WhatsApp only re-pushes full history on a fresh QR link, never on reconnect).
const STORE_PATH = process.env.WA_STORE || join(HOME, '.config', 'tg', 'wa-store.json')
// Per-chat ring buffer cap for incoming text we keep available to `unread`.
const MAX_BUFFER_PER_CHAT = 50

const log = pino({ level: process.env.WA_LOG || 'info' })

// ---- in-memory state ------------------------------------------------------
// We can't ask WhatsApp for "my unread inbox" directly, so we mirror it:
//   chatMeta:  jid -> { unreadCount, name }
//   buffered:  jid -> [{ id, text, ts, sender, key, file }]  (newest last)
// Both are persisted to STORE_PATH and reloaded on startup.
const chatMeta = new Map()
const buffered = new Map()
let ready = false // true once the socket reaches "open"
let lastQRAscii = '' // ASCII QR for the current pairing prompt, '' once paired

// ---- persistence ----------------------------------------------------------
function saveStore() {
  try {
    mkdirSync(dirname(STORE_PATH), { recursive: true })
    const data = { chatMeta: [...chatMeta.entries()], buffered: [...buffered.entries()] }
    writeFileSync(STORE_PATH, JSON.stringify(data))
  } catch (e) {
    log.warn({ e: String(e?.message || e) }, 'wa store save failed')
  }
}

let saveTimer = null
function scheduleSave() {
  if (saveTimer) return
  saveTimer = setTimeout(() => {
    saveTimer = null
    saveStore()
  }, 1500)
}

function loadStore() {
  try {
    if (!existsSync(STORE_PATH)) return
    const data = JSON.parse(readFileSync(STORE_PATH, 'utf8'))
    for (const [k, v] of data.chatMeta || []) chatMeta.set(k, v)
    for (const [k, v] of data.buffered || []) buffered.set(k, v)
    log.info({ chats: chatMeta.size }, 'restored wa store from disk')
  } catch (e) {
    log.warn({ e: String(e?.message || e) }, 'wa store load failed (starting empty)')
  }
}

// A 1:1 chat is either a classic phone JID (@s.whatsapp.net) or a privacy
// LinkedID JID (@lid) — modern WhatsApp delivers many DMs via @lid. Groups
// (@g.us), broadcast/status, and newsletters are intentionally excluded.
const isDirectChat = (jid) => typeof jid === 'string' && (jid.endsWith('@s.whatsapp.net') || jid.endsWith('@lid'))
const numberOf = (jid) => (jid || '').split('@')[0]

// unwrap peels WhatsApp envelopes that nest the real content (disappearing
// messages, view-once, device-sent, edited), so text inside them is still found.
function unwrap(msg) {
  let m = msg || {}
  for (let i = 0; i < 5; i++) {
    const inner =
      m.ephemeralMessage?.message ||
      m.viewOnceMessage?.message ||
      m.viewOnceMessageV2?.message ||
      m.viewOnceMessageV2Extension?.message ||
      m.documentWithCaptionMessage?.message ||
      m.deviceSentMessage?.message ||
      m.editedMessage?.message
    if (!inner) break
    m = inner
  }
  return m
}

// mediaLabel returns a short "[type]" tag for non-text messages so triage still
// sees that something arrived (mirrors how Telegram triage labels media).
function mediaLabel(msg) {
  if (msg.imageMessage) return '[image]'
  if (msg.videoMessage) return '[video]'
  if (msg.audioMessage) return msg.audioMessage.ptt ? '[voice note]' : '[audio]'
  if (msg.documentMessage) return '[document]'
  if (msg.stickerMessage) return '[sticker]'
  if (msg.contactMessage || msg.contactsArrayMessage) return '[contact]'
  if (msg.locationMessage || msg.liveLocationMessage) return '[location]'
  if (msg.pollCreationMessage || msg.pollCreationMessageV3) return '[poll]'
  return ''
}

function messageText(m) {
  const msg = unwrap(m.message)
  const text =
    msg.conversation ||
    msg.extendedTextMessage?.text ||
    msg.imageMessage?.caption ||
    msg.videoMessage?.caption ||
    msg.documentMessage?.caption ||
    ''
  return text || mediaLabel(msg)
}

// mediaInfo describes a downloadable attachment on a message (or null for
// text-only), giving the file extension we'll save it under.
function mediaInfo(m) {
  const msg = unwrap(m.message || {})
  let kind = '',
    mimetype = '',
    fileName = '',
    defExt = 'bin'
  if (msg.imageMessage) { kind = 'image'; mimetype = msg.imageMessage.mimetype || ''; defExt = 'jpg' }
  else if (msg.videoMessage) { kind = 'video'; mimetype = msg.videoMessage.mimetype || ''; defExt = 'mp4' }
  else if (msg.audioMessage) { kind = 'audio'; mimetype = msg.audioMessage.mimetype || ''; defExt = msg.audioMessage.ptt ? 'ogg' : 'm4a' }
  else if (msg.stickerMessage) { kind = 'sticker'; mimetype = msg.stickerMessage.mimetype || ''; defExt = 'webp' }
  else if (msg.documentMessage) { kind = 'document'; mimetype = msg.documentMessage.mimetype || ''; fileName = msg.documentMessage.fileName || ''; defExt = 'bin' }
  else return null

  let ext = defExt
  if (fileName && fileName.includes('.')) ext = fileName.split('.').pop().toLowerCase()
  else if (mimetype) {
    const sub = mimetype.split(';')[0].split('/')[1]
    if (sub) ext = sub.replace('jpeg', 'jpg').replace('mpeg', 'mp3')
  }
  return { kind, ext }
}

// downloadMedia fetches an incoming attachment to MEDIA_DIR and records its path
// on the buffered entry so `unread` can hand the local file to the agent.
async function downloadMedia(sock, m, entry) {
  try {
    const info = mediaInfo(m)
    if (!info || !entry) return
    const buf = await downloadMediaMessage(m, 'buffer', {}, { logger: log, reuploadRequest: sock.updateMediaMessage })
    if (!buf || !buf.length) return
    const safeId = String(m.key?.id || Date.now()).replace(/[^A-Za-z0-9_-]/g, '')
    const dest = join(MEDIA_DIR, `${safeId}.${info.ext}`)
    writeFileSync(dest, buf)
    entry.file = dest
    scheduleSave()
    log.info({ file: dest, bytes: buf.length, kind: info.kind }, 'wa media downloaded')
  } catch (e) {
    log.warn({ e: String(e?.message || e) }, 'wa media download failed')
  }
}

// rememberMessage buffers an incoming 1:1 message and returns its entry (or the
// existing one on a dedupe hit), so callers can attach a downloaded media path.
function rememberMessage(m) {
  if (m.key?.fromMe) return null
  const jid = m.key?.remoteJid
  if (!isDirectChat(jid)) return null // 1:1 only — skip groups/broadcast/status/newsletter
  const text = messageText(m)
  if (!text) return null // skip reactions / receipts / system messages with no content

  const arr = buffered.get(jid) || []
  const existing = arr.find((e) => e.id === m.key.id)
  if (existing) return existing // de-dupe (history + live overlap)

  const entry = {
    id: m.key.id,
    text,
    ts: Number(m.messageTimestamp) || Math.floor(Date.now() / 1000),
    sender: m.pushName || numberOf(jid),
    key: m.key,
    file: '', // set by downloadMedia for attachments
  }
  arr.push(entry)
  while (arr.length > MAX_BUFFER_PER_CHAT) arr.shift()
  buffered.set(jid, arr)
  scheduleSave()
  return entry
}

function noteChat(c) {
  if (!c?.id || !isDirectChat(c.id)) return
  const prev = chatMeta.get(c.id) || { unreadCount: 0, name: '' }
  if (typeof c.unreadCount === 'number') prev.unreadCount = c.unreadCount
  if (c.name || c.notify) prev.name = c.name || c.notify
  chatMeta.set(c.id, prev)
  scheduleSave()
}

// ---- request handlers -----------------------------------------------------
function handleUnread() {
  const out = []
  for (const [jid, meta] of chatMeta) {
    const n = meta.unreadCount || 0
    if (n <= 0) continue
    const arr = buffered.get(jid) || []
    // Best effort: the newest `n` buffered incoming messages for this chat.
    for (const e of arr.slice(-n)) {
      out.push({
        chatId: jid,
        sender: e.sender || meta.name || numberOf(jid),
        text: e.text,
        ts: e.ts,
        msgId: e.id,
        file: e.file || '',
      })
    }
  }
  return { ok: true, unread: out }
}

// forget drops messages from our local unread mirror (buffer + count). It does
// NOT touch WhatsApp, so it leaves no read receipts / blue ticks.
function forget(chatId, msgIds) {
  const arr = buffered.get(chatId) || []
  buffered.set(chatId, arr.filter((x) => !msgIds.includes(x.id)))
  const meta = chatMeta.get(chatId)
  if (meta) meta.unreadCount = Math.max(0, (meta.unreadCount || 0) - msgIds.length)
  scheduleSave()
}

async function handleRead(sock, { chatId, msgIds }) {
  if (!chatId || !Array.isArray(msgIds) || msgIds.length === 0) {
    return { ok: false, error: 'read requires chatId and msgIds' }
  }
  const arr = buffered.get(chatId) || []
  const keys = msgIds.map((id) => {
    const e = arr.find((x) => x.id === id)
    return e ? e.key : { remoteJid: chatId, id, fromMe: false }
  })
  await sock.readMessages(keys) // sends WhatsApp read receipts (blue ticks)
  forget(chatId, msgIds)
  return { ok: true }
}

// dismiss is "mark triaged" WITHOUT sending read receipts: it only clears our
// local mirror so the same messages aren't re-triaged next pass. Used for
// read-only triage on a personal number, so reading leaves no blue ticks.
function handleDismiss({ chatId, msgIds }) {
  if (!chatId || !Array.isArray(msgIds) || msgIds.length === 0) {
    return { ok: false, error: 'dismiss requires chatId and msgIds' }
  }
  forget(chatId, msgIds)
  return { ok: true }
}

async function handleSend(sock, { chatId, text }) {
  if (!chatId || !text) return { ok: false, error: 'send requires chatId and text' }
  await sock.sendMessage(chatId, { text })
  return { ok: true }
}

// outboundContent builds the Baileys message payload for a local file, picking
// image/video/audio/document by extension (unknown types go as a document).
function outboundContent(path, caption) {
  const buf = readFileSync(path)
  const name = basename(path)
  const ext = (name.split('.').pop() || '').toLowerCase()
  const cap = caption || undefined
  if (['jpg', 'jpeg', 'png', 'webp', 'gif'].includes(ext)) return { image: buf, caption: cap }
  if (['mp4', 'mov', 'mkv', '3gp', 'webm'].includes(ext)) return { video: buf, caption: cap }
  if (['mp3', 'm4a', 'aac', 'wav'].includes(ext)) return { audio: buf, mimetype: 'audio/mp4' }
  if (['ogg', 'opus'].includes(ext)) return { audio: buf, mimetype: 'audio/ogg; codecs=opus', ptt: true }
  if (ext === 'pdf') return { document: buf, fileName: name, mimetype: 'application/pdf', caption: cap }
  return { document: buf, fileName: name, mimetype: 'application/octet-stream', caption: cap }
}

async function handleSendFile(sock, { chatId, path, text }) {
  if (!chatId || !path) return { ok: false, error: 'sendfile requires chatId and path' }
  if (!existsSync(path)) return { ok: false, error: `file not found: ${path}` }
  await sock.sendMessage(chatId, outboundContent(path, text))
  return { ok: true }
}

async function dispatch(sock, req) {
  if (req.op === 'status') {
    // Always answerable, even before pairing — used by `tg wa status`.
    return { ok: true, ready, chats: chatMeta.size }
  }
  if (req.op === 'qr') {
    // Answerable before pairing — used by `tg wa login` to render the QR.
    return { ok: true, ready, qr: ready ? '' : lastQRAscii }
  }
  if (!ready) return { ok: false, error: 'whatsapp not authenticated / not connected yet' }
  switch (req.op) {
    case 'unread':
      return handleUnread()
    case 'read':
      return handleRead(sock, req)
    case 'dismiss':
      return handleDismiss(req)
    case 'send':
      return handleSend(sock, req)
    case 'sendfile':
      return handleSendFile(sock, req)
    default:
      return { ok: false, error: `unknown op: ${req.op}` }
  }
}

// ---- unix socket server ---------------------------------------------------
function startServer(getSock) {
  mkdirSync(dirname(SOCK_PATH), { recursive: true })
  if (existsSync(SOCK_PATH)) unlinkSync(SOCK_PATH) // clear a stale socket

  const server = createServer((conn) => {
    let buf = ''
    conn.on('data', async (chunk) => {
      buf += chunk.toString('utf8')
      let nl
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl).trim()
        buf = buf.slice(nl + 1)
        if (!line) continue
        let resp
        try {
          resp = await dispatch(getSock(), JSON.parse(line))
        } catch (err) {
          resp = { ok: false, error: String(err?.message || err) }
        }
        conn.write(JSON.stringify(resp) + '\n')
      }
    })
    conn.on('error', (e) => log.warn({ e: String(e) }, 'socket connection error'))
  })

  server.listen(SOCK_PATH, () => log.info({ SOCK_PATH }, 'listening on unix socket'))
  server.on('error', (e) => {
    log.error({ e: String(e) }, 'socket server error')
    process.exit(1)
  })
}

// ---- whatsapp connection --------------------------------------------------
async function connect() {
  mkdirSync(AUTH_DIR, { recursive: true })
  mkdirSync(MEDIA_DIR, { recursive: true })
  const { state, saveCreds } = await useMultiFileAuthState(AUTH_DIR)
  const { version } = await fetchLatestBaileysVersion()
  log.info({ version }, 'using baileys protocol version')

  const sock = makeWASocket({
    version,
    auth: state,
    logger: pino({ level: 'silent' }),
    markOnlineOnConnect: false, // stay low-footprint; don't flip presence on
    syncFullHistory: true, // pull history on link so triage can surface backlog
    // unread, not just messages that arrive live. Richest on a fresh QR pairing;
    // an already-linked device may need a re-link to backfill old history.
  })

  sock.ev.on('creds.update', saveCreds)

  sock.ev.on('connection.update', (u) => {
    const { connection, lastDisconnect, qr } = u
    if (qr) {
      console.log('\nScan this QR with WhatsApp → Linked devices → Link a device:\n')
      qrcode.generate(qr, { small: true })
      // Also stash an ASCII rendering so `tg wa login` can show it over the socket.
      qrcode.generate(qr, { small: true }, (s) => {
        lastQRAscii = s
      })
    }
    if (connection === 'open') {
      ready = true
      lastQRAscii = '' // paired — no QR to show anymore
      log.info('whatsapp connection open')
    }
    if (connection === 'close') {
      ready = false
      const code = lastDisconnect?.error?.output?.statusCode
      const loggedOut = code === DisconnectReason.loggedOut
      log.warn({ code, loggedOut }, 'connection closed')
      if (!loggedOut) setTimeout(() => start(true), 3000) // transient drop -> reconnect (reassigns currentSock)
      else log.error('logged out — delete the auth dir and re-pair (scan QR again)')
    }
  })

  // History sync on connect: seed chat metadata + recent messages.
  sock.ev.on('messaging-history.set', ({ chats, messages }) => {
    for (const c of chats || []) noteChat(c)
    for (const m of messages || []) {
      const entry = rememberMessage(m)
      if (entry && mediaInfo(m)) downloadMedia(sock, m, entry) // async, best-effort
    }
  })
  sock.ev.on('chats.upsert', (chats) => chats.forEach(noteChat))
  sock.ev.on('chats.update', (chats) => chats.forEach(noteChat))

  // Live messages.
  sock.ev.on('messages.upsert', ({ messages, type }) => {
    for (const m of messages) {
      // Privacy-safe diagnostic: envelope keys + capture result, no content.
      log.info(
        {
          type,
          fromMe: !!m.key?.fromMe,
          kind: isDirectChat(m.key?.remoteJid) ? '1:1' : 'other',
          dom: (m.key?.remoteJid || '').split('@')[1] || '',
          envelope: Object.keys(unwrap(m.message || {})),
          captured: !!messageText(m),
        },
        'msg.upsert',
      )
    }
    if (type !== 'notify') return
    for (const m of messages) {
      const entry = rememberMessage(m)
      const jid = m.key?.remoteJid
      if (m.key?.fromMe || !isDirectChat(jid)) continue
      if (entry && mediaInfo(m)) downloadMedia(sock, m, entry) // async, best-effort
      const meta = chatMeta.get(jid) || { unreadCount: 0, name: m.pushName || numberOf(jid) }
      meta.unreadCount = (meta.unreadCount || 0) + 1
      if (m.pushName) meta.name = m.pushName
      chatMeta.set(jid, meta)
      scheduleSave()
    }
  })

  return sock
}

// ---- main -----------------------------------------------------------------
let currentSock = null
loadStore() // restore chats/unread from the last run before serving
startServer(() => currentSock) // serve immediately; ops return "not authenticated" until open

// Flush the store on shutdown (systemd sends SIGTERM on restart) so the latest
// state isn't lost in the debounce window.
for (const sig of ['SIGTERM', 'SIGINT']) {
  process.on(sig, () => {
    saveStore()
    process.exit(0)
  })
}

// start (re)builds the socket and — crucially — points currentSock at the NEW
// one. Reconnects MUST go through here; otherwise dispatch keeps using the dead
// socket and every send/read fails with "Connection Closed" even though the new
// socket reports the connection open.
async function start(isRetry) {
  try {
    currentSock = await connect()
  } catch (e) {
    log.error({ e: String(e) }, 'failed to start whatsapp connection')
    if (!isRetry) process.exit(1)
  }
}
start(false)
