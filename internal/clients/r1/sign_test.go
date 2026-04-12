package r1

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestBuildV3Payload_Shape(t *testing.T) {
	p := BuildV3Payload(V3PayloadParams{
		DeviceID:     "dev-abc",
		ClientID:     "node-host",
		ClientMode:   "node",
		Role:         "node",
		Scopes:       []string{"a", "b"},
		SignedAtMs:   1700000000000,
		Token:        "tok-xyz",
		Nonce:        "nonce-1",
		Platform:     "Android",
		DeviceFamily: "Rabbit-R1",
	})
	want := "v3|dev-abc|node-host|node|node|a,b|1700000000000|tok-xyz|nonce-1|android|rabbit-r1"
	if p != want {
		t.Errorf("got  %q\nwant %q", p, want)
	}
}

func TestBuildV3Payload_EmptyToken(t *testing.T) {
	p := BuildV3Payload(V3PayloadParams{
		DeviceID:   "d",
		ClientID:   "c",
		ClientMode: "node",
		Role:       "node",
		Scopes:     []string{},
		SignedAtMs: 1,
		Token:      "", // empty is allowed
		Nonce:      "n",
	})
	// Empty scopes join to "", empty platform/family also "".
	want := "v3|d|c|node|node||1||n||"
	if p != want {
		t.Errorf("got  %q\nwant %q", p, want)
	}
}

func TestVerifySignature_Roundtrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	payload := "v3|dev|cli|node|node||1||nonce||"
	sig := ed25519.Sign(priv, []byte(payload))

	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	pubB64 := base64.RawURLEncoding.EncodeToString(pub)

	if !VerifySignature(pubB64, payload, sigB64) {
		t.Error("expected verify to succeed")
	}
}

func TestVerifySignature_WrongPayload(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sig := ed25519.Sign(priv, []byte("correct"))
	pubB64 := base64.RawURLEncoding.EncodeToString(pub)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	if VerifySignature(pubB64, "tampered", sigB64) {
		t.Error("expected verify to fail for tampered payload")
	}
}

func TestVerifySignature_BadBase64(t *testing.T) {
	if VerifySignature("not-base64!!!", "any", "also-bad") {
		t.Error("bad input should verify false, not panic")
	}
}

func TestDeriveDeviceID(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pubB64 := base64.RawURLEncoding.EncodeToString(pub)
	id := DeriveDeviceID(pubB64)
	if len(id) != 64 { // sha256 hex
		t.Errorf("expected 64-char hex, got %d chars: %q", len(id), id)
	}
	// Deterministic.
	if DeriveDeviceID(pubB64) != id {
		t.Error("DeriveDeviceID should be deterministic")
	}
}
