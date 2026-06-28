package client

// ProtocolVersion is the wire-contract version this client speaks. It is a
// constant owned here in comms/client and advertised by the daemon on connect.
// Every tier above comms is built against a specific comms release; on start
// each binary checks the daemon's advertised version via Hello and exits loudly
// on a mismatch rather than talking a protocol it doesn't understand.
//
// Bump this only on a breaking change to the wire types in protocol.go. Additive,
// backward-compatible fields (new omitempty options) do not require a bump.
const ProtocolVersion = 1
