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
	userID := strconv.FormatInt(msg.From.ID, 10)
	chatID := msg.Chat.ID

	// Typing indicator while claude is working. Refreshed every 4s.
	stopTyping := c.startTypingLoop(ctx, chatID)
	defer stopTyping()

	reply, err := c.handleText(ctx, userID, msg.Text)
	if err != nil {
		slog.Error("dispatch failed", "error", err, "user", userID)
		c.sendText(ctx, chatID, msg.ID, "Error: "+err.Error())
		return
	}
	if reply == "" {
		return // access denied — silent drop
	}
	for _, chunk := range Chunk(reply) {
		c.sendText(ctx, chatID, msg.ID, chunk)
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

func (c *Client) sendText(ctx context.Context, chatID int64, replyTo int, text string) {
	_, err := c.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
		ReplyParameters: &models.ReplyParameters{
			MessageID: replyTo,
		},
	})
	if err != nil {
		slog.Warn("telegram send failed", "error", err)
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
