package r1

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"
)

// newPair returns (pubB64, priv).
func newPair(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(pub), priv
}

// buildConnectParams builds a ConnectParams JSON for a v3-signing client.
func buildConnectParams(t *testing.T, pubB64 string, priv ed25519.PrivateKey, nonce, token, role string) json.RawMessage {
	t.Helper()
	deviceID := DeriveDeviceID(pubB64)
	const signedAt = int64(1700000000000)
	payload := BuildV3Payload(V3PayloadParams{
		DeviceID:     deviceID,
		ClientID:     "node-host",
		ClientMode:   "node",
		Role:         role,
		Scopes:       []string{},
		SignedAtMs:   signedAt,
		Token:        token,
		Nonce:        nonce,
		Platform:     "android",
		DeviceFamily: "rabbit-r1",
	})
	sig := ed25519.Sign(priv, []byte(payload))
	params := map[string]any{
		"minProtocol": 3,
		"maxProtocol": 3,
		"client": map[string]any{
			"id":           "node-host",
			"version":      "1.0.0",
			"platform":     "android",
			"deviceFamily": "rabbit-r1",
			"mode":         "node",
		},
		"role":   role,
		"scopes": []string{},
		"device": map[string]any{
			"id":        deviceID,
			"publicKey": pubB64,
			"signature": base64.RawURLEncoding.EncodeToString(sig),
			"signedAt":  signedAt,
			"nonce":     nonce,
		},
	}
	if token != "" {
		params["auth"] = map[string]any{"bootstrapToken": token}
	}
	buf, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}

func TestHandshake_BootstrapSucceeds(t *testing.T) {
	store, _ := OpenDeviceStore(filepath.Join(t.TempDir(), "r1.json"))
	h := NewHandshake(HandshakeConfig{
		BootstrapToken: "boot-xyz",
		DeviceStore:    store,
		Nonce:          "nonce-1",
	})
	pubB64, priv := newPair(t)
	params := buildConnectParams(t, pubB64, priv, "nonce-1", "boot-xyz", RoleNode)

	hello, errShape := h.Handle(params)
	if errShape != nil {
		t.Fatalf("handshake failed: %+v", errShape)
	}
	if hello.Type != "hello-ok" {
		t.Errorf("bad hello type: %q", hello.Type)
	}
	if hello.Protocol != ProtocolVersion {
		t.Errorf("bad protocol: %d", hello.Protocol)
	}
	if hello.Auth == nil || hello.Auth.DeviceToken == "" {
		t.Error("expected minted device token in HelloOk.auth")
	}
	if !store.Paired() {
		t.Error("store should be paired after successful handshake")
	}
}

func TestHandshake_WrongNonceFails(t *testing.T) {
	store, _ := OpenDeviceStore(filepath.Join(t.TempDir(), "r1.json"))
	h := NewHandshake(HandshakeConfig{
		BootstrapToken: "boot-xyz",
		DeviceStore:    store,
		Nonce:          "server-nonce",
	})
	pubB64, priv := newPair(t)
	// Client signs a different nonce than the server remembers.
	params := buildConnectParams(t, pubB64, priv, "wrong-nonce", "boot-xyz", RoleNode)

	_, errShape := h.Handle(params)
	if errShape == nil {
		t.Fatal("expected handshake to fail")
	}
	if errShape.Code != ErrCodeUnauthorized {
		t.Errorf("want UNAUTHORIZED, got %q", errShape.Code)
	}
	if store.Paired() {
		t.Error("store should NOT be paired after failed handshake")
	}
}

func TestHandshake_BadBootstrapFails(t *testing.T) {
	store, _ := OpenDeviceStore(filepath.Join(t.TempDir(), "r1.json"))
	h := NewHandshake(HandshakeConfig{
		BootstrapToken: "boot-xyz",
		DeviceStore:    store,
		Nonce:          "n",
	})
	pubB64, priv := newPair(t)
	params := buildConnectParams(t, pubB64, priv, "n", "wrong-boot", RoleNode)

	_, errShape := h.Handle(params)
	if errShape == nil || errShape.Code != ErrCodeUnauthorized {
		t.Fatalf("expected UNAUTHORIZED, got %+v", errShape)
	}
}

func TestHandshake_NonNodeRoleRejected(t *testing.T) {
	store, _ := OpenDeviceStore(filepath.Join(t.TempDir(), "r1.json"))
	h := NewHandshake(HandshakeConfig{
		BootstrapToken: "boot-xyz",
		DeviceStore:    store,
		Nonce:          "n",
	})
	pubB64, priv := newPair(t)
	params := buildConnectParams(t, pubB64, priv, "n", "boot-xyz", "operator")

	_, errShape := h.Handle(params)
	if errShape == nil || errShape.Code != ErrCodeInvalidRequest {
		t.Fatalf("expected INVALID_REQUEST, got %+v", errShape)
	}
}

func TestHandshake_DeviceTokenReconnect(t *testing.T) {
	store, _ := OpenDeviceStore(filepath.Join(t.TempDir(), "r1.json"))
	h1 := NewHandshake(HandshakeConfig{
		BootstrapToken: "boot-xyz",
		DeviceStore:    store,
		Nonce:          "n1",
	})
	pubB64, priv := newPair(t)
	// First connect pairs.
	hello1, errShape := h1.Handle(buildConnectParams(t, pubB64, priv, "n1", "boot-xyz", RoleNode))
	if errShape != nil {
		t.Fatalf("first connect: %+v", errShape)
	}
	token := hello1.Auth.DeviceToken

	// Second connect with deviceToken (no bootstrap) on a new nonce.
	h2 := NewHandshake(HandshakeConfig{
		BootstrapToken: "boot-xyz",
		DeviceStore:    store,
		Nonce:          "n2",
	})
	// Build params with deviceToken instead of bootstrapToken.
	deviceID := DeriveDeviceID(pubB64)
	const signedAt = int64(1700000001000)
	payload := BuildV3Payload(V3PayloadParams{
		DeviceID: deviceID, ClientID: "node-host", ClientMode: "node",
		Role: RoleNode, Scopes: []string{}, SignedAtMs: signedAt,
		Token: token, Nonce: "n2",
		Platform: "android", DeviceFamily: "rabbit-r1",
	})
	sig := ed25519.Sign(priv, []byte(payload))
	raw, _ := json.Marshal(map[string]any{
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
			"signedAt":  signedAt, "nonce": "n2",
		},
		"auth": map[string]any{"deviceToken": token},
	})
	hello2, errShape := h2.Handle(raw)
	if errShape != nil {
		t.Fatalf("reconnect: %+v", errShape)
	}
	if hello2.Auth == nil || hello2.Auth.DeviceToken != token {
		t.Error("reconnect should return the same device token")
	}
}
