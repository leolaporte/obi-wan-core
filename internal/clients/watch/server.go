// Package watch implements the Apple Watch webhook client for obi-wan-core.
package watch

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/core"
)

// Dispatcher is the subset of core.Dispatcher the watch server needs.
type Dispatcher interface {
	Dispatch(ctx context.Context, turn core.Turn) (*core.Reply, error)
}

// Echo is an optional side-channel that delivers the reply to another
// system (typically the telegram client, so Leo sees the Watch reply in
// his DM). Pass a no-op implementation if unwanted.
type Echo interface {
	Echo(ctx context.Context, text string)
}

// NoOpEcho is an Echo that discards.
type NoOpEcho struct{}

// Echo implements Echo by doing nothing.
func (NoOpEcho) Echo(ctx context.Context, text string) {}

// Config is the runtime configuration for the webhook server.
type Config struct {
	Port       int
	WebhookKey string
	Channel    string // "watch"
	UserLabel  string // synthetic user label used for Turn.UserID and logs;
	// access control is gated by the webhook key, not a
	// per-user allowlist (channel uses OpenAccess=true).
}

// Server is the Watch webhook server.
type Server struct {
	cfg        Config
	dispatcher Dispatcher
	echo       Echo
	httpServer *http.Server
}

// NewServer constructs but does not start the server.
func NewServer(cfg Config, d Dispatcher, e Echo) *Server {
	if e == nil {
		e = NoOpEcho{}
	}
	return &Server{cfg: cfg, dispatcher: d, echo: e}
}

// Start listens on cfg.Port and blocks until ctx is cancelled or an
// unrecoverable error occurs.
func (s *Server) Start(ctx context.Context) error {
	s.httpServer = &http.Server{
		Addr:              ":" + strconv.Itoa(s.cfg.Port),
		Handler:           s.mux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("watch webhook listening", "port", s.cfg.Port)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/message", s.handleMessage)
	return mux
}

type msgRequest struct {
	Text string `json:"text"`
}

type msgResponse struct {
	OK       bool   `json:"ok"`
	Response string `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

	key := r.URL.Query().Get("key")
	if key == "" {
		key = r.Header.Get("X-Pax-Key")
	}
	if key != s.cfg.WebhookKey {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req msgRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, msgResponse{Error: "bad json"})
		return
	}
	if req.Text == "" {
		writeJSON(w, http.StatusBadRequest, msgResponse{Error: "no text provided"})
		return
	}

	slog.Info("watch message received", "len", len(req.Text))

	reply, err := s.dispatcher.Dispatch(r.Context(), core.Turn{
		Channel:    s.cfg.Channel,
		UserID:     s.cfg.UserLabel,
		Message:    req.Text,
		ReceivedAt: time.Now(),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, msgResponse{Error: err.Error()})
		return
	}

	s.echo.Echo(r.Context(), reply.Text)
	writeJSON(w, http.StatusOK, msgResponse{OK: true, Response: reply.Text})
}

func writeJSON(w http.ResponseWriter, status int, body msgResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
