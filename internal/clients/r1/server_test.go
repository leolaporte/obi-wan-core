package r1

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/core"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type stubDispatcher struct{}

func (stubDispatcher) Dispatch(ctx context.Context, turn core.Turn) (*core.Reply, error) {
	return &core.Reply{Text: "reply-to:" + turn.Message}, nil
}

func TestServer_FullHandshakeAndSend(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(Config{
		Port:           0, // ephemeral
		BootstrapToken: "boot-xyz",
		Channel:        "r1",
		StatePath:      filepath.Join(dir, "r1-devices.json"),
	}, stubDispatcher{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startErr := make(chan error, 1)
	go func() { startErr <- srv.Start(ctx) }()

	// Wait until the listener is assigned.
	waitUntil(t, 2*time.Second, func() bool { return srv.Addr() != "" })
	addr := srv.Addr()

	// Dial a WS connection.
	u := url.URL{Scheme: "ws", Host: addr, Path: "/"}
	dialCtx, dialCancel := context.WithTimeout(ctx, 2*time.Second)
	defer dialCancel()
	c, _, err := websocket.Dial(dialCtx, u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// Receive connect.challenge.
	var chal Frame
	if err := wsjson.Read(ctx, c, &chal); err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	if chal.Type != FrameTypeEvent || chal.Event != EventConnectChallenge {
		t.Fatalf("bad challenge frame: %+v", chal)
	}
	var chalPayload struct {
		Nonce string `json:"nonce"`
		Ts    int64  `json:"ts"`
	}
	if err := json.Unmarshal(chal.Payload, &chalPayload); err != nil {
		t.Fatalf("parse challenge payload: %v", err)
	}

	// Build a connect req, sign it.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pubB64 := base64.RawURLEncoding.EncodeToString(pub)
	deviceID := DeriveDeviceID(pubB64)
	const signedAt = int64(1700000000000)
	payload := BuildV3Payload(V3PayloadParams{
		DeviceID: deviceID, ClientID: "node-host", ClientMode: "node",
		Role: RoleNode, Scopes: []string{}, SignedAtMs: signedAt,
		Token: "boot-xyz", Nonce: chalPayload.Nonce,
		Platform: "android", DeviceFamily: "rabbit-r1",
	})
	sig := ed25519.Sign(priv, []byte(payload))
	connectParams, _ := json.Marshal(map[string]any{
		"minProtocol": 3, "maxProtocol": 3,
		"client": map[string]any{
			"id": "node-host", "version": "1.0.0",
			"platform": "android", "deviceFamily": "rabbit-r1",
			"mode": "node",
		},
		"role": RoleNode, "scopes": []string{},
		"device": map[string]any{
			"id": deviceID, "publicKey": pubB64,
			"signature": base64.RawURLEncoding.EncodeToString(sig),
			"signedAt":  signedAt, "nonce": chalPayload.Nonce,
		},
		"auth": map[string]any{"bootstrapToken": "boot-xyz"},
	})
	if err := wsjson.Write(ctx, c, Frame{
		Type:   FrameTypeReq,
		ID:     "req-1",
		Method: MethodConnect,
		Params: connectParams,
	}); err != nil {
		t.Fatalf("send connect: %v", err)
	}

	// Expect HelloOk response.
	var res Frame
	if err := wsjson.Read(ctx, c, &res); err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if res.Type != FrameTypeRes || res.ID != "req-1" || res.OK == nil || !*res.OK {
		t.Fatalf("handshake failed: %+v", res)
	}
	var hello HelloOk
	if err := json.Unmarshal(res.Payload, &hello); err != nil {
		t.Fatalf("parse hello: %v", err)
	}
	if hello.Type != "hello-ok" || hello.Protocol != ProtocolVersion {
		t.Errorf("bad hello: %+v", hello)
	}
	if hello.Auth == nil || hello.Auth.DeviceToken == "" {
		t.Error("expected HelloOk.auth.deviceToken")
	}

	// Send a chat via sessions.send.
	sendParams, _ := json.Marshal(map[string]any{"text": "ping"})
	if err := wsjson.Write(ctx, c, Frame{
		Type:   FrameTypeReq,
		ID:     "req-2",
		Method: MethodSessionsSend,
		Params: sendParams,
	}); err != nil {
		t.Fatalf("send sessions.send: %v", err)
	}

	var sendRes Frame
	if err := wsjson.Read(ctx, c, &sendRes); err != nil {
		t.Fatalf("read sessions.send res: %v", err)
	}
	if sendRes.OK == nil || !*sendRes.OK {
		t.Fatalf("sessions.send failed: %+v", sendRes)
	}
	if sendRes.ID != "req-2" {
		t.Errorf("response ID mismatch: got %q want %q", sendRes.ID, "req-2")
	}
	var sendPayload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(sendRes.Payload, &sendPayload); err != nil {
		t.Fatalf("parse send res: %v", err)
	}
	if sendPayload.Text != "reply-to:ping" {
		t.Errorf("bad reply: %q", sendPayload.Text)
	}

	// Clean shutdown.
	c.Close(websocket.StatusNormalClosure, "")
	cancel()
	select {
	case err := <-startErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("server exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("server did not shut down in time")
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}
