// Command modeltrack runs the Discord free-model tracker.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ylnodz/modeltrack-go/internal/api"
	"github.com/ylnodz/modeltrack-go/internal/bot"
	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/state"
	"github.com/ylnodz/modeltrack-go/internal/tracker"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println("modeltrack-go", version)
		return
	}

	cfg := config.Load()
	setupLogging(cfg.LogLevel)

	if cfg.DiscordBotToken == "" || cfg.DiscordChannelID == "" {
		slog.Error("DISCORD_BOT_TOKEN and DISCORD_CHANNEL_ID are required")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config) error {
	store, err := state.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	defer store.Close()

	trk := tracker.New(cfg, store, nil)

	b, err := bot.New(cfg, store, trk)
	if err != nil {
		return fmt.Errorf("bot: %w", err)
	}

	errCh := make(chan error, 2)
	go func() { errCh <- api.Serve(ctx, cfg, store, trk) }()
	go func() { errCh <- b.Run(ctx) }()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
		return nil
	case err := <-errCh:
		return err
	}
}

func setupLogging(level string) {
	var lvl slog.Level
	switch level {
	case "DEBUG":
		lvl = slog.LevelDebug
	case "WARN":
		lvl = slog.LevelWarn
	case "ERROR":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(handler))
}
