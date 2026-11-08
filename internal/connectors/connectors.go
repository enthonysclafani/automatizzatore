// Package connectors links automations to outside services.
//
// A connection is a set of credentials for one service in one workspace
// (stored sealed with the master key). A connector kind (google, slack,
// websearch, mcp) turns a connection into actions such as gmail.search or
// slack.send_message. Every action declares its risk, which the engine
// weighs against the automation's autonomy before running it.
package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/internal/vault"
)

// Risk is how much harm an action can do if it runs when it should not.
type Risk string

// Risks, from the least to the most dangerous.
const (
	RiskRead        Risk = "read"        // only reads
	RiskWrite       Risk = "write"       // changes that stay reversible: labels, drafts, documents
	RiskSend        Risk = "send"        // reaches other people: emails, chat messages
	RiskDestructive Risk = "destructive" // deletes or overwrites
)

// Kinds of connection.
const (
	KindGoogle    = "google"
	KindSlack     = "slack"
	KindWebSearch = "websearch"
	KindMCP       = "mcp"
)

// ActionInfo describes an action.
type ActionInfo struct {
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	Risk             Risk           `json:"risk"`
	DraftAlternative string         `json:"draftAlternative,omitempty"`
	Input            map[string]any `json:"-"`
}

// Bound is an action ready to call with a connection's credentials.
type Bound struct {
	ActionInfo
	Connection string
	call       func(ctx context.Context, input map[string]any) (any, error)
	schema     *jsonschema.Schema
}

// Call validates input against the action's schema and runs the action.
func (b *Bound) Call(ctx context.Context, input map[string]any) (any, error) {
	if input == nil {
		input = map[string]any{}
	}
	if b.schema != nil {
		if err := b.schema.Validate(jsonValue(input)); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %s", b.Name, oneLine(err.Error()))
		}
	}
	return b.call(ctx, input)
}

