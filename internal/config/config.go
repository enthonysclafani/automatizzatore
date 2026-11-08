// Package config loads the Pendolo server configuration.
//
// Configuration comes from a YAML file (pendolo.yaml) and a few environment
// variables. Every field has a default, so Pendolo starts without a file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config is the whole server configuration.
type Config struct {
	// DataDir holds the SQLite database.
	DataDir string `yaml:"dataDir"`
	// AutomationsDir holds one folder per automation.
	AutomationsDir string `yaml:"automationsDir"`
	// Workers is how many runs execute at the same time.
	Workers int `yaml:"workers"`

	Server    Server              `yaml:"server"`
	Runner    Runner              `yaml:"runner"`
	Auth      Auth                `yaml:"auth"`
	Notify    Notify              `yaml:"notify"`
	Heartbeat Heartbeat           `yaml:"heartbeat"`
	Secrets   Secrets             `yaml:"secrets"`
	Retention Retention           `yaml:"retention"`
	Builder   Builder             `yaml:"builder"`
	Providers map[string]Provider `yaml:"providers"`
	Models    map[string]Model    `yaml:"models"`
}

// Server configures the HTTP listener (health and, later, the API).
type Server struct {
	Listen string `yaml:"listen"`
	// PublicURL is how people reach Pendolo, e.g. https://pendolo.example.com.
	// OAuth redirects and links in notifications use it.
	PublicURL string `yaml:"publicURL"`
	// TrustProxy reads the client address and scheme from X-Forwarded-For
	// and X-Forwarded-Proto. Enable it only behind a reverse proxy.
	TrustProxy bool `yaml:"trustProxy"`
}

// Auth configures dashboard sign-in.
type Auth struct {
	// SessionTTL is how long a browser stays signed in without activity.
	SessionTTL Duration `yaml:"sessionTTL"`
	OIDC       OIDC     `yaml:"oidc"`
}

// OIDC enables single sign-on with any OpenID Connect provider (Google,
// Microsoft Entra, Clerk, Authentik, Keycloak, Zitadel...).
type OIDC struct {
	Issuer       string `yaml:"issuer"`
	ClientID     string `yaml:"clientID"`
	ClientSecret string `yaml:"clientSecret"`
	// RedirectURL is https://<your host>/api/v1/auth/oidc/callback.
	RedirectURL string `yaml:"redirectURL"`
	// Label is shown on the button: "Sign in with <label>".
	Label string `yaml:"label"`
	// AutoCreate creates unknown users whose email domain is in
	// AllowedDomains; they start without workspaces.
	AutoCreate     bool     `yaml:"autoCreate"`
	AllowedDomains []string `yaml:"allowedDomains"`
}

// Enabled reports whether OIDC is configured.
func (o OIDC) Enabled() bool { return o.Issuer != "" && o.ClientID != "" }

// Runner points shell steps at a separate `pendolo runner` process. Without
// a URL, scripts run as child processes of the server.
type Runner struct {
	URL string `yaml:"url"`
	// Token is shared with the runner, usually a secret:// reference.
	Token string `yaml:"token"`
}

// Notify holds the credentials of notification channels.
type Notify struct {
	Slack    SlackNotify    `yaml:"slack"`
	Telegram TelegramNotify `yaml:"telegram"`
	Email    EmailNotify    `yaml:"email"`
}

// SlackNotify posts with a bot token (chat:write scope).
type SlackNotify struct {
	Token string `yaml:"token"`
}

// TelegramNotify posts with a bot token from @BotFather.
type TelegramNotify struct {
	BotToken string `yaml:"botToken"`
}

// EmailNotify sends through an SMTP server.
type EmailNotify struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
	// Security is starttls (default), tls (implicit, usually port 465) or none.
	Security string `yaml:"security"`
}

// Heartbeat pings an external monitor so you learn when Pendolo itself stops.
type Heartbeat struct {
	// URL is pinged with GET every Interval. It may be a secret:// reference.
	URL      string   `yaml:"url"`
	Interval Duration `yaml:"interval"`
}

// Secrets says where secret:// references are read from.
type Secrets struct {
	// MasterKeyFile holds the base64 key that encrypts secrets stored in the
	// dashboard. Without it (and without PENDOLO_MASTER_KEY) a key is
	// generated in dataDir/master.key.
	MasterKeyFile string `yaml:"masterKeyFile"`
	// Dir holds one file per secret, named after it (Docker secrets layout).
	Dir string `yaml:"dir"`
	// EnvPrefix: secret://server-ssh-key is read from PENDOLO_SECRET_SERVER_SSH_KEY.
	EnvPrefix string `yaml:"envPrefix"`
}

// Retention controls how long run history is kept.
type Retention struct {
	Runs Duration `yaml:"runs"`
}

// Builder configures the AI builder of the dashboard.
type Builder struct {
	// Model is the alias (or provider/model) that writes automations.
	Model string `yaml:"model"`
	// Disabled hides the builder even when a model is configured.
	Disabled bool `yaml:"disabled"`
}

