package r1

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/core"
)

type fakeDispatcher struct {
	mu       sync.Mutex
	lastTurn core.Turn
	reply    string
	err      error
	done     chan struct{}
}

func newFakeDispatcher(reply string, err error) *fakeDispatcher {
	return &fakeDispatcher{reply: reply, err: err, done: make(chan struct{}, 1)}
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, turn core.Turn) (*core.Reply, error) {
	f.mu.Lock()
	f.lastTurn = turn
	f.mu.Unlock()
	defer func() {
		select {
		case f.done <- struct{}{}:
		default:
		}
	}()
	if f.err != nil {
		return nil, f.err
	}
	return &core.Reply{Text: f.reply}, nil
}

func (f *fakeDispatcher) waitForDispatch(t *testing.T) core.Turn {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher never called")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastTurn
}

type chatEvent struct {
	name    string
	payload map[string]any
}

func newChatCollector(buf int) (EventPusher, <-chan chatEvent) {
	ch := make(chan chatEvent, buf)
	push := func(event string, payload any) {
		m, _ := payload.(map[string]any)
		ch <- chatEvent{name: event, payload: m}
	}
	return push, ch
}

func waitChat(t *testing.T, ch <-chan chatEvent) chatEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no chat event pushed")
		return chatEvent{}
	}
}

// TestHandleMethod_SessionsSend verifies the async contract: immediate
// {runId, status:"started"} ACK synchronously, then a final "chat" event
// carrying the reply text once the dispatcher completes.
func TestHandleMethod_SessionsSend(t *testing.T) {
	d := newFakeDispatcher("hello back", nil)
	push, events := newChatCollector(2)
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: d,
		Channel:    "r1",
		DeviceID:   "dev-1",
		PushEvent:  push,
	})
	params := json.RawMessage(`{"text":"hello","idempotencyKey":"idem-1","sessionKey":"sess-a"}`)
	payload, errShape := m.Handle(context.Background(), MethodSessionsSend, params)
	if errShape != nil {
		t.Fatalf("handler errored: %+v", errShape)
	}

	// ACK is synchronous and carries runId + status=started.
	var ack map[string]any
	if err := json.Unmarshal(payload, &ack); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if ack["status"] != "started" {
		t.Errorf("expected status=started, got %v", ack["status"])
	}
	if ack["runId"] != "idem-1" {
		t.Errorf("expected runId=idem-1, got %v", ack["runId"])
	}

	// Dispatcher must be called with the Turn fields wired through.
	turn := d.waitForDispatch(t)
	if turn.Channel != "r1" || turn.UserID != "dev-1" || turn.Message != "hello" {
		t.Errorf("bad turn: %+v", turn)
	}

	// A final chat event should arrive with the reply text.
	ev := waitChat(t, events)
	if ev.name != "chat" {
		t.Fatalf("expected 'chat' event, got %q", ev.name)
	}
	if ev.payload["state"] != "final" {
		t.Errorf("expected state=final, got %v", ev.payload["state"])
	}
	msg, _ := ev.payload["message"].(map[string]any)
	content, _ := msg["content"].([]map[string]any)
	if len(content) == 0 {
		t.Fatalf("no content in chat event: %+v", ev.payload)
	}
	if content[0]["text"] != "hello back" {
		t.Errorf("expected reply text in chat event, got %+v", content[0])
	}
}

func TestHandleMethod_ChatSendAliasesSessionsSend(t *testing.T) {
	d := newFakeDispatcher("pong", nil)
	push, events := newChatCollector(2)
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: d, Channel: "r1", DeviceID: "dev-1", PushEvent: push,
	})
	payload, errShape := m.Handle(context.Background(), MethodChatSend, json.RawMessage(`{"text":"ping"}`))
	if errShape != nil {
		t.Fatalf("chat.send: %+v", errShape)
	}
	if payload == nil {
		t.Error("expected non-nil ack")
	}
	turn := d.waitForDispatch(t)
	if turn.Message != "ping" {
		t.Errorf("dispatcher not called with ping: %+v", turn)
	}
	if ev := waitChat(t, events); ev.payload["state"] != "final" {
		t.Errorf("expected final state, got %v", ev.payload["state"])
	}
}

