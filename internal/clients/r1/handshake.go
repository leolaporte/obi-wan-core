package r1

import (
	"encoding/json"
	"time"
)

// HandshakeConfig is the per-connection handshake context.
type HandshakeConfig struct {
	BootstrapToken string       // shim-configured pairing secret (from env); empty disables pairing
	DeviceStore    *DeviceStore // shared across all connections
	Nonce          string       // the nonce the server emitted in connect.challenge
}

// NewHandshake returns a reusable Handshake bound to this connection's state.
func NewHandshake(cfg HandshakeConfig) *Handshake {
	return &Handshake{cfg: cfg}
}

// Handshake is one connection's handshake state.
type Handshake struct {
	cfg HandshakeConfig
}

// ConnectParams is the parsed shape of the client's connect request.
// Only fields the shim inspects are modeled; everything else is dropped.
type ConnectParams struct {
	MinProtocol int                    `json:"minProtocol"`
	MaxProtocol int                    `json:"maxProtocol"`
	Client      ConnectClient          `json:"client"`
	Role        string                 `json:"role"`
	Scopes      []string               `json:"scopes"`
	Device      *ConnectDeviceIdentity `json:"device,omitempty"`
	Auth        *ConnectAuth           `json:"auth,omitempty"`
}

// ConnectClient mirrors the nested client{} object.
type ConnectClient struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	Platform     string `json:"platform"`
	DeviceFamily string `json:"deviceFamily"`
	Mode         string `json:"mode"`
}

// ConnectDeviceIdentity is the signed device block.
type ConnectDeviceIdentity struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
	Signature string `json:"signature"`
	SignedAt  int64  `json:"signedAt"`
	Nonce     string `json:"nonce"`
}

// ConnectAuth holds whichever token the client presented.
type ConnectAuth struct {
	BootstrapToken string `json:"bootstrapToken,omitempty"`
	DeviceToken    string `json:"deviceToken,omitempty"`
	Token          string `json:"token,omitempty"`
	Password       string `json:"password,omitempty"`
}

// HelloOk is the payload the server returns inside the res frame for a
// successful connect request.
type HelloOk struct {
	Type         string        `json:"type"`
	Protocol     int           `json:"protocol"`
	Server       HelloServer   `json:"server"`
	Features     HelloFeatures `json:"features"`
	Snapshot     HelloSnapshot `json:"snapshot"`
	Auth         *HelloAuth    `json:"auth,omitempty"`
	Policy       HelloPolicy   `json:"policy"`
}

// HelloServer is the nested server{} block of HelloOk.
type HelloServer struct {
	Version string `json:"version"`
	ConnID  string `json:"connId"`
}

// HelloFeatures advertises the methods + events the shim supports.
type HelloFeatures struct {
	Methods []string `json:"methods"`
	Events  []string `json:"events"`
}

// HelloSnapshot is a deliberately-empty stub. The R1's actual read
// requirements are gap §8.3.6 in the recon doc — we ship empty and
// let Task 11 surface anything the R1 refuses to accept.
type HelloSnapshot struct {
	Presence     []any          `json:"presence"`
	Sessions     []any          `json:"sessions"`
	StateVersion map[string]int `json:"stateVersion"`
}

// HelloAuth carries the issued device token back to the client.
type HelloAuth struct {
	DeviceToken string   `json:"deviceToken"`
	Role        string   `json:"role"`
	Scopes      []string `json:"scopes"`
	IssuedAtMs  int64    `json:"issuedAtMs"`
}

// HelloPolicy advertises server limits.
type HelloPolicy struct {
	MaxPayload       int   `json:"maxPayload"`
	MaxBufferedBytes int   `json:"maxBufferedBytes"`
	TickIntervalMs   int64 `json:"tickIntervalMs"`
}

