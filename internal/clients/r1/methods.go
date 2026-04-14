package r1

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/core"
)

// EventPusher is a callback the method handler uses to push async events
// (like chat responses) back to the connected client after the initial
// ACK response has been sent.
type EventPusher func(event string, payload any)

// MethodHandlerConfig is the per-connection context for method dispatch.
type MethodHandlerConfig struct {
	Dispatcher Dispatcher
	Channel    string // "r1"
	DeviceID   string // stable id for Turn.UserID and node.pending.pull responses
	PushEvent  EventPusher
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
	case MethodTalkSpeak:
		return m.handleTalkSpeak(params)
	case MethodTalkConfig:
		return m.handleTalkConfig()
	default:
		slog.Warn("r1 unknown method called", "method", method, "params", string(params))
		return nil, &ErrorShape{Code: ErrCodeUnknownMethod, Message: "unknown method: " + method}
	}
}

// sendParams is the narrow view of sessions.send / chat.send the shim cares
// about. The R1 sends "message" (not "text") with a sessionKey and
// idempotencyKey. We accept both field names for flexibility.
type sendParams struct {
	Text           string `json:"text"`
	Message        string `json:"message"`
	SessionKey     string `json:"sessionKey"`
	IdempotencyKey string `json:"idempotencyKey"`
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

	// OpenClaw's chat.send returns an immediate ACK with {runId, status}
	// and then pushes the actual response as async "chat" events.
	// The R1 expects this pattern — it won't display anything from the
	// synchronous res payload.
	runID := p.IdempotencyKey
	if runID == "" {
		runID = "run-" + text[:min(len(text), 8)]
	}
	sessionKey := p.SessionKey
	if sessionKey == "" {
		sessionKey = "main"
	}

	// Dispatch claude -p in the background; push "chat" events when done.
	go func() {
		dispatchCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		reply, err := m.cfg.Dispatcher.Dispatch(dispatchCtx, core.Turn{
			Channel:    m.cfg.Channel,
			UserID:     m.cfg.DeviceID,
			Message:    text,
			ReceivedAt: time.Now(),
		})
		push := m.cfg.PushEvent
		if push == nil {
			slog.Warn("r1 chat.send completed without PushEvent wired; dropping result", "runId", runID, "err", err)
			return
		}
		if err != nil {
			push("chat", map[string]any{
				"runId":        runID,
				"sessionKey":   sessionKey,
				"seq":          1,
				"state":        "error",
				"errorMessage": err.Error(),
			})
			return
		}
		push("chat", map[string]any{
			"runId":      runID,
			"sessionKey": sessionKey,
			"seq":        0,
			"state":      "final",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "text", "text": reply.Text},
				},
			},
		})
	}()

	// Return the immediate ACK.
	ack, _ := json.Marshal(map[string]any{
		"runId":  runID,
		"status": "started",
	})
	return ack, nil
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

// handleTalkSpeak returns a fallback-eligible "talk_unconfigured" error.
// If the R1 firmware implements the same fallback as the OpenClaw Android
// app, it will use Android system TTS (which supports custom voices) to
// speak the text locally on-device. This lets us discover whether the R1
// calls talk.speak at all — check journal logs for this line.
func (m *MethodHandler) handleTalkSpeak(params json.RawMessage) (json.RawMessage, *ErrorShape) {
	slog.Info("r1 talk.speak called — returning fallback-eligible error to trigger device TTS", "params", string(params))
	return nil, &ErrorShape{
		Code:    "TALK_UNCONFIGURED",
		Message: "server-side TTS not configured; use device TTS",
		Details: rawJSON(map[string]any{
			"reason":           "talk_unconfigured",
			"fallbackEligible": true,
		}),
	}
}

// handleTalkConfig returns an empty/unconfigured response so the R1 knows
// to fall back to device-local TTS if it queries TTS capabilities.
func (m *MethodHandler) handleTalkConfig() (json.RawMessage, *ErrorShape) {
	slog.Info("r1 talk.config called — returning unconfigured")
	buf, _ := json.Marshal(map[string]any{
		"config": map[string]any{
			"talk": map[string]any{
				"provider": "",
			},
		},
	})
	return buf, nil
}
