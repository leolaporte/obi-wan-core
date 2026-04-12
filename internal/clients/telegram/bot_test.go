package telegram

import (
	"context"
	"errors"
	"testing"

	"github.com/leolaporte/obi-wan-core/internal/core"
	"github.com/stretchr/testify/require"
)

type fakeDispatcher struct {
	lastTurn core.Turn
	reply    *core.Reply
	err      error
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, turn core.Turn) (*core.Reply, error) {
	f.lastTurn = turn
	return f.reply, f.err
}

func TestHandleText_buildsTurnAndReturnsReply(t *testing.T) {
	fd := &fakeDispatcher{reply: &core.Reply{Text: "hi back"}}
	c := &Client{
		cfg:        Config{Channel: "telegram"},
		dispatcher: fd,
	}
	reply, err := c.handleText(context.Background(), "42", "hello")
	require.NoError(t, err)
	require.Equal(t, "hi back", reply)
	require.Equal(t, "telegram", fd.lastTurn.Channel)
	require.Equal(t, "42", fd.lastTurn.UserID)
	require.Equal(t, "hello", fd.lastTurn.Message)
}

func TestHandleText_accessDeniedReturnsEmpty(t *testing.T) {
	fd := &fakeDispatcher{err: core.ErrAccessDenied}
	c := &Client{cfg: Config{Channel: "telegram"}, dispatcher: fd}
	reply, err := c.handleText(context.Background(), "999", "hi")
	require.NoError(t, err, "access denied is silent, not an error")
	require.Empty(t, reply)
}

func TestHandleText_otherErrorSurfaces(t *testing.T) {
	fd := &fakeDispatcher{err: errors.New("boom")}
	c := &Client{cfg: Config{Channel: "telegram"}, dispatcher: fd}
	_, err := c.handleText(context.Background(), "42", "hi")
	require.Error(t, err)
}
