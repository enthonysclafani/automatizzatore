package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/runner"
)

func cmdRunner(args []string, _, stderr io.Writer) int {
	c := newCommand("runner", stderr, false)
	listen := c.fs.String("listen", envOr("PENDOLO_RUNNER_LISTEN", ":8090"), "address to listen on")
	root := c.fs.String("automations", envOr("PENDOLO_AUTOMATIONS_DIR", "automations"), "automations folder (read-only is enough)")
	tokenFile := c.fs.String("token-file", envOr("PENDOLO_RUNNER_TOKEN_FILE", "/run/secrets/runner-token"), "file holding the shared token")
	if _, err := c.parse(args); err != nil {
		return exitUsage
	}
	token := os.Getenv("PENDOLO_RUNNER_TOKEN")
	if token == "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			return fail(stderr, fmt.Errorf("no token: set PENDOLO_RUNNER_TOKEN or create %s", *tokenFile))
		}
		token = strings.TrimSpace(string(b))
	}
	if len(token) < 16 {
		return fail(stderr, errors.New("the runner token must be at least 16 characters"))
	}
	log := slog.New(slog.NewTextHandler(stderr, nil)).With("component", "runner")
	srv := &http.Server{Addr: *listen, Handler: runner.Handler{Root: *root, Token: token}, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("runner started", "listen", *listen, "automations", *root)
	select {
	case err := <-errc:
		return fail(stderr, err)
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	log.Info("runner stopped")
	return exitOK
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