// Handle verifies a connect request and returns either a HelloOk payload
// or an ErrorShape to wrap in a failing res frame. Exactly one of the
// returned values is non-nil.
func (h *Handshake) Handle(paramsJSON json.RawMessage) (*HelloOk, *ErrorShape) {
	var params ConnectParams
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return nil, &ErrorShape{Code: ErrCodeInvalidRequest, Message: "bad connect params: " + err.Error()}
	}

	// Protocol negotiation.
	if params.MinProtocol > ProtocolVersion || params.MaxProtocol < ProtocolVersion {
		return nil, &ErrorShape{Code: ErrCodeInvalidRequest, Message: "protocol mismatch"}
	}

	// Role gating: only "node" is allowed. Missing defaults to node for this shim.
	role := params.Role
	if role == "" {
		role = RoleNode
	}
	if role != RoleNode {
		return nil, &ErrorShape{Code: ErrCodeInvalidRequest, Message: "only role=node is accepted"}
	}

	// Device identity required.
	if params.Device == nil {
		return nil, &ErrorShape{Code: ErrCodeUnauthorized, Message: "device identity required"}
	}
	// Nonce must match this connection's challenge.
	if params.Device.Nonce != h.cfg.Nonce {
		return nil, &ErrorShape{Code: ErrCodeUnauthorized, Message: "nonce mismatch"}
	}

	// Decide which token is being presented.
	var token string
	var isBootstrap bool
	if params.Auth != nil {
		switch {
		case params.Auth.BootstrapToken != "":
			token = params.Auth.BootstrapToken
			isBootstrap = true
		case params.Auth.DeviceToken != "":
			token = params.Auth.DeviceToken
		case params.Auth.Token != "":
			token = params.Auth.Token
		}
	}
	if token == "" {
		return nil, &ErrorShape{Code: ErrCodeUnauthorized, Message: "no token presented"}
	}

	// Validate token against the right backing store.
	switch {
	case isBootstrap:
		if h.cfg.BootstrapToken == "" || token != h.cfg.BootstrapToken {
			return nil, &ErrorShape{Code: ErrCodeUnauthorized, Message: "invalid bootstrap token"}
		}
		if h.cfg.DeviceStore.Paired() {
			return nil, &ErrorShape{Code: ErrCodeUnauthorized, Message: "r1 already paired"}
		}
	default:
		dev, ok := h.cfg.DeviceStore.LookupByToken(token)
		if !ok {
			return nil, &ErrorShape{Code: ErrCodeUnauthorized, Message: "invalid device token"}
		}
		if dev.PublicKey != params.Device.PublicKey {
			return nil, &ErrorShape{Code: ErrCodeUnauthorized, Message: "device pubkey mismatch"}
		}
	}

	// Verify ed25519 signature over v3 payload. The shim is v3-only.
	payload := BuildV3Payload(V3PayloadParams{
		DeviceID:     params.Device.ID,
		ClientID:     params.Client.ID,
		ClientMode:   params.Client.Mode,
		Role:         role,
		Scopes:       params.Scopes,
		SignedAtMs:   params.Device.SignedAt,
		Token:        token,
		Nonce:        params.Device.Nonce,
		Platform:     params.Client.Platform,
		DeviceFamily: params.Client.DeviceFamily,
	})
	if !VerifySignature(params.Device.PublicKey, payload, params.Device.Signature) {
		return nil, &ErrorShape{Code: ErrCodeUnauthorized, Message: "signature verification failed"}
	}

	// Token handling: on bootstrap, mint a fresh device token; on reconnect, reuse existing.
	var deviceToken string
	if isBootstrap {
		dev, err := h.cfg.DeviceStore.Pair(PairRequest{
			DeviceID:  params.Device.ID,
			PublicKey: params.Device.PublicKey,
			Role:      role,
			Scopes:    []string{},
		})
		if err != nil {
			return nil, &ErrorShape{Code: ErrCodeInternal, Message: err.Error()}
		}
		deviceToken = dev.DeviceToken
	} else {
		deviceToken = token
	}

	hello := &HelloOk{
		Type:     "hello-ok",
		Protocol: ProtocolVersion,
		Server: HelloServer{
			Version: "obi-wan-core/r1-shim/1",
			ConnID:  "", // filled in by server.go per-connection
		},
		Features: HelloFeatures{
			Methods: shimMethods(),
			Events:  shimEvents(),
		},
		Snapshot: HelloSnapshot{
			Presence:     []any{},
			Sessions:     []any{},
			StateVersion: map[string]int{"presence": 0, "health": 0},
		},
		Auth: &HelloAuth{
			DeviceToken: deviceToken,
			Role:        role,
			Scopes:      []string{},
			IssuedAtMs:  time.Now().UnixMilli(),
		},
		Policy: HelloPolicy{
			MaxPayload:       MaxPayloadBytes,
			MaxBufferedBytes: MaxPayloadBytes * 2,
			TickIntervalMs:   int64(TickInterval / time.Millisecond),
		},
	}
	return hello, nil
}

// shimMethods is the feature-list we advertise. Intentionally minimal.
func shimMethods() []string {
	return []string{
		MethodSessionsSend,
		MethodChatSend,
		MethodNodePendingPull,
		MethodNodePendingAck,
		MethodNodeInvokeResult,
		MethodNodeEvent,
		MethodVoicewakeGet,
		MethodWake,
		MethodLastHeartbeat,
		MethodSetHeartbeats,
	}
}

// shimEvents is the event-list we advertise.
func shimEvents() []string {
	return []string{
		EventConnectChallenge,
		EventTick,
		EventVoicewakeChanged,
		EventNodeInvokeReq,
	}
}
