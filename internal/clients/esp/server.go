package esp

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/core"
)

// Dispatcher is the subset of core.Dispatcher the ESP server needs.
type Dispatcher interface {
	Dispatch(ctx context.Context, turn core.Turn) (*core.Reply, error)
}

// Echo mirrors the reply text to a side-channel (typically Telegram) so
// Leo sees what Obi-Wan said when the BOX only plays audio.
type Echo interface {
	Echo(ctx context.Context, text string)
}

// NoOpEcho discards.
type NoOpEcho struct{}

// Echo implements Echo by doing nothing.
func (NoOpEcho) Echo(ctx context.Context, text string) {}

// Config is the runtime configuration for the ESP32 voice webhook.
type Config struct {
	Port          int
	WebhookKey    string // optional; if empty, no auth check (LAN-only)
	Channel       string // "esp"
	UserLabel     string // synthetic user id for Turn.UserID
	WhisperURL    string // e.g. http://localhost:8002/transcribe
	PiperURL      string // e.g. http://localhost:8888/synthesize
	PiperVoice    string // default voice name, e.g. "main"
	SampleRate    int    // 16000 for whisper input + piper output
	MaxAudioBytes int64  // upload cap, e.g. 5 MiB
	// NotifyURL, when non-empty, receives a POST after dispatch so the
	// reply is also spoken through the Framework desktop speakers.
	// Useful as a side-by-side latency check against ESP playback.
	NotifyURL string
}

// Server is the ESP32 voice webhook.
type Server struct {
	cfg        Config
	dispatcher Dispatcher
	echo       Echo
	httpServer *http.Server
	httpClient *http.Client
}

// NewServer constructs but does not start the server.
func NewServer(cfg Config, d Dispatcher, e Echo) *Server {
	if e == nil {
		e = NoOpEcho{}
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.MaxAudioBytes == 0 {
		cfg.MaxAudioBytes = 5 * 1024 * 1024
	}
	if cfg.PiperVoice == "" {
		cfg.PiperVoice = "main"
	}
	return &Server{
		cfg:        cfg,
		dispatcher: d,
		echo:       e,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// Start listens on cfg.Port and blocks until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	s.httpServer = &http.Server{
		Addr:              ":" + strconv.Itoa(s.cfg.Port),
		Handler:           s.mux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("esp webhook listening", "port", s.cfg.Port)
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
	mux.HandleFunc("/talk", s.handleTalk)
	mux.HandleFunc("/health", s.handleHealth)
	return mux
}

func (s *Server) notifyFramework(text string) {
	slog.Info("esp framework notify: firing", "url", s.cfg.NotifyURL, "chars", len(text))
	body, err := json.Marshal(map[string]any{
		"title":   "Obi-Wan",
		"message": text,
		"voice":   true,
		"name":    s.cfg.PiperVoice,
	})
	if err != nil {
		slog.Warn("esp framework notify: marshal", "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.NotifyURL, bytes.NewReader(body))
	if err != nil {
		slog.Warn("esp framework notify: request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		slog.Warn("esp framework notify failed", "error", err)
		return
	}
	slog.Info("esp framework notify done", "status", resp.StatusCode)
	resp.Body.Close()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if status := r.Header.Get("X-ESP32-Status"); status != "" {
		slog.Info("esp health", "status", status, "from", r.RemoteAddr)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

func (s *Server) handleTalk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.cfg.WebhookKey != "" {
		key := r.URL.Query().Get("key")
		if key == "" {
			key = r.Header.Get("X-Pax-Key")
		}
		if subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.WebhookKey)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	start := time.Now()
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxAudioBytes)
	pcm, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("esp read body", "error", err)
		http.Error(w, "read failed", http.StatusBadRequest)
		return
	}
	slog.Info("esp audio received", "bytes", len(pcm))

	wav := wrapPCMAsWAV(pcm, s.cfg.SampleRate, 1, 16)

	sttStart := time.Now()
	text, err := transcribe(r.Context(), s.httpClient, s.cfg.WhisperURL, wav)
	if err != nil {
		slog.Error("esp stt", "error", err)
		http.Error(w, "transcription failed", http.StatusInternalServerError)
		return
	}
	text = strings.TrimSpace(text)
	slog.Info("esp stt done", "dur", time.Since(sttStart).Round(time.Millisecond), "text", text)
	if text == "" {
		http.Error(w, "no speech detected", http.StatusBadRequest)
		return
	}

	dispatchStart := time.Now()
	reply, err := s.dispatcher.Dispatch(r.Context(), core.Turn{
		Channel:    s.cfg.Channel,
		UserID:     s.cfg.UserLabel,
		Message:    text,
		ReceivedAt: time.Now(),
	})
	if err != nil {
		slog.Error("esp dispatch", "error", err)
		http.Error(w, "dispatch failed", http.StatusInternalServerError)
		return
	}
	replyText := strings.TrimSpace(reply.Text)
	slog.Info("esp dispatch done", "dur", time.Since(dispatchStart).Round(time.Millisecond), "chars", len(replyText))
	if replyText == "" {
		http.Error(w, "empty reply", http.StatusInternalServerError)
		return
	}

	// Mirror the text to the echo channel (e.g. Telegram) so Leo has a
	// written record even though the BOX only plays audio back.
	if replyText != "(no output)" {
		s.echo.Echo(r.Context(), reply.Text)
	}

	// Fire-and-forget: also ask the local voice server to speak the
	// reply through Framework speakers so Leo can compare timing
	// between Framework (zero network hop) and ESP (WiFi HTTP + parse
	// + I2S). Runs in the background so it never delays the ESP HTTP
	// response.
	if s.cfg.NotifyURL != "" && replyText != "(no output)" {
		go s.notifyFramework(replyText)
	}

	ttsStart := time.Now()
	audio, err := synthesize(r.Context(), s.httpClient, s.cfg.PiperURL, replyText, s.cfg.PiperVoice, s.cfg.SampleRate)
	if err != nil {
		slog.Error("esp tts", "error", err)
		http.Error(w, "synthesis failed", http.StatusInternalServerError)
		return
	}
	slog.Info("esp tts done", "dur", time.Since(ttsStart).Round(time.Millisecond), "bytes", len(audio))

	slog.Info("esp turn complete", "total", time.Since(start).Round(time.Millisecond))

	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(audio)))
	_, _ = w.Write(audio)
}
