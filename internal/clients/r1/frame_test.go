package r1

import (
	"encoding/json"
	"testing"
)

func TestFrame_MarshalRequest(t *testing.T) {
	f := Frame{
		Type:   FrameTypeReq,
		ID:     "req-1",
		Method: MethodConnect,
		Params: json.RawMessage(`{"x":1}`),
	}
	got, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"req","id":"req-1","method":"connect","params":{"x":1}}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestFrame_MarshalResponseOK(t *testing.T) {
	f := Frame{
		Type:    FrameTypeRes,
		ID:      "req-1",
		OK:      boolPtr(true),
		Payload: json.RawMessage(`{"hello":"world"}`),
	}
	got, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"res","id":"req-1","ok":true,"payload":{"hello":"world"}}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestFrame_MarshalResponseError(t *testing.T) {
	f := Frame{
		Type: FrameTypeRes,
		ID:   "req-1",
		OK:   boolPtr(false),
		Error: &ErrorShape{
			Code:    "INVALID_REQUEST",
			Message: "bad nonce",
		},
	}
	got, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"res","id":"req-1","ok":false,"error":{"code":"INVALID_REQUEST","message":"bad nonce"}}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestFrame_MarshalEvent(t *testing.T) {
	f := Frame{
		Type:    FrameTypeEvent,
		Event:   EventTick,
		Payload: json.RawMessage(`{"ts":1000}`),
	}
	got, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"event","event":"tick","payload":{"ts":1000}}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestFrame_UnmarshalRequest(t *testing.T) {
	data := []byte(`{"type":"req","id":"req-1","method":"connect","params":{"x":1}}`)
	var f Frame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f.Type != FrameTypeReq || f.ID != "req-1" || f.Method != MethodConnect {
		t.Errorf("bad frame: %+v", f)
	}
	if string(f.Params) != `{"x":1}` {
		t.Errorf("params mismatch: %s", f.Params)
	}
}

func TestFrame_UnmarshalEventNoPayload(t *testing.T) {
	data := []byte(`{"type":"event","event":"tick"}`)
	var f Frame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f.Type != FrameTypeEvent || f.Event != EventTick {
		t.Errorf("bad frame: %+v", f)
	}
	if f.Payload != nil {
		t.Errorf("payload should be nil, got %s", f.Payload)
	}
}

func boolPtr(b bool) *bool { return &b }
