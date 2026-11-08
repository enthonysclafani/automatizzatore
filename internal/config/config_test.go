package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pendolo.yaml")
	body := `workers: 2
heartbeat: { url: "https://hc.example/ping", interval: 1m }
providers:
  anthropic: { type: anthropic, apiKey: "secret://anthropic-api-key" }
models:
  smart: { provider: anthropic, model: claude-opus-5-5, price: { input: 4, output: 20 } }
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PENDOLO_DATA_DIR", "/var/lib/pendolo")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workers != 2 || cfg.DataDir != "/var/lib/pendolo" || cfg.Heartbeat.Interval.Std() != time.Minute ||
		cfg.Models["smart"].Price.Output != 20 || cfg.Secrets.EnvPrefix != "PENDOLO_SECRET_" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]string{
		"unknown field":    "wokers: 2\n",
		"bad duration":     "heartbeat: { interval: soon }\n",
		"unknown provider": "models: { smart: { provider: nope, model: m } }\n",
		"bad type":         "providers: { p: { type: magic } }\n",
		"openai needs url": "providers: { p: { type: openai } }\n",
		"workers":          "workers: 0\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "p.yaml")
			_ = os.WriteFile(path, []byte(body), 0o644)
			if _, err := Load(path); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestEmptyFileMeansDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.yaml")
	_ = os.WriteFile(path, nil, 0o644)
	cfg, err := Load(path)
	if err != nil || cfg.Workers != 4 || !strings.HasSuffix(cfg.Server.Listen, ":8080") {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
}
