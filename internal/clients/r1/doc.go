// Package r1 implements the Rabbit R1 gateway shim: a minimal subset of
// OpenClaw's gateway WebSocket wire protocol, just enough for the R1 to
// connect to obi-wan-core believing it has reached an OpenClaw gateway.
//
// Incoming voice transcripts become core.Turn values routed through the
// shared dispatcher, the same way telegram and watch clients do. Replies
// are pushed back to the R1 as node.invoke.request events.
//
// See ~/Obsidian/lgl/AI/Research/2026-04-11 OpenClaw Gateway WebSocket
// Protocol (for R1 shim).md for the protocol spec this shim implements.
package r1
