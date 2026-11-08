package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/server"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// command holds the flags most commands share.
type command struct {
	fs         *flag.FlagSet
	configPath *string
	jsonOut    *bool
}

func newCommand(name string, stderr io.Writer, withJSON bool) *command {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	c := &command{fs: fs, configPath: fs.String("config", "", "configuration file (default $PENDOLO_CONFIG or ./pendolo.yaml)")}
	if withJSON {
		c.jsonOut = fs.Bool("json", false, "print JSON")
	}
	return c
}

// parse accepts flags before and after positional arguments.
func (c *command) parse(args []string) ([]string, error) {
	var positional []string
	for {
		if err := c.fs.Parse(args); err != nil {
			return nil, err
		}
		rest := c.fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func (c *command) json() bool { return c.jsonOut != nil && *c.jsonOut }

func (c *command) config() (config.Config, error) {
	path := *c.configPath
	if path == "" {
		path = os.Getenv("PENDOLO_CONFIG")
	}
	if path == "" {
		if _, err := os.Stat("pendolo.yaml"); err == nil {
			path = "pendolo.yaml"
		}
	}
	return config.Load(path)
}

func (c *command) openStore() (*store.Store, config.Config, error) {
	cfg, err := c.config()
	if err != nil {
		return nil, cfg, err
	}
	st, err := store.Open(server.DBPath(cfg))
	return st, cfg, err
}

func printJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "error:", err)
	return exitInvalid
}

func short(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func duration(start, end *time.Time) string {
	if start == nil {
		return "-"
	}
	stop := time.Now()
	if end != nil {
		stop = *end
	}
	d := stop.Sub(*start)
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

func local(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
