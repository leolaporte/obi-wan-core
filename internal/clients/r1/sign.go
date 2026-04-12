package r1

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
)

// V3PayloadParams are the fields packed into the v3 signed payload.
// Matches buildDeviceAuthPayloadV3 in openclaw/src/gateway/device-auth.ts.
type V3PayloadParams struct {
	DeviceID     string
	ClientID     string
	ClientMode   string
	Role         string
	Scopes       []string
	SignedAtMs   int64
	Token        string // may be empty
	Nonce        string
	Platform     string // optional
	DeviceFamily string // optional
}

// BuildV3Payload builds the pipe-delimited v3 canonical payload string
// that the client signs and the server verifies. Platform and DeviceFamily
// are lowercased (ASCII only) to match OpenClaw's
// normalizeDeviceMetadataForAuth.
func BuildV3Payload(p V3PayloadParams) string {
	return strings.Join([]string{
		"v3",
		p.DeviceID,
		p.ClientID,
		p.ClientMode,
		p.Role,
		strings.Join(p.Scopes, ","),
		strconv.FormatInt(p.SignedAtMs, 10),
		p.Token,
		p.Nonce,
		asciiLower(strings.TrimSpace(p.Platform)),
		asciiLower(strings.TrimSpace(p.DeviceFamily)),
	}, "|")
}

// BuildV2Payload builds the pipe-delimited v2 canonical payload string.
// Matches buildDeviceAuthPayload in openclaw/src/gateway/device-auth.ts.
// v2 omits platform and deviceFamily.
func BuildV2Payload(p V3PayloadParams) string {
	return strings.Join([]string{
		"v2",
		p.DeviceID,
		p.ClientID,
		p.ClientMode,
		p.Role,
		strings.Join(p.Scopes, ","),
		strconv.FormatInt(p.SignedAtMs, 10),
		p.Token,
		p.Nonce,
	}, "|")
}

// asciiLower mirrors openclaw's toLowerAscii: only ASCII A-Z are
// lowercased, non-ASCII is left alone. Keeps cross-runtime determinism.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// VerifySignature returns true iff sigBase64URL is a valid ed25519
// signature of payload under the ed25519 public key encoded as a
// raw 32-byte key in base64url (no padding). Any malformed input
// returns false rather than panicking.
func VerifySignature(pubKeyBase64URL, payload, sigBase64URL string) bool {
	pub, err := base64.RawURLEncoding.DecodeString(pubKeyBase64URL)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigBase64URL)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), []byte(payload), sig)
}

// DeriveDeviceID returns the OpenClaw-compatible deviceId derived from a
// raw ed25519 public key encoded as base64url: hex(sha256(rawPubKey)).
// Returns empty string if the key is malformed.
func DeriveDeviceID(pubKeyBase64URL string) string {
	pub, err := base64.RawURLEncoding.DecodeString(pubKeyBase64URL)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return ""
	}
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}
