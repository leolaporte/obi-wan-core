package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/clients/r1"
	"github.com/leolaporte/obi-wan-core/internal/clients/telegram"
	"github.com/leolaporte/obi-wan-core/internal/clients/watch"
	"github.com/leolaporte/obi-wan-core/internal/config"
	"github.com/leolaporte/obi-wan-core/internal/core"
	"github.com/leolaporte/obi-wan-core/internal/memory"
)

const defaultConfigPath = "~/.config/obi-wan-core/config.yaml"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: obi-wan-core <serve|dispatch> [flags]")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			slog.Error("serve failed", "error", err)
			os.Exit(1)
		}
	case "dispatch":
		if err := runDispatch(os.Args[2:]); err != nil {
			slog.Error("dispatch failed", "error", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(2)
	}
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "path to config.yaml")
	_ = fs.Parse(args)

	d, cfg, err := buildDispatcherWithConfig(expandHome(*configPath))
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	var tgClient *telegram.Client
	if ch, ok := cfg.Channels["telegram"]; ok && ch.Enabled {
		token := os.Getenv(ch.BotTokenEnv)
		if token == "" {
			return fmt.Errorf("telegram enabled but %s is empty", ch.BotTokenEnv)
		}
		tgClient, err = telegram.New(telegram.Config{
			BotToken: token,
			Channel:  "telegram",
		}, d)
		if err != nil {
			return fmt.Errorf("telegram client: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			tgClient.Start(ctx)
		}()
		slog.Info("telegram client launched")
	}

	if ch, ok := cfg.Channels["watch"]; ok && ch.Enabled {
		key := os.Getenv(ch.WebhookKeyEnv)
		if key == "" {
			return fmt.Errorf("watch enabled but %s is empty", ch.WebhookKeyEnv)
		}
		var echo watch.Echo = watch.NoOpEcho{}
		if chatID := os.Getenv(ch.WatchChatIDEnv); chatID != "" && tgClient != nil {
			echo = &telegramEcho{client: tgClient, chatID: chatID}
			slog.Info("watch echo wired to telegram", "chatID", chatID)
		} else if ch.WatchChatIDEnv != "" {
			slog.Info("watch echo disabled",
				"reason", "env var empty or telegram not configured",
				"env", ch.WatchChatIDEnv,
			)
		}
		srv := watch.NewServer(watch.Config{
			Port:       ch.WebhookPort,
			WebhookKey: key,
			Channel:    "watch",
			UserLabel:  "watch",
		}, d, echo)

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Start(ctx); err != nil {
				slog.Error("watch server stopped", "error", err)
			}
		}()
		slog.Info("watch server launched", "port", ch.WebhookPort)
	}

	if ch, ok := cfg.Channels["r1"]; ok && ch.Enabled {
		if ch.BootstrapTokenEnv == "" {
			return fmt.Errorf("r1 enabled but bootstrap_token_env is empty")
		}
		bootstrap := os.Getenv(ch.BootstrapTokenEnv)
		if bootstrap == "" {
			return fmt.Errorf("r1 enabled but %s is empty", ch.BootstrapTokenEnv)
		}
		statePath := ch.DeviceStatePath
		if statePath == "" {
			statePath = filepath.Join(cfg.StateDir, "r1-devices.json")
		}
		r1Srv, err := r1.NewServer(r1.Config{
			Port:           ch.WebhookPort,
			BootstrapToken: bootstrap,
			Channel:        "r1",
			StatePath:      statePath,
		}, d)
		if err != nil {
			return fmt.Errorf("r1 server: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r1Srv.Start(ctx); err != nil {
				slog.Error("r1 server stopped", "error", err)
			}
		}()
		slog.Info("r1 gateway launched", "port", ch.WebhookPort)
	}

	<-ctx.Done()
	slog.Info("obi-wan-core shutting down")
	wg.Wait()
	return nil
}

// telegramEcho adapts a telegram Client to the watch.Echo interface so
// Watch replies are mirrored into Leo's Telegram DM.
type telegramEcho struct {
	client *telegram.Client
	chatID string
}

func (t *telegramEcho) Echo(ctx context.Context, text string) {
	t.client.SendToChat(ctx, t.chatID, text)
}

func runDispatch(args []string) error {
	fs := flag.NewFlagSet("dispatch", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "path to config.yaml")
	channel := fs.String("channel", "", "channel name (e.g. telegram)")
	user := fs.String("user", "", "user id (must be allowlisted in config)")
	msg := fs.String("message", "", "message text")
	_ = fs.Parse(args)

	if *channel == "" || *user == "" || *msg == "" {
		return errors.New("dispatch requires --channel, --user, and --message")
	}

	d, err := buildDispatcher(expandHome(*configPath))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	reply, err := d.Dispatch(ctx, core.Turn{
		Channel:    *channel,
		UserID:     *user,
		Message:    *msg,
		ReceivedAt: time.Now(),
	})
	if err != nil {
		return err
	}

	fmt.Println(reply.Text)
	return nil
}

func buildDispatcherWithConfig(cfgPath string) (*core.Dispatcher, *config.Config, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	sessions, err := core.NewSessionStore(cfg.StateDir)
	if err != nil {
		return nil, nil, fmt.Errorf("session store: %w", err)
	}

	// Memory lives under ~/.claude/channels by convention, not under
	// state_dir — shared with Claude Code's existing channel memory.
	memRoot := expandHome("~/.claude/channels")
	mem := memory.NewLoader(memRoot)

	primary := core.NewClaudeRunner(cfg.ClaudeBinary, cfg.Model)

	var tiers []core.FallbackTierConfig
	if cfg.Fallback.Enabled {
		for _, t := range cfg.Fallback.Tiers {
			var extraEnv []string
			extraEnv = append(extraEnv, "ANTHROPIC_BASE_URL="+t.BaseURL)
			if t.APIKeyEnv != "" {
				apiKey := os.Getenv(t.APIKeyEnv)
				if apiKey == "" {
					slog.Warn("fallback tier enabled but API key env var is empty",
						"env", t.APIKeyEnv,
						"label", t.Label,
					)
					continue
				}
				extraEnv = append(extraEnv, "ANTHROPIC_API_KEY="+apiKey)
			}
			if t.AuthTokenEnv != "" {
				authToken := os.Getenv(t.AuthTokenEnv)
				if authToken != "" {
					extraEnv = append(extraEnv, "ANTHROPIC_AUTH_TOKEN="+authToken)
				}
			}
			runner := core.NewClaudeRunnerWithEnv(cfg.ClaudeBinary, t.Model, extraEnv)
			tiers = append(tiers, core.FallbackTierConfig{
				Runner: runner,
				Label:  t.Label,
			})
			slog.Info("fallback tier configured",
				"label", t.Label,
				"base_url", t.BaseURL,
				"model", t.Model,
			)
		}
	}

	fb := core.NewFallbackRunner(primary, core.BuildFallbackTiers(tiers))

	return core.NewDispatcher(cfg, core.NewAccess(cfg), sessions, mem, fb), cfg, nil
}

// buildDispatcher is the dispatch-subcommand entry point; it discards cfg.
func buildDispatcher(cfgPath string) (*core.Dispatcher, error) {
	d, _, err := buildDispatcherWithConfig(cfgPath)
	return d, err
}

func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
