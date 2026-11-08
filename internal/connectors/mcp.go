package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/enthonysclafani/automatizzatore/internal/version"
)

// HeaderPrefix marks MCP connection secrets that are sent as HTTP headers,
// e.g. "header:Authorization".
const HeaderPrefix = "header:"

type mcpTool struct {
	Name        string
	Description string
	Schema      map[string]any
	Risk        Risk
}

func (t mcpTool) info(conn string) ActionInfo {
	return ActionInfo{Name: conn + "." + t.Name, Description: t.Description, Risk: t.Risk, Input: t.Schema}
}

type mcpCache struct {
	tools []mcpTool
	at    time.Time
	key   string
}

// headerTransport adds the connection's headers to every request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.headers {
		r.Header.Set(k, v)
	}
	return t.base.RoundTrip(r)
}

func (h *Hub) mcpSession(ctx context.Context, c *Conn) (*mcp.ClientSession, error) {
	endpoint := str(c.Config, "url")
	if endpoint == "" {
		return nil, fmt.Errorf("connection %s has no url", c.Name)
	}
	headers := map[string]string{}
	for k, v := range c.Secrets {
		if name, ok := strings.CutPrefix(k, HeaderPrefix); ok {
			headers[name] = v
		}
	}
	base := h.client().Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "pendolo", Version: version.Version}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: headerTransport{base: base, headers: headers}},
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("MCP server %s: %s", c.Name, oneLine(err.Error()))
	}
	return session, nil
}

// riskOf reads a tool's risk from the connection's overrides, then from
// the tool's annotations; tools that say nothing count as writes.
func riskOf(c *Conn, t *mcp.Tool) Risk {
	if overrides, ok := c.Config["risks"].(map[string]any); ok {
		if r, ok := overrides[t.Name].(string); ok {
			switch Risk(r) {
			case RiskRead, RiskWrite, RiskSend, RiskDestructive:
				return Risk(r)
			}
		}
	}
	a := t.Annotations
	switch {
	case a == nil:
		return RiskWrite
	case a.ReadOnlyHint:
		return RiskRead
	case a.DestructiveHint != nil && *a.DestructiveHint:
		return RiskDestructive
	default:
		return RiskWrite
	}
}

func (h *Hub) mcpList(ctx context.Context, c *Conn) ([]mcpTool, error) {
	key := c.Workspace + "/" + c.Name
	stamp := c.UpdatedAt.String()
	h.mcpMu.Lock()
	if cached, ok := h.mcpTools[key]; ok && cached.key == stamp && time.Since(cached.at) < 5*time.Minute {
		h.mcpMu.Unlock()
		return cached.tools, nil
	}
	h.mcpMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	session, err := h.mcpSession(ctx, c)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var tools []mcpTool
	var cursor string
	for page := 0; page < 20; page++ {
		res, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("MCP server %s: list tools: %s", c.Name, oneLine(err.Error()))
		}
		for _, t := range res.Tools {
			var schema map[string]any
			if b, err := json.Marshal(t.InputSchema); err == nil {
				_ = json.Unmarshal(b, &schema)
			}
			tools = append(tools, mcpTool{Name: t.Name, Description: t.Description, Schema: schema, Risk: riskOf(c, t)})
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	h.mcpMu.Lock()
	if h.mcpTools == nil {
		h.mcpTools = map[string]mcpCache{}
	}
	h.mcpTools[key] = mcpCache{tools: tools, at: time.Now(), key: stamp}
	h.mcpMu.Unlock()
	return tools, nil
}

func (h *Hub) mcpAction(ctx context.Context, c *Conn, action, toolName string) (*Bound, error) {
	tools, err := h.mcpList(ctx, c)
	if err != nil {
		return nil, err
	}
	for _, t := range tools {
		if t.Name != toolName {
			continue
		}
		info := t.info(c.Name)
		info.Name = action
		return h.bind(info, c.Name, func(ctx context.Context, in map[string]any) (any, error) {
			return h.mcpCall(ctx, c, toolName, in)
		})
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return nil, fmt.Errorf("MCP server %s has no tool %q; it offers: %s", c.Name, toolName, strings.Join(names, ", "))
}

func (h *Hub) mcpCall(ctx context.Context, c *Conn, tool string, in map[string]any) (any, error) {
	session, err := h.mcpSession(ctx, c)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: in})
	if err != nil {
		return nil, fmt.Errorf("MCP %s.%s: %s", c.Name, tool, oneLine(err.Error()))
	}
	var text strings.Builder
	for _, content := range res.Content {
		if t, ok := content.(*mcp.TextContent); ok {
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(t.Text)
		}
	}
	if res.IsError {
		return nil, errors.New("MCP " + c.Name + "." + tool + ": " + oneLine(text.String()))
	}
	if res.StructuredContent != nil {
		return jsonValue(res.StructuredContent), nil
	}
	// Many servers return JSON as text; hand it over parsed when it is.
	var parsed any
	if json.Unmarshal([]byte(text.String()), &parsed) == nil {
		return parsed, nil
	}
	return text.String(), nil
}

// ConnectionSecretNames lists which secrets a connection holds, for the
// API (values are never returned). MCP headers appear by header name.
func ConnectionSecretNames(c *Conn) []string {
	out := []string{}
	for _, k := range sortedKeys(c.Secrets) {
		if c.Secrets[k] != "" {
			out = append(out, strings.TrimPrefix(k, HeaderPrefix))
		}
	}
	return out
}
