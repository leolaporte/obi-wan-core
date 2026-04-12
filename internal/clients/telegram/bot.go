package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/leolaporte/obi-wan-core/internal/core"
)

// Dispatcher is the subset of core.Dispatcher the telegram client needs.
// Declared as an interface so tests can inject a fake without instantiating
// the real claude subprocess runner.
type Dispatcher interface {
	Dispatch(ctx context.Context, turn core.Turn) (*core.Reply, error)
}

// Config bundles the runtime knobs the telegram client needs.
type Config struct {
	BotToken string
	Channel  string // the config-key name ("telegram")
}

// Client is the Telegram long-poll client.
type Client struct {
	cfg        Config
	dispatcher Dispatcher
	bot        *bot.Bot
}

// New constructs a Telegram client. The bot is created but not started —
// call Start to begin long-polling.
func New(cfg Config, d Dispatcher) (*Client, error) {
	c := &Client{cfg: cfg, dispatcher: d}

	b, err := bot.New(cfg.BotToken,
		bot.WithDefaultHandler(c.onUpdate),
		bot.WithAllowedUpdates(bot.AllowedUpdates{"message"}),
	)
	if err != nil {
		return nil, err
	}
	c.bot = b
	return c, nil
}

// Start begins long-polling. Blocks until ctx is cancelled.
func (c *Client) Start(ctx context.Context) {
	slog.Info("telegram client starting")
	c.bot.Start(ctx)
	slog.Info("telegram client stopped")
}

func (c *Client) onUpdate(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}
	msg := update.Message
	userID, ok := extractSender(msg)
	if !ok {
		slog.Warn("dropping message with nil From", "chat", msg.Chat.ID, "msgID", msg.ID)
		return
	}
	chatID := msg.Chat.ID

	// Typing indicator while claude is working. Refreshed every 4s.
	stopTyping := c.startTypingLoop(ctx, chatID)
	defer stopTyping()

	reply, err := c.handleText(ctx, userID, msg.Text)
	if err != nil {
		slog.Error("dispatch failed", "error", err, "user", userID)
		c.sendText(ctx, chatID, replyParamsFor(0, msg.ID), "Error: "+err.Error())
		return
	}
	if reply == "" {
		return // access denied — silent drop
	}
	for i, chunk := range Chunk(reply) {
		c.sendText(ctx, chatID, replyParamsFor(i, msg.ID), chunk)
	}
}

// handleText is the pure, testable half of onUpdate.
func (c *Client) handleText(ctx context.Context, userID, text string) (string, error) {
	r, err := c.dispatcher.Dispatch(ctx, core.Turn{
		Channel:    c.cfg.Channel,
		UserID:     userID,
		Message:    text,
		ReceivedAt: time.Now(),
	})
	if err != nil {
		if errors.Is(err, core.ErrAccessDenied) {
			return "", nil
		}
		return "", err
	}
	return r.Text, nil
}

func (c *Client) sendText(ctx context.Context, chatID int64, rp *models.ReplyParameters, text string) {
	_, err := c.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          chatID,
		Text:            text,
		ReplyParameters: rp,
	})
	if err != nil {
		slog.Warn("telegram send failed", "error", err)
	}
}

// extractSender returns the string-formatted sender ID from a message,
// or ("", false) if the message has no From (channel posts, certain
// automated messages). The caller must drop updates where ok=false.
func extractSender(msg *models.Message) (string, bool) {
	if msg.From == nil {
		return "", false
	}
	return strconv.FormatInt(msg.From.ID, 10), true
}

// replyParamsFor returns the ReplyParameters for chunk index i of a
// multi-chunk reply. Only the first chunk threads back to the original
// message; subsequent chunks are sent without reply-threading to avoid
// repeated "In reply to" banners. Matches telegram-daemon's sendLong.
func replyParamsFor(i, msgID int) *models.ReplyParameters {
	if i != 0 {
		return nil
	}
	return &models.ReplyParameters{MessageID: msgID}
}

// SendToChat sends a plain text message to an arbitrary chat ID (used by
// the watch echo adapter to mirror Watch replies into Leo's Telegram DM).
// Long messages are chunked. Errors are logged and swallowed — Echo is a
// best-effort sidecar.
func (c *Client) SendToChat(ctx context.Context, chatID, text string) {
	id, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		slog.Warn("invalid chat id", "chatID", chatID, "error", err)
		return
	}
	for _, chunk := range Chunk(text) {
		if _, err := c.bot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: id,
			Text:   chunk,
		}); err != nil {
			slog.Warn("echo send failed", "error", err)
			return
		}
	}
}

// startTypingLoop emits a typing chat action immediately, then again every
// 4 seconds until the returned stop function is called.
func (c *Client) startTypingLoop(ctx context.Context, chatID int64) func() {
	doneCh := make(chan struct{})
	send := func() {
		_, _ = c.bot.SendChatAction(ctx, &bot.SendChatActionParams{
			ChatID: chatID,
			Action: models.ChatActionTyping,
		})
	}
	send()
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-doneCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				send()
			}
		}
	}()
	return func() { close(doneCh) }
}
