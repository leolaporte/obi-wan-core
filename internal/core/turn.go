// Package core holds the shared dispatcher and supporting types.
// Clients (Telegram, Watch, R1) submit Turns and receive Replies.
package core

import "time"

// Turn is one request from a client to the dispatcher.
// Clients construct a Turn and hand it to Dispatcher.Dispatch.
type Turn struct {
	Channel    string    // "telegram" | "watch" | "r1"
	UserID     string    // stable per-channel user identifier
	Message    string    // the text prompt
	ReceivedAt time.Time // when the client received it (for logging)
}

// Reply is the dispatcher's response to a Turn.
type Reply struct {
	Text string // the agent's textual response
}

// Message is a single turn in the conversation history, stored on disk
// and sent to the Messages API.
type Message struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"`
}
