package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"imap-proxy/internal/config"
	"imap-proxy/internal/proxy"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	logger.Info("starting imap-proxy", "listen", cfg.Server.Listen, "accounts", len(cfg.Accounts))

	srv := proxy.NewServer(cfg, logger)

	// Handle signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != nil {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
}