// Provider is a language model API.
type Provider struct {
	// Type is "anthropic" or "openai" (any OpenAI-compatible API: OpenAI,
	// OpenRouter, Gemini's OpenAI endpoint, Ollama, LM Studio, vLLM, Groq).
	Type    string `yaml:"type"`
	BaseURL string `yaml:"baseURL"`
	// APIKey is usually a secret:// reference.
	APIKey string `yaml:"apiKey"`
}

// Model maps an alias used in automations (fast, smart, local) to a real model.
type Model struct {
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"maxTokens"`
	// Effort is passed to providers that support it (Anthropic: low..max).
	Effort string `yaml:"effort"`
	// Fallbacks enables server-side refusal fallbacks where supported
	// ("default" on Anthropic models that accept it).
	Fallbacks string `yaml:"fallbacks"`
	// Price per million tokens, in EUR. Unknown prices count as zero and are
	// flagged in the run record.
	Price Price `yaml:"price"`
}

// Price per million tokens.
type Price struct {
	Input  float64 `yaml:"input"`
	Output float64 `yaml:"output"`
}

// Duration is a time.Duration written as "5m" in YAML.
type Duration time.Duration

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	parsed, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q", n.Line, n.Value)
	}
	*d = Duration(parsed)
	return nil
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Defaults returns the configuration used when no file is given.
func Defaults() Config {
	return Config{
		DataDir:        "data",
		AutomationsDir: "automations",
		Workers:        4,
		Server:         Server{Listen: ":8080", PublicURL: "http://localhost:8080"},
		Heartbeat:      Heartbeat{Interval: Duration(5 * time.Minute)},
		Secrets:        Secrets{Dir: "/run/secrets", EnvPrefix: "PENDOLO_SECRET_", MasterKeyFile: "/run/secrets/pendolo-master-key"},
		Auth:           Auth{SessionTTL: Duration(14 * 24 * time.Hour)},
		Retention:      Retention{Runs: Duration(90 * 24 * time.Hour)},
		Builder:        Builder{Model: "smart"},
	}
}

// Load reads path (if not empty) over the defaults, then applies
// environment overrides: PENDOLO_DATA_DIR, PENDOLO_AUTOMATIONS_DIR,
// PENDOLO_WORKERS, PENDOLO_LISTEN.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return cfg, err
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
			return cfg, fmt.Errorf("%s: %w", path, err)
		}
	}
	if v := os.Getenv("PENDOLO_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("PENDOLO_AUTOMATIONS_DIR"); v != "" {
		cfg.AutomationsDir = v
	}
	if v := os.Getenv("PENDOLO_PUBLIC_URL"); v != "" {
		cfg.Server.PublicURL = v
	}
	if v := os.Getenv("PENDOLO_LISTEN"); v != "" {
		cfg.Server.Listen = v
	}
	if v := os.Getenv("PENDOLO_WORKERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("PENDOLO_WORKERS: %w", err)
		}
		cfg.Workers = n
	}
	return cfg, cfg.Validate()
}

// Validate checks values that would otherwise fail later and confusingly.
func (c Config) Validate() error {
	var errs []error
	if c.Workers < 1 || c.Workers > 64 {
		errs = append(errs, fmt.Errorf("workers must be between 1 and 64, got %d", c.Workers))
	}
	if c.DataDir == "" {
		errs = append(errs, errors.New("dataDir is empty"))
	}
	if c.AutomationsDir == "" {
		errs = append(errs, errors.New("automationsDir is empty"))
	}
	for name, p := range c.Providers {
		switch p.Type {
		case "anthropic", "openai":
		default:
			errs = append(errs, fmt.Errorf("provider %q: type must be anthropic or openai, got %q", name, p.Type))
		}
		if p.Type == "openai" && p.BaseURL == "" {
			errs = append(errs, fmt.Errorf("provider %q: baseURL is required for type openai", name))
		}
	}
	for alias, m := range c.Models {
		if _, ok := c.Providers[m.Provider]; !ok {
			errs = append(errs, fmt.Errorf("model %q: unknown provider %q", alias, m.Provider))
		}
		if m.Model == "" {
			errs = append(errs, fmt.Errorf("model %q: model is empty", alias))
		}
	}
	if o := c.Auth.OIDC; (o.Issuer != "") != (o.ClientID != "") || (o.Enabled() && o.RedirectURL == "") {
		errs = append(errs, errors.New("auth.oidc needs issuer, clientID and redirectURL together"))
	}
	if c.Auth.SessionTTL.Std() < 5*time.Minute {
		errs = append(errs, errors.New("auth.sessionTTL must be at least 5m"))
	}
	if c.Runner.URL != "" && c.Runner.Token == "" {
		errs = append(errs, errors.New("runner.token is required when runner.url is set"))
	}
	switch c.Notify.Email.Security {
	case "", "starttls", "tls", "none":
	default:
		errs = append(errs, fmt.Errorf("notify.email.security must be starttls, tls or none, got %q", c.Notify.Email.Security))
	}
	if c.Notify.Email.Host != "" && c.Notify.Email.From == "" {
		errs = append(errs, errors.New("notify.email.from is required when notify.email.host is set"))
	}
	if c.Heartbeat.URL != "" && c.Heartbeat.Interval.Std() < 10*time.Second {
		errs = append(errs, errors.New("heartbeat.interval must be at least 10s"))
	}
	return errors.Join(errs...)
}
