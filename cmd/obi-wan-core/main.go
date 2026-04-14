package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
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
	"github.com/leolaporte/obi-wan-core/internal/tools"
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

	// Resolve primary API key.
	apiKey := os.Getenv(cfg.APIKeyEnv)
	if apiKey == "" {
		return nil, nil, fmt.Errorf("%s is empty", cfg.APIKeyEnv)
	}

	// Ensure state dir exists.
	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("create state dir: %w", err)
	}

	// Unified history shared across all channels.
	history := core.NewHistory(filepath.Join(cfg.StateDir, "history.json"), cfg.TokenBudget)

	// Memory lives under ~/.claude/channels by convention.
	memRoot := expandHome("~/.claude/channels")
	mem := memory.NewLoader(memRoot)

	// Primary API client.
	primary := core.NewAPIClient(cfg.BaseURL, apiKey, cfg.Model)

	// Tool registry.
	registry := tools.NewRegistry()

	// Obsidian tools (if vault_root configured).
	if cfg.VaultRoot != "" {
		vaultRoot := expandHome(cfg.VaultRoot)
		tools.RegisterObsidianTools(registry, vaultRoot)
		slog.Info("obsidian tools registered", "vault", vaultRoot)
	}

	// Fastmail tools (if credentials configured).
	if cfg.FastmailTokenEnv != "" || cfg.FastmailUser != "" {
		fmToken := os.Getenv(cfg.FastmailTokenEnv)
		fmPassword := ""
		if cfg.FastmailPasswordEnv != "" {
			fmPassword = os.Getenv(cfg.FastmailPasswordEnv)
		}
		caldavURL := "https://caldav.fastmail.com"

		// Discover real calendar path identifiers so Claude's "Personal"
		// resolves to whatever Fastmail actually uses (e.g. "Default" or
		// a GUID). Non-fatal: if discovery fails the map stays nil and
		// the handler passes the display name through.
		var calendarPaths map[string]string
		if fmPassword != "" && cfg.FastmailUser != "" {
			discoverCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			paths, err := tools.DiscoverCalendars(discoverCtx, caldavURL, cfg.FastmailUser, fmPassword)
			cancel()
			if err != nil {
				slog.Warn("fastmail calendar discovery failed; calendar names will be passed through raw", "err", err)
			} else {
				calendarPaths = paths
				names := make([]string, 0, len(paths))
				for n := range paths {
					names = append(names, n)
				}
				slog.Info("fastmail calendars discovered", "count", len(paths), "names", names)
			}
		}

		tools.RegisterFastmailTools(registry,
			caldavURL,
			cfg.FastmailUser, fmPassword,
			"https://api.fastmail.com/jmap/api/", fmToken,
			calendarPaths,
		)
		slog.Info("fastmail tools registered")
	}

	// Spawn claude tool (if binary configured or found in PATH).
	claudeBin := cfg.ClaudeBinary
	if claudeBin == "" {
		if found, err := exec.LookPath("claude"); err == nil {
			claudeBin = found
		}
	} else {
		claudeBin = expandHome(claudeBin)
	}
	if claudeBin != "" {
		tools.RegisterClaudeTools(registry, claudeBin)
		slog.Info("spawn_claude_code tool registered", "binary", claudeBin)
	}

	// Wire tools into API client.
	schemas := registry.Schemas()
	if len(schemas) > 0 {
		rawSchemas := make([]json.RawMessage, len(schemas))
		for i, s := range schemas {
			rawSchemas[i], _ = json.Marshal(s)
		}
		primary.SetToolSchemas(rawSchemas)
		primary.SetToolExecutor(registry.Execute)
	}

	// Fallback tiers.
	var tiers []core.FallbackTier
	if cfg.Fallback.Enabled {
		for _, t := range cfg.Fallback.Tiers {
			tierAPIKey := ""
			if t.APIKeyEnv != "" {
				tierAPIKey = os.Getenv(t.APIKeyEnv)
			}
			if t.AuthTokenEnv != "" {
				if tok := os.Getenv(t.AuthTokenEnv); tok != "" {
					tierAPIKey = tok
				}
			}
			if tierAPIKey == "" {
				slog.Warn("fallback tier has no usable key; skipping",
					"label", t.Label,
				)
				continue
			}
			client := core.NewAPIClient(t.BaseURL, tierAPIKey, t.Model)
			tiers = append(tiers, core.FallbackTier{
				Client: client,
				Label:  t.Label,
			})
			slog.Info("fallback tier configured",
				"label", t.Label,
				"base_url", t.BaseURL,
				"model", t.Model,
			)
		}
	}

	fb := core.NewFallbackRunner(primary, tiers)

	return core.NewDispatcher(cfg, core.NewAccess(cfg), history, mem, fb), cfg, nil
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
