package r1

import "time"

// ProtocolVersion is the OpenClaw gateway wire-protocol version this shim speaks.
// Matches PROTOCOL_VERSION in openclaw/src/gateway/protocol/schema/protocol-schemas.ts.
const ProtocolVersion = 3

// TickInterval is the keepalive cadence. Matches TICK_INTERVAL_MS in openclaw.
const TickInterval = 30 * time.Second

// MaxPayloadBytes is the per-frame size limit. Matches MAX_PAYLOAD_BYTES in openclaw.
const MaxPayloadBytes = 25 * 1024 * 1024

// Frame type discriminators.
const (
	FrameTypeReq   = "req"
	FrameTypeRes   = "res"
	FrameTypeEvent = "event"
)

// Event names emitted by the shim.
const (
	EventConnectChallenge = "connect.challenge"
	EventTick             = "tick"
	EventVoicewakeChanged = "voicewake.changed"
	EventNodeInvokeReq    = "node.invoke.request"
)

// Method names the shim handles. This is intentionally a minimal subset;
// unknown methods get an UNKNOWN_METHOD error response.
const (
	MethodConnect         = "connect"
	MethodSessionsSend    = "sessions.send"
	MethodChatSend        = "chat.send"
	MethodNodePendingPull = "node.pending.pull"
	MethodNodePendingAck  = "node.pending.ack"
	MethodNodeInvokeResult = "node.invoke.result"
	MethodNodeEvent       = "node.event"
	MethodVoicewakeGet    = "voicewake.get"
	MethodWake            = "wake"
	MethodLastHeartbeat   = "last-heartbeat"
	MethodSetHeartbeats   = "set-heartbeats"
	MethodTalkSpeak       = "talk.speak"
	MethodTalkConfig      = "talk.config"
)

// RoleNode is the only role this shim accepts. OpenClaw has two roles
// (operator, node); the R1 is always node.
const RoleNode = "node"
