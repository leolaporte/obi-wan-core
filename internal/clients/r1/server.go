package r1

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/core"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// Dispatcher is the subset of core.Dispatcher the r1 shim needs.
type Dispatcher interface {
	Dispatch(ctx context.Context, turn core.Turn) (*core.Reply, error)
}

// Config holds runtime configuration for the R1 shim.
type Config struct {
	Port           int
	BootstrapToken string
	Channel        string
	StatePath      string
}

// Server is the R1 gateway shim.
type Server struct {
	cfg        Config
	dispatcher Dispatcher
	store      *DeviceStore
	httpServer *http.Server
	listener   net.Listener
	// ready is closed once Start has bound the listener. Addr() uses
	// the close as a happens-before barrier so the listener field can
	// be read without racing against Start's assignment.
	ready chan struct{}
	// connMu guards the single-active-connection invariant. If a second
	// connection arrives while one is active, it is refused with 409.
	connMu sync.Mutex
	active bool
}

// NewServer constructs but does not start the server.
func NewServer(cfg Config, d Dispatcher) (*Server, error) {
	if cfg.StatePath == "" {
		return nil, fmt.Errorf("r1: StatePath required")
	}
	store, err := OpenDeviceStore(cfg.StatePath)
	if err != nil {
		return nil, fmt.Errorf("r1: device store: %w", err)
	}
	return &Server{cfg: cfg, dispatcher: d, store: store, ready: make(chan struct{})}, nil
}

// Addr returns the listening address after Start has bound the listener.
// Returns an empty string if called before Start has bound. Used by tests
// that need the port assigned by :0.
func (s *Server) Addr() string {
	select {
	case <-s.ready:
		return s.listener.Addr().String()
	default:
		return ""
	}
}

// Start binds the listener and serves until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	addr := ":" + strconv.Itoa(s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("r1: listen: %w", err)
	}
	s.listener = ln
	close(s.ready)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleUpgrade)

	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("r1 gateway listening", "addr", ln.Addr().String())
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		// Note: http.Server.Shutdown does not close already-hijacked
		// WebSocket connections. For this single-connection shim that is
		// acceptable — the in-flight R1 connection's context will be
		// cancelled by the http.Server base-context propagation, which
		// unblocks the serveConnection reads and lets the handler return
		// naturally. If this shim ever serves multiple concurrent
		// connections, revisit: maintain a registry and close them here.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	// The R1 sends a plain HTTP GET health check before upgrading to
	// WebSocket. If the request doesn't have an Upgrade header, respond
	// with 200 OK so the R1's pre-flight check passes.
	if r.Header.Get("Upgrade") == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","server":"obi-wan-core/r1-shim"}`))
		return
	}

	s.connMu.Lock()
	if s.active {
		s.connMu.Unlock()
		http.Error(w, "r1: another connection is active", http.StatusConflict)
		return
	}
	s.active = true
	s.connMu.Unlock()
	defer func() {
		s.connMu.Lock()
		s.active = false
		s.connMu.Unlock()
	}()

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // we don't do Origin checks for the node path
	})
	if err != nil {
		slog.Warn("r1 upgrade failed", "error", err)
		return
	}
	c.SetReadLimit(int64(MaxPayloadBytes))

	connID := randomHex(16)
	ctx := r.Context()
	slog.Info("r1 connection opened", "connId", connID)
	defer func() {
		_ = c.Close(websocket.StatusNormalClosure, "")
		slog.Info("r1 connection closed", "connId", connID)
	}()

	if err := s.serveConnection(ctx, c, connID); err != nil {
		slog.Warn("r1 connection ended with error", "connId", connID, "error", err)
	}
}

