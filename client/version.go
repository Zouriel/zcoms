package client

// ProtocolVersion is the wire-contract version this client speaks. It is a
// constant owned here in comms/client and advertised by the daemon on connect.
// Every tier above comms is built against a specific comms release; on start
// each binary checks the daemon's advertised version via Hello and exits loudly
// on a mismatch rather than talking a protocol it doesn't understand.
//
// Bump this only on a breaking change to the wire types in protocol.go. Additive,
// backward-compatible fields (new omitempty options) do not require a bump.
//
// v2: multi-transport. Requests/Events carry a Transport tag (absent = telegram)
// and an Address; the daemon routes sends through a transport registry and
// exposes per-connector status (the `connectors` op). The bump is a safety
// interlock: a v2 caller may ask the daemon to send on WhatsApp/Instagram, which
// a v1 (Telegram-only) daemon would silently misroute — so they must match.
const ProtocolVersion = 2
