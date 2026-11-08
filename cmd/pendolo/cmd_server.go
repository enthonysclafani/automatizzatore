package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/enthonysclafani/automatizzatore/internal/server"
	"github.com/enthonysclafani/automatizzatore/internal/version"
)

func cmdServer(args []string, _, stderr io.Writer) int {
	c := newCommand("server", stderr, false)
	logFormat := c.fs.String("log-format", "text", "log format: text or json")
	if _, err := c.parse(args); err != nil {
		return exitUsage
	}
	cfg, err := c.config()
	if err != nil {
		return fail(stderr, err)
	}
	var handler slog.Handler = slog.NewTextHandler(stderr, nil)
	if *logFormat == "json" {
		handler = slog.NewJSONHandler(stderr, nil)
	}
	log := slog.New(handler).With("component", "pendolo")
	slog.SetDefault(log)
	log.Info("starting", "version", version.Version, "data", cfg.DataDir, "automations", cfg.AutomationsDir)

	srv, err := server.New(cfg, log)
	if err != nil {
		return fail(stderr, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitInvalid
	}
	return exitOK
}