// jsonValue round-trips through JSON so the validator sees plain JSON types.
func jsonValue(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// Hub resolves actions for the engine and manages connections for the API.
type Hub struct {
	Store *store.Store
	Vault *vault.Vault
	Log   *slog.Logger
	// PublicURL is where people reach Pendolo, for OAuth redirects.
	PublicURL string
	// HTTP is used for every outside call.
	HTTP *http.Client
	// Endpoints overrides service base URLs, for tests: "gmail", "docs",
	// "google_oauth", "slack", "brave", "tavily".
	Endpoints map[string]string
	// AllowPrivateFetch lets web.fetch reach private and loopback addresses.
	AllowPrivateFetch bool

	mcpMu    sync.Mutex
	mcpTools map[string]mcpCache
}

func (h *Hub) endpoint(name, def string) string {
	if v, ok := h.Endpoints[name]; ok {
		return strings.TrimRight(v, "/")
	}
	return def
}

func (h *Hub) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (h *Hub) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

// prefixKinds maps action prefixes of built-in connectors to the kind of
// connection they need.
var prefixKinds = map[string]string{
	"gmail":       KindGoogle,
	"google_docs": KindGoogle,
	"slack":       KindSlack,
	"web":         KindWebSearch,
}

// Resolve finds the action named "<prefix>.<action>" for a workspace. The
// connection is the one named by connName, or else the workspace's
// connection named like the prefix, or its only connection of the kind.
// For MCP, the prefix is the connection's name.
func (h *Hub) Resolve(ctx context.Context, workspace, action, connName string) (*Bound, error) {
	prefix, rest, ok := strings.Cut(action, ".")
	if !ok || rest == "" {
		return nil, fmt.Errorf("action %q must look like <connector>.<action>", action)
	}
	kind, builtin := prefixKinds[prefix]
	if !builtin {
		name := connName
		if name == "" {
			name = prefix
		}
		conn, err := h.connection(ctx, workspace, name, KindMCP)
		if err != nil {
			return nil, err
		}
		return h.mcpAction(ctx, conn, action, rest)
	}
	if prefix == "web" && rest == "fetch" {
		return h.bind(webActionInfos["web.fetch"], "", func(ctx context.Context, in map[string]any) (any, error) {
			return h.webFetch(ctx, str(in, "url"))
		})
	}
	conn, err := h.findConnection(ctx, workspace, kind, prefix, connName)
	if err != nil {
		return nil, err
	}
	var actions map[string]actionImpl
	switch kind {
	case KindGoogle:
		actions = h.googleActions(conn)
	case KindSlack:
		actions = h.slackActions(conn)
	case KindWebSearch:
		actions = h.webActions(conn)
	}
	impl, ok := actions[action]
	if !ok {
		return nil, fmt.Errorf("unknown action %q; %s offers: %s", action, conn.Name, strings.Join(sortedKeys(actions), ", "))
	}
	return h.bind(impl.info, conn.Name, impl.run)
}

type actionImpl struct {
	info ActionInfo
	run  func(ctx context.Context, input map[string]any) (any, error)
}

func (h *Hub) bind(info ActionInfo, conn string, run func(context.Context, map[string]any) (any, error)) (*Bound, error) {
	b := &Bound{ActionInfo: info, Connection: conn, call: run}
	if info.Input != nil {
		c := jsonschema.NewCompiler()
		url := "mem://" + info.Name + ".json"
		if err := c.AddResource(url, jsonValue(info.Input)); err == nil {
			b.schema, _ = c.Compile(url)
		}
	}
	return b, nil
}

// connection loads a connection of an expected kind.
func (h *Hub) connection(ctx context.Context, workspace, name, kind string) (*Conn, error) {
	c, err := h.Store.GetConnection(ctx, workspace, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("no connection %q in workspace %s; add it on the Connections page", name, workspace)
	}
	if err != nil {
		return nil, err
	}
	if c.Kind != kind {
		return nil, fmt.Errorf("connection %q is a %s connection, not %s", name, c.Kind, kind)
	}
	return h.open(c)
}

func (h *Hub) findConnection(ctx context.Context, workspace, kind, prefix, name string) (*Conn, error) {
	if name != "" {
		return h.connection(ctx, workspace, name, kind)
	}
	list, err := h.Store.ListConnections(ctx, workspace)
	if err != nil {
		return nil, err
	}
	var ofKind []store.Connection
	for _, c := range list {
		if c.Kind == kind {
			if c.Name == prefix {
				return h.open(&c)
			}
			ofKind = append(ofKind, c)
		}
	}
	switch len(ofKind) {
	case 1:
		return h.open(&ofKind[0])
	case 0:
		return nil, fmt.Errorf("no %s connection in workspace %s; add one on the Connections page", kind, workspace)
	default:
		return nil, fmt.Errorf("several %s connections in workspace %s; choose one with connection: <name>", kind, workspace)
	}
}

// GoogleConnection opens a workspace's Google connection by name.
func (h *Hub) GoogleConnection(ctx context.Context, workspace, name string) (*Conn, error) {
	return h.connection(ctx, workspace, name, KindGoogle)
}

// Conn is a connection with its credentials opened.
type Conn struct {
	store.Connection
	Secrets map[string]string
}

func sealContext(workspace, name string) string { return "connection:" + workspace + ":" + name }

func (h *Hub) open(c *store.Connection) (*Conn, error) {
	conn := &Conn{Connection: *c, Secrets: map[string]string{}}
	if c.Sealed == "" {
		return conn, nil
	}
	plain, err := h.Vault.Open(c.Sealed, sealContext(c.Workspace, c.Name))
	if err != nil {
		return nil, fmt.Errorf("connection %s: %w", c.Name, err)
	}
	if err := json.Unmarshal(plain, &conn.Secrets); err != nil {
		return nil, fmt.Errorf("connection %s: credentials unreadable", c.Name)
	}
	return conn, nil
}

// Seal encrypts a connection's credentials for storage.
func (h *Hub) Seal(workspace, name string, secrets map[string]string) (string, error) {
	if len(secrets) == 0 {
		return "", nil
	}
	b, err := json.Marshal(secrets)
	if err != nil {
		return "", err
	}
	return h.Vault.Seal(b, sealContext(workspace, name))
}

// Open returns a stored connection with its credentials, for the API.
func (h *Hub) Open(c *store.Connection) (*Conn, error) { return h.open(c) }

// saveSecrets stores refreshed credentials (e.g. a new OAuth token).
func (h *Hub) saveSecrets(ctx context.Context, c *Conn, status, errMsg, account string) {
	sealed, err := h.Seal(c.Workspace, c.Name, c.Secrets)
	if err != nil {
		h.log().Error("seal connection", "connection", c.Name, "err", err)
		return
	}
	if err := h.Store.UpdateConnectionState(ctx, c.Workspace, c.Name, sealed, status, errMsg, account, time.Now()); err != nil {
		h.log().Error("save connection", "connection", c.Name, "err", err)
	}
}

// Actions lists what a connection offers. For MCP it asks the server.
func (h *Hub) Actions(ctx context.Context, c *store.Connection) []ActionInfo {
	conn, err := h.open(c)
	if err != nil {
		return []ActionInfo{}
	}
	var impls map[string]actionImpl
	switch c.Kind {
	case KindGoogle:
		impls = h.googleActions(conn)
	case KindSlack:
		impls = h.slackActions(conn)
	case KindWebSearch:
		impls = h.webActions(conn)
	case KindMCP:
		tools, err := h.mcpList(ctx, conn)
		if err != nil {
			return []ActionInfo{}
		}
		out := make([]ActionInfo, 0, len(tools))
		for _, t := range tools {
			out = append(out, t.info(c.Name))
		}
		return out
	}
	out := make([]ActionInfo, 0, len(impls))
	for _, k := range sortedKeys(impls) {
		out = append(out, impls[k].info)
	}
	return out
}

// Test checks a connection's credentials with a harmless call.
func (h *Hub) Test(ctx context.Context, c *store.Connection) (bool, string) {
	conn, err := h.open(c)
	if err != nil {
		return false, err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var detail string
	switch c.Kind {
	case KindGoogle:
		detail, err = h.googleTest(ctx, conn)
	case KindSlack:
		detail, err = h.slackTest(ctx, conn)
	case KindWebSearch:
		detail, err = h.webTest(ctx, conn)
	case KindMCP:
		var tools []mcpTool
		tools, err = h.mcpList(ctx, conn)
		detail = fmt.Sprintf("connected; %d tools", len(tools))
	default:
		err = fmt.Errorf("unknown kind %q", c.Kind)
	}
	if err != nil {
		return false, err.Error()
	}
	return true, detail
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}

func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func strList(m map[string]any, key string) []string {
	switch v := m[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}

func intOr(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return def
}

// obj builds a JSON Schema object with the given properties and required names.
func obj(required []string, props map[string]any) map[string]any {
	r := make([]any, len(required))
	for i, x := range required {
		r[i] = x
	}
	return map[string]any{"type": "object", "properties": props, "required": r, "additionalProperties": false}
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

func listOf(typ, desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": typ}, "description": desc}
}