// serveConnection runs the full per-connection lifecycle: challenge →
// handshake → request loop with tick emission on a side channel.
func (s *Server) serveConnection(ctx context.Context, c *websocket.Conn, connID string) error {
	nonce := randomHex(16)

	// Handshake phase — timeout to prevent hung connections from locking
	// out the real R1 indefinitely.
	hsCtx, hsCancel := context.WithTimeout(ctx, 10*time.Second)
	defer hsCancel()

	// Emit connect.challenge.
	if err := wsjson.Write(hsCtx, c, Frame{
		Type:    FrameTypeEvent,
		Event:   EventConnectChallenge,
		Payload: rawJSON(map[string]any{"nonce": nonce, "ts": time.Now().UnixMilli()}),
	}); err != nil {
		return fmt.Errorf("send challenge: %w", err)
	}

	// Read first frame: must be req connect.
	var first Frame
	if err := wsjson.Read(hsCtx, c, &first); err != nil {
		return fmt.Errorf("read connect: %w", err)
	}
	if first.Type != FrameTypeReq || first.Method != MethodConnect {
		return writeError(hsCtx, c, first.ID, ErrCodeInvalidRequest, "first frame must be req connect")
	}

	h := NewHandshake(HandshakeConfig{
		BootstrapToken: s.cfg.BootstrapToken,
		DeviceStore:    s.store,
		Nonce:          nonce,
	})
	hello, errShape := h.Handle(first.Params)
	if errShape != nil {
		slog.Warn("r1 handshake rejected", "connId", connID, "code", errShape.Code, "msg", errShape.Message)
		return writeErrorShape(hsCtx, c, first.ID, errShape)
	}
	hello.Server.ConnID = connID

	helloBytes, err := json.Marshal(hello)
	if err != nil {
		return fmt.Errorf("marshal hello: %w", err)
	}
	ok := true
	if err := wsjson.Write(hsCtx, c, Frame{
		Type:    FrameTypeRes,
		ID:      first.ID,
		OK:      &ok,
		Payload: helloBytes,
	}); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	// Push voicewake.changed with empty triggers immediately after HelloOk,
	// matching OpenClaw's node-connect behavior (recon §5.3 point 4).
	// If the R1 gates on this event before sending transcripts, omitting
	// it would silently stall the connection.
	_ = wsjson.Write(ctx, c, Frame{
		Type:    FrameTypeEvent,
		Event:   EventVoicewakeChanged,
		Payload: rawJSON(map[string]any{"triggers": []any{}}),
	})

	// Now in "connected" state. Resolve the authoritative deviceID for
	// method dispatch: signed device.id from the handshake.
	var params ConnectParams
	_ = json.Unmarshal(first.Params, &params)
	deviceID := ""
	if params.Device != nil {
		deviceID = params.Device.ID
	}
	if deviceID == "" {
		deviceID = params.Client.ID
	}
	slog.Info("r1 handshake complete", "connId", connID, "deviceId", deviceID)

	methods := NewMethodHandler(MethodHandlerConfig{
		Dispatcher: s.dispatcher,
		Channel:    s.cfg.Channel,
		DeviceID:   deviceID,
	})

	// Start the tick loop.
	tickDone := make(chan struct{})
	tickCtx, cancelTick := context.WithCancel(ctx)
	defer func() {
		cancelTick()
		<-tickDone
	}()
	go func() {
		defer close(tickDone)
		ticker := time.NewTicker(TickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-tickCtx.Done():
				return
			case <-ticker.C:
				_ = wsjson.Write(tickCtx, c, Frame{
					Type:    FrameTypeEvent,
					Event:   EventTick,
					Payload: rawJSON(map[string]any{"ts": time.Now().UnixMilli()}),
				})
			}
		}
	}()

	// Request loop.
	for {
		var f Frame
		if err := wsjson.Read(ctx, c, &f); err != nil {
			slog.Info("r1 read ended", "connId", connID, "error", err)
			return nil // normal close path
		}
		slog.Info("r1 frame received", "connId", connID, "type", f.Type, "method", f.Method, "id", f.ID, "event", f.Event, "raw", string(f.Params))
		if f.Type != FrameTypeReq {
			// Post-handshake the server only accepts req frames.
			continue
		}
		payload, errShape := methods.Handle(ctx, f.Method, f.Params)
		if errShape != nil {
			if err := writeErrorShape(ctx, c, f.ID, errShape); err != nil {
				return err
			}
			continue
		}
		ok := true
		slog.Info("r1 sending response", "connId", connID, "method", f.Method, "id", f.ID, "payload", string(payload))
		if err := wsjson.Write(ctx, c, Frame{
			Type:    FrameTypeRes,
			ID:      f.ID,
			OK:      &ok,
			Payload: payload,
		}); err != nil {
			slog.Warn("r1 response write failed", "connId", connID, "error", err)
			return err
		}
		slog.Info("r1 response sent", "connId", connID, "id", f.ID)

		// Also push the response as a chat event — the R1 may expect
		// streaming chat events rather than (or in addition to) the
		// synchronous res frame.
		if f.Method == MethodChatSend || f.Method == MethodSessionsSend {
			var textPayload struct{ Text string `json:"text"` }
			_ = json.Unmarshal(payload, &textPayload)
			if textPayload.Text != "" {
				chatEvent := rawJSON(map[string]any{
					"type":    "assistant",
					"content": textPayload.Text,
					"done":    true,
				})
				_ = wsjson.Write(ctx, c, Frame{
					Type:    FrameTypeEvent,
					Event:   "chat",
					Payload: chatEvent,
				})
				slog.Info("r1 chat event pushed", "connId", connID)
			}
		}
	}
}

func writeError(ctx context.Context, c *websocket.Conn, id, code, msg string) error {
	return writeErrorShape(ctx, c, id, &ErrorShape{Code: code, Message: msg})
}

func writeErrorShape(ctx context.Context, c *websocket.Conn, id string, errShape *ErrorShape) error {
	ok := false
	return wsjson.Write(ctx, c, Frame{
		Type:  FrameTypeRes,
		ID:    id,
		OK:    &ok,
		Error: errShape,
	})
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func randomHex(nBytes int) string {
	buf := make([]byte, nBytes)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
