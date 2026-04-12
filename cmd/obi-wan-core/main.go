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
	"syscall"
	"time"

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

	d, err := buildDispatcher(expandHome(*configPath))
	if err != nil {
		return err
	}

	slog.Info("obi-wan-core starting in serve mode",
		"clients", "none yet (Plan 2/3 wiring)",
	)

	// Plan 1 serve mode: just wait for signal. Plan 2/3 will launch the
	// Telegram bot, webhook server, and R1 WebSocket server here.
	_ = d
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	slog.Info("obi-wan-core shutting down")
	return nil
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

func buildDispatcher(cfgPath string) (*core.Dispatcher, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	sessions, err := core.NewSessionStore(cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}

	// Memory lives under ~/.claude/channels by convention, not under
	// state_dir — shared with Claude Code's existing channel memory.
	memRoot := expandHome("~/.claude/channels")
	mem := memory.NewLoader(memRoot)

	runner := core.NewClaudeRunner(cfg.ClaudeBinary, "sonnet")

	return core.NewDispatcher(cfg, core.NewAccess(cfg), sessions, mem, runner), nil
}

func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
