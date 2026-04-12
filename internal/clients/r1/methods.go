package r1

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/core"
)

// MethodHandlerConfig is the per-connection context for method dispatch.
type MethodHandlerConfig struct {
	Dispatcher Dispatcher
	Channel    string // "r1"
	DeviceID   string // stable id for Turn.UserID and node.pending.pull responses
}

// MethodHandler routes request frames to the right per-method logic.
type MethodHandler struct {
	cfg MethodHandlerConfig
}

// NewMethodHandler constructs a MethodHandler.
func NewMethodHandler(cfg MethodHandlerConfig) *MethodHandler {
	return &MethodHandler{cfg: cfg}
}

// Handle runs one method call and returns either a response payload or an
// error shape. Exactly one is non-nil on successful dispatch.
func (m *MethodHandler) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *ErrorShape) {
	switch method {
	case MethodSessionsSend, MethodChatSend:
		return m.handleSend(ctx, params)
	case MethodNodePendingPull:
		return m.handleNodePendingPull()
	case MethodNodePendingAck:
		return m.handleNodePendingAck()
	case MethodNodeInvokeResult:
		// The R1 reports results back to us; we accept and discard — the
		// chat path uses sessions.send, so node.invoke.result is only for
		// non-chat commands which the shim does not currently initiate.
		return json.RawMessage(`{"ok":true}`), nil
	case MethodNodeEvent:
		return json.RawMessage(`{"ok":true}`), nil
	case MethodVoicewakeGet:
		return json.RawMessage(`{"triggers":[]}`), nil
	case MethodWake:
		return json.RawMessage(`{"ok":true}`), nil
	case MethodLastHeartbeat, MethodSetHeartbeats:
		// Application-level heartbeat bookkeeping — safe to stub.
		return json.RawMessage(`{"ok":true}`), nil
	default:
		return nil, &ErrorShape{Code: ErrCodeUnknownMethod, Message: "unknown method: " + method}
	}
}

// sendParams is the narrow view of sessions.send / chat.send the shim cares
// about. The R1 sends "message" (not "text") with a sessionKey and
// idempotencyKey. We accept both field names for flexibility.
type sendParams struct {
	Text    string `json:"text"`
	Message string `json:"message"`
}

type sendResponse struct {
	Text string `json:"text"`
}

func (m *MethodHandler) handleSend(ctx context.Context, raw json.RawMessage) (json.RawMessage, *ErrorShape) {
	var p sendParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &ErrorShape{Code: ErrCodeInvalidRequest, Message: "bad send params: " + err.Error()}
	}
	// R1 sends "message", OpenClaw webchat sends "text". Accept both.
	text := strings.TrimSpace(p.Message)
	if text == "" {
		text = strings.TrimSpace(p.Text)
	}
	if text == "" {
		return nil, &ErrorShape{Code: ErrCodeInvalidRequest, Message: "text required"}
	}
	// Use a detached context with a generous timeout for the dispatch.
	// The R1 may disconnect before claude -p finishes; if we used the
	// connection's context, exec.CommandContext would kill the subprocess.
	// A 2-minute timeout is enough for even cold-start dispatches.
	dispatchCtx, dispatchCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer dispatchCancel()
	reply, err := m.cfg.Dispatcher.Dispatch(dispatchCtx, core.Turn{
		Channel:    m.cfg.Channel,
		UserID:     m.cfg.DeviceID,
		Message:    text,
		ReceivedAt: time.Now(),
	})
	if errors.Is(err, core.ErrAccessDenied) {
		return nil, &ErrorShape{Code: ErrCodeUnauthorized, Message: "access denied"}
	}
	if err != nil {
		return nil, &ErrorShape{Code: ErrCodeInternal, Message: err.Error()}
	}
	buf, err := json.Marshal(sendResponse{Text: reply.Text})
	if err != nil {
		return nil, &ErrorShape{Code: ErrCodeInternal, Message: err.Error()}
	}
	return buf, nil
}

// nodePendingResponse matches openclaw's server-methods/nodes.ts:814-827.
type nodePendingResponse struct {
	NodeID  string `json:"nodeId"`
	Actions []any  `json:"actions"`
}

func (m *MethodHandler) handleNodePendingPull() (json.RawMessage, *ErrorShape) {
	// We are online-only — the pending queue is always empty. The R1 may
	// call this on reconnect as a defensive drain; return an empty list.
	buf, err := json.Marshal(nodePendingResponse{NodeID: m.cfg.DeviceID, Actions: []any{}})
	if err != nil {
		return nil, &ErrorShape{Code: ErrCodeInternal, Message: err.Error()}
	}
	return buf, nil
}

// nodeAckResponse matches openclaw's server-methods/nodes.ts node.pending.ack.
type nodeAckResponse struct {
	NodeID         string   `json:"nodeId"`
	AckedIds       []string `json:"ackedIds"`
	RemainingCount int      `json:"remainingCount"`
}

func (m *MethodHandler) handleNodePendingAck() (json.RawMessage, *ErrorShape) {
	buf, err := json.Marshal(nodeAckResponse{
		NodeID:         m.cfg.DeviceID,
		AckedIds:       []string{},
		RemainingCount: 0,
	})
	if err != nil {
		return nil, &ErrorShape{Code: ErrCodeInternal, Message: err.Error()}
	}
	return buf, nil
}
