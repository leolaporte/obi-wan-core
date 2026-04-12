package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leolaporte/obi-wan-core/internal/core"
	"github.com/stretchr/testify/require"
)

type fakeDispatcher struct {
	reply *core.Reply
	err   error
	seen  core.Turn
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, turn core.Turn) (*core.Reply, error) {
	f.seen = turn
	return f.reply, f.err
}

type fakeEcho struct {
	called bool
	text   string
}

func (f *fakeEcho) Echo(ctx context.Context, text string) {
	f.called = true
	f.text = text
}

func newTestServer(t *testing.T, d Dispatcher, e Echo) *Server {
	t.Helper()
	return NewServer(Config{
		WebhookKey: "secret",
		Channel:    "watch",
		UserLabel:  "watch",
	}, d, e)
}

func TestHandler_validPostReturnsReply(t *testing.T) {
	fd := &fakeDispatcher{reply: &core.Reply{Text: "hi from claude"}}
	fe := &fakeEcho{}
	srv := newTestServer(t, fd, fe)

	req := httptest.NewRequest(http.MethodPost, "/message?key=secret",
		bytes.NewBufferString(`{"text":"wake up"}`))
	rr := httptest.NewRecorder()
	srv.mux().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, true, body["ok"])
	require.Equal(t, "hi from claude", body["response"])
	require.True(t, fe.called, "echo should have been triggered")
	require.Equal(t, "hi from claude", fe.text)
	require.Equal(t, "watch", fd.seen.Channel)
	require.Equal(t, "watch", fd.seen.UserID)
	require.Equal(t, "wake up", fd.seen.Message)
}

func TestHandler_missingKeyReturns401(t *testing.T) {
	fd := &fakeDispatcher{reply: &core.Reply{Text: "ok"}}
	srv := newTestServer(t, fd, &fakeEcho{})

	req := httptest.NewRequest(http.MethodPost, "/message", bytes.NewBufferString(`{"text":"hi"}`))
	rr := httptest.NewRecorder()
	srv.mux().ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandler_headerAuthWorks(t *testing.T) {
	fd := &fakeDispatcher{reply: &core.Reply{Text: "ok"}}
	srv := newTestServer(t, fd, &fakeEcho{})

	req := httptest.NewRequest(http.MethodPost, "/message", bytes.NewBufferString(`{"text":"hi"}`))
	req.Header.Set("X-Pax-Key", "secret")
	rr := httptest.NewRecorder()
	srv.mux().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
}

func TestHandler_emptyTextReturns400(t *testing.T) {
	fd := &fakeDispatcher{reply: &core.Reply{Text: "ok"}}
	srv := newTestServer(t, fd, &fakeEcho{})

	req := httptest.NewRequest(http.MethodPost, "/message?key=secret",
		bytes.NewBufferString(`{"text":""}`))
	rr := httptest.NewRecorder()
	srv.mux().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandler_wrongMethodReturns405(t *testing.T) {
	fd := &fakeDispatcher{reply: &core.Reply{Text: "ok"}}
	srv := newTestServer(t, fd, &fakeEcho{})

	req := httptest.NewRequest(http.MethodGet, "/message?key=secret", nil)
	rr := httptest.NewRecorder()
	srv.mux().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandler_dispatchErrorReturns500(t *testing.T) {
	fd := &fakeDispatcher{err: errors.New("boom")}
	srv := newTestServer(t, fd, &fakeEcho{})

	req := httptest.NewRequest(http.MethodPost, "/message?key=secret",
		bytes.NewBufferString(`{"text":"hi"}`))
	rr := httptest.NewRecorder()
	srv.mux().ServeHTTP(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, "boom", body["error"])
}

func TestHandler_oversizedBodyReturns400(t *testing.T) {
	fd := &fakeDispatcher{reply: &core.Reply{Text: "ok"}}
	srv := newTestServer(t, fd, &fakeEcho{})

	// 2 MiB body — the handler must reject with 400 (the MaxBytesReader
	// surfaces an error during decode, and the empty-text branch or decode
	// failure path returns 400). We don't care which exact status as long
	// as it's 4xx.
	big := bytes.Repeat([]byte("x"), 2<<20)
	payload := bytes.NewBuffer(nil)
	payload.WriteString(`{"text":"`)
	payload.Write(big)
	payload.WriteString(`"}`)

	req := httptest.NewRequest(http.MethodPost, "/message?key=secret", payload)
	rr := httptest.NewRecorder()
	srv.mux().ServeHTTP(rr, req)

	require.GreaterOrEqual(t, rr.Code, 400)
	require.Less(t, rr.Code, 500, "oversized body should be 4xx, not 5xx")
}

func TestNewServer_nilEchoUsesNoOp(t *testing.T) {
	fd := &fakeDispatcher{reply: &core.Reply{Text: "ok"}}
	// Explicit nil — NewServer must default to NoOpEcho so handleMessage
	// doesn't panic calling echo.Echo.
	srv := NewServer(Config{
		WebhookKey: "secret",
		Channel:    "watch",
		UserLabel:  "watch",
	}, fd, nil)

	req := httptest.NewRequest(http.MethodPost, "/message?key=secret",
		bytes.NewBufferString(`{"text":"hi"}`))
	rr := httptest.NewRecorder()
	require.NotPanics(t, func() {
		srv.mux().ServeHTTP(rr, req)
	})
	require.Equal(t, http.StatusOK, rr.Code)
}