// TestHandleMethod_AccessDenied verifies that a dispatcher error surfaces as
// an async chat event with state=error (not a synchronous ErrorShape — the
// sync response has already ACKed with status=started by then).
func TestHandleMethod_AccessDenied(t *testing.T) {
	d := newFakeDispatcher("", core.ErrAccessDenied)
	push, events := newChatCollector(2)
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: d, Channel: "r1", DeviceID: "dev-1", PushEvent: push,
	})
	_, errShape := m.Handle(context.Background(), MethodSessionsSend, json.RawMessage(`{"text":"x"}`))
	if errShape != nil {
		t.Fatalf("sync ACK should not error: %+v", errShape)
	}
	ev := waitChat(t, events)
	if ev.payload["state"] != "error" {
		t.Errorf("expected state=error, got %v", ev.payload["state"])
	}
	if ev.payload["errorMessage"] == "" || ev.payload["errorMessage"] == nil {
		t.Errorf("expected errorMessage set, got %v", ev.payload["errorMessage"])
	}
}

// TestHandleMethod_SendWithoutPushEvent ensures the async goroutine does not
// panic when PushEvent is not wired (smoke test — real connections always
// wire one, but defensive callers shouldn't crash the process).
func TestHandleMethod_SendWithoutPushEvent(t *testing.T) {
	d := newFakeDispatcher("ok", nil)
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: d, Channel: "r1", DeviceID: "dev-1",
	})
	if _, errShape := m.Handle(context.Background(), MethodSessionsSend, json.RawMessage(`{"text":"hi"}`)); errShape != nil {
		t.Fatalf("unexpected error: %+v", errShape)
	}
	d.waitForDispatch(t)
	// Give the goroutine a beat to return past the nil push.
	time.Sleep(10 * time.Millisecond)
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

// TestHandleMethod_TalkSpeakReturnsFallbackEligibleError verifies the
// shim returns a TALK_UNCONFIGURED error with fallbackEligible=true so the
// R1 firmware falls back to on-device Android TTS rather than expecting
// server-side audio.
func TestHandleMethod_TalkSpeakReturnsFallbackEligibleError(t *testing.T) {
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: newFakeDispatcher("", nil), Channel: "r1", DeviceID: "dev-1",
	})
	payload, errShape := m.Handle(context.Background(), MethodTalkSpeak, json.RawMessage(`{"text":"hello"}`))
	if payload != nil {
		t.Errorf("expected nil payload, got %s", string(payload))
	}
	if errShape == nil {
		t.Fatal("expected ErrorShape, got nil")
	}
	if errShape.Code != "TALK_UNCONFIGURED" {
		t.Errorf("expected code TALK_UNCONFIGURED, got %q", errShape.Code)
	}
	if errShape.Details == nil {
		t.Fatal("expected details set")
	}
	var details map[string]any
	if err := json.Unmarshal(errShape.Details, &details); err != nil {
		t.Fatalf("parse details: %v", err)
	}
	if details["fallbackEligible"] != true {
		t.Errorf("expected fallbackEligible=true, got %v", details["fallbackEligible"])
	}
	if details["reason"] != "talk_unconfigured" {
		t.Errorf("expected reason=talk_unconfigured, got %v", details["reason"])
	}
}

// TestHandleMethod_TalkConfigReturnsUnconfigured verifies the shim
// advertises no TTS provider so the R1 defers to device-local TTS.
func TestHandleMethod_TalkConfigReturnsUnconfigured(t *testing.T) {
	m := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: newFakeDispatcher("", nil), Channel: "r1", DeviceID: "dev-1",
	})
	payload, errShape := m.Handle(context.Background(), MethodTalkConfig, json.RawMessage(`{}`))
	if errShape != nil {
		t.Fatalf("unexpected error: %+v", errShape)
	}
	var got struct {
		Config struct {
			Talk struct {
				Provider string `json:"provider"`
			} `json:"talk"`
		} `json:"config"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if got.Config.Talk.Provider != "" {
		t.Errorf("expected empty provider, got %q", got.Config.Talk.Provider)
	}
}

// Silence unused warning if test is edited down in the future.
var _ = errors.New
var _ = time.Now
