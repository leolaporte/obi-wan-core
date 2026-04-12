package telegram

import (
	"context"
	"errors"
	"testing"

	"github.com/go-telegram/bot/models"
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

func TestChunkReplyThreading_onlyFirstChunkThreads(t *testing.T) {
	// Document the desired behavior via a pure helper test. We cannot
	// test onUpdate directly (it talks to the real bot API), so this
	// test pins the expected semantics: for a multi-chunk reply, chunk
	// index 0 carries ReplyParameters and chunks 1+ do not.
	//
	// Since that logic lives in onUpdate as an inline loop, extract it
	// into a pure helper replyParamsFor(index, msgID) that onUpdate
	// calls — that helper is what this test exercises.
	rp0 := replyParamsFor(0, 42)
	require.NotNil(t, rp0)
	require.Equal(t, 42, rp0.MessageID)

	rp1 := replyParamsFor(1, 42)
	require.Nil(t, rp1, "second chunk must not thread")

	rp2 := replyParamsFor(2, 42)
	require.Nil(t, rp2, "third chunk must not thread")
}

func TestExtractTextTurn_nilFromIsDropped(t *testing.T) {
	// A message with nil From must be silently dropped (no panic, no reply).
	msg := &models.Message{
		ID:   1,
		Text: "hello",
		Chat: models.Chat{ID: 10},
		// From is nil
	}
	userID, ok := extractSender(msg)
	require.False(t, ok)
	require.Empty(t, userID)
}

func TestExtractTextTurn_emptyTextIsDropped(t *testing.T) {
	msg := &models.Message{
		ID:   1,
		Text: "",
		From: &models.User{ID: 42},
		Chat: models.Chat{ID: 10},
	}
	_, ok := extractSender(msg)
	require.True(t, ok, "extractSender only checks From; text check stays in onUpdate")
}

func TestExtractTextTurn_validMessageExtracts(t *testing.T) {
	msg := &models.Message{
		ID:   1,
		Text: "hello",
		From: &models.User{ID: 42},
		Chat: models.Chat{ID: 10},
	}
	userID, ok := extractSender(msg)
	require.True(t, ok)
	require.Equal(t, "42", userID)
}
