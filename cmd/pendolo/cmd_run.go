package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/registry"
	"github.com/enthonysclafani/automatizzatore/internal/server"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// findAutomation accepts a folder or the name of an automation in the
// configured automations folder.
func findAutomation(cfg config.Config, arg string) (engine.Automation, error) {
	if info, err := os.Stat(filepath.Join(arg, manifest.FileName)); err == nil && !info.IsDir() {
		a, inv := registry.LoadFolder(arg)
		if inv != nil {
			return a, invalidError(inv)
		}
		return a, nil
	}
	snap, err := registry.Load(cfg.AutomationsDir)
	if err != nil {
		return engine.Automation{}, fmt.Errorf("%q is not an automation folder, and the automations folder cannot be read: %w", arg, err)
	}
	if a, ok := snap.Valid[arg]; ok {
		return a, nil
	}
	for _, inv := range snap.Invalid {
		if inv.Name == arg {
			return engine.Automation{}, invalidError(&inv)
		}
	}
	return engine.Automation{}, fmt.Errorf("no automation named %q in %s", arg, cfg.AutomationsDir)
}

func invalidError(inv *registry.Invalid) error {
	msg := fmt.Sprintf("automation %s is invalid:", inv.Path)
	for _, p := range inv.Problems {
		if p.Severity == manifest.SeverityError {
			msg += "\n  " + p.String()
		}
	}
	return errors.New(msg)
}

func cmdRun(args []string, stdout, stderr io.Writer) int {
	c := newCommand("run", stderr, true)
	verbose := c.fs.Bool("verbose", false, "log while running")
	pos, err := c.parse(args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "usage: pendolo run [--config f] [--json] [--verbose] <folder|name>")
		return exitUsage
	}
	st, cfg, err := c.openStore()
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	a, err := findAutomation(cfg, pos[0])
	if err != nil {
		return fail(stderr, err)
	}

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	// A run started by hand executes scripts here, in the foreground.
	v, err := server.OpenVault(cfg, log)
	if err != nil {
		return fail(stderr, err)
	}
	eng, err := server.NewEngine(cfg, st, v, log, false)
	if err != nil {
		return fail(stderr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	run, err := st.StartNow(ctx, store.NewRun{
		Automation: a.Name, Workspace: a.Workspace(), ManifestHash: a.Hash, TriggerType: "manual",
		TriggerData: map[string]any{"by": "cli"},
	}, "cli", time.Now())
	if err != nil {
		return fail(stderr, err)
	}
	out := eng.Execute(ctx, run, a)
	if code := printRun(context.Background(), st, run.ID, c.json(), false, stdout, stderr); code != exitOK {
		return code
	}
	if out.Status != store.RunSucceeded {
		return exitInvalid
	}
	return exitOK
}

func cmdTrigger(args []string, stdout, stderr io.Writer) int {
	c := newCommand("trigger", stderr, false)
	pos, err := c.parse(args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "usage: pendolo trigger [--config f] <name>")
		return exitUsage
	}
	st, cfg, err := c.openStore()
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	a, err := findAutomation(cfg, pos[0])
	if err != nil {
		return fail(stderr, err)
	}
	id, _, err := st.Enqueue(context.Background(), store.NewRun{
		Automation: a.Name, Workspace: a.Workspace(), ManifestHash: a.Hash, TriggerType: "manual", TriggerData: map[string]any{"by": "cli"},
	}, time.Now())
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "queued run %s of %s; the server starts it within a second\n", id, a.Name)
	return exitOK
}
