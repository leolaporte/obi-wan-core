package r1

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/core"
)

type fakeDispatcher struct {
	lastTurn core.Turn
	reply    string
	err      error
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, turn core.Turn) (*core.Reply, error) {
	f.lastTurn = turn
	if f.err != nil {
		return nil, f.err
	}
	return &core.Reply{Text: f.reply}, nil
}

func TestHandleMethod_SessionsSend(t *testing.T) {
	d := &fakeDispatcher{reply: "hello back"}
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: d,
		Channel:    "r1",
		DeviceID:   "dev-1",
	})
	params := json.RawMessage(`{"text":"hello"}`)
	payload, errShape := m.Handle(context.Background(), MethodSessionsSend, params)
	if errShape != nil {
		t.Fatalf("handler errored: %+v", errShape)
	}
	if d.lastTurn.Channel != "r1" || d.lastTurn.UserID != "dev-1" || d.lastTurn.Message != "hello" {
		t.Errorf("bad turn: %+v", d.lastTurn)
	}
	// Payload should carry the reply text.
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if got["text"] != "hello back" {
		t.Errorf("bad payload: %+v", got)
	}
}

func TestHandleMethod_ChatSendAliasesSessionsSend(t *testing.T) {
	d := &fakeDispatcher{reply: "pong"}
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: d, Channel: "r1", DeviceID: "dev-1",
	})
	payload, errShape := m.Handle(context.Background(), MethodChatSend, json.RawMessage(`{"text":"ping"}`))
	if errShape != nil {
		t.Fatalf("chat.send: %+v", errShape)
	}
	if d.lastTurn.Message != "ping" {
		t.Errorf("dispatcher not called: %+v", d.lastTurn)
	}
	if payload == nil {
		t.Error("expected non-nil payload")
	}
}

func TestHandleMethod_AccessDenied(t *testing.T) {
	d := &fakeDispatcher{err: core.ErrAccessDenied}
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: d, Channel: "r1", DeviceID: "dev-1",
	})
	_, errShape := m.Handle(context.Background(), MethodSessionsSend, json.RawMessage(`{"text":"x"}`))
	if errShape == nil || errShape.Code != ErrCodeUnauthorized {
		t.Fatalf("want UNAUTHORIZED, got %+v", errShape)
	}
}

func TestHandleMethod_EmptyText(t *testing.T) {
	d := &fakeDispatcher{reply: "ignored"}
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: d, Channel: "r1", DeviceID: "dev-1",
	})
	_, errShape := m.Handle(context.Background(), MethodSessionsSend, json.RawMessage(`{"text":""}`))
	if errShape == nil || errShape.Code != ErrCodeInvalidRequest {
		t.Fatalf("want INVALID_REQUEST, got %+v", errShape)
	}
}

func TestHandleMethod_NodePendingPullReturnsEmpty(t *testing.T) {
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: &fakeDispatcher{}, Channel: "r1", DeviceID: "dev-1",
	})
	payload, errShape := m.Handle(context.Background(), MethodNodePendingPull, json.RawMessage(`{}`))
	if errShape != nil {
		t.Fatalf("pending.pull: %+v", errShape)
	}
	var got struct {
		NodeID  string        `json:"nodeId"`
		Actions []interface{} `json:"actions"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if got.NodeID != "dev-1" {
		t.Errorf("bad nodeId: %q", got.NodeID)
	}
	if len(got.Actions) != 0 {
		t.Errorf("expected empty actions, got %d", len(got.Actions))
	}
}

func TestHandleMethod_NodePendingAckReturnsEmpty(t *testing.T) {
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: &fakeDispatcher{}, Channel: "r1", DeviceID: "dev-1",
	})
	payload, errShape := m.Handle(context.Background(), MethodNodePendingAck, json.RawMessage(`{}`))
	if errShape != nil {
		t.Fatalf("pending.ack: %+v", errShape)
	}
	var got struct {
		NodeID         string   `json:"nodeId"`
		AckedIds       []string `json:"ackedIds"`
		RemainingCount int      `json:"remainingCount"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if got.NodeID != "dev-1" {
		t.Errorf("bad nodeId: %q", got.NodeID)
	}
	if got.AckedIds == nil || len(got.AckedIds) != 0 {
		t.Errorf("expected empty ackedIds, got %v", got.AckedIds)
	}
	if got.RemainingCount != 0 {
		t.Errorf("expected remainingCount=0, got %d", got.RemainingCount)
	}
}

func TestHandleMethod_UnknownMethod(t *testing.T) {
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: &fakeDispatcher{}, Channel: "r1", DeviceID: "dev-1",
	})
	_, errShape := m.Handle(context.Background(), "nope.nope", json.RawMessage(`{}`))
	if errShape == nil || errShape.Code != ErrCodeUnknownMethod {
		t.Fatalf("want UNKNOWN_METHOD, got %+v", errShape)
	}
}

// Silence unused warning if test is edited down in the future.
var _ = errors.New
var _ = time.Now
