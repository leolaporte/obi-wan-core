package r1

import "encoding/json"

// Frame is the top-level envelope for all OpenClaw gateway messages.
// It is a discriminated union by Type: exactly one of "req", "res", or "event".
// Fields not relevant to the chosen type are omitted in marshaling.
type Frame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	OK      *bool           `json:"ok,omitempty"`
	Error   *ErrorShape     `json:"error,omitempty"`
	Event   string          `json:"event,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Seq     *uint64         `json:"seq,omitempty"`
}

// ErrorShape mirrors OpenClaw's ErrorShape schema.
type ErrorShape struct {
	Code         string          `json:"code"`
	Message      string          `json:"message"`
	Details      json.RawMessage `json:"details,omitempty"`
	Retryable    *bool           `json:"retryable,omitempty"`
	RetryAfterMs *uint64         `json:"retryAfterMs,omitempty"`
}

// Error codes matching openclaw/src/gateway/protocol/schema/error-codes.ts.
// Only the ones the shim emits are defined here; grow as needed.
const (
	ErrCodeInvalidRequest = "INVALID_REQUEST"
	ErrCodeUnauthorized   = "UNAUTHORIZED"
	ErrCodeUnknownMethod  = "UNKNOWN_METHOD"
	ErrCodeInternal       = "INTERNAL_ERROR"
)
