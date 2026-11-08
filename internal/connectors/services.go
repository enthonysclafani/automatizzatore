package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

func htmlUnescape(s string) string { return html.UnescapeString(s) }

// ---- Slack ----

var slackActionInfos = map[string]ActionInfo{
	"slack.send_message": {Name: "slack.send_message", Risk: RiskSend,
		Description: "Post a message to a Slack channel the bot is a member of.",
		Input: obj([]string{"channel", "text"}, map[string]any{
			"channel": prop("string", "Channel name like #ops, or a channel id"), "text": prop("string", "Message text (Slack markdown)"),
		})},
}

func (h *Hub) slackCall(ctx context.Context, c *Conn, method string, body map[string]any) (map[string]any, error) {
	token := c.Secrets["token"]
	if token == "" {
		return nil, fmt.Errorf("connection %s has no bot token", c.Name)
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpoint("slack", "https://slack.com/api")+"/"+method, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := h.client().Do(req)
	if err != nil {
		return nil, errors.New("slack: request failed")
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("slack: HTTP %d", resp.StatusCode)
	}
	if ok, _ := out["ok"].(bool); !ok {
		return nil, fmt.Errorf("slack: %v", out["error"])
	}
	return out, nil
}

func (h *Hub) slackActions(c *Conn) map[string]actionImpl {
	return map[string]actionImpl{
		"slack.send_message": {info: slackActionInfos["slack.send_message"], run: func(ctx context.Context, in map[string]any) (any, error) {
			out, err := h.slackCall(ctx, c, "chat.postMessage", map[string]any{"channel": str(in, "channel"), "text": str(in, "text")})
			if err != nil {
				return nil, err
			}
			return map[string]any{"channel": out["channel"], "ts": out["ts"]}, nil
		}},
	}
}

func (h *Hub) slackTest(ctx context.Context, c *Conn) (string, error) {
	out, err := h.slackCall(ctx, c, "auth.test", map[string]any{})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("connected to %v as %v", out["team"], out["user"]), nil
}

// ---- Web: search and fetch ----

var webActionInfos = map[string]ActionInfo{
	"web.search": {Name: "web.search", Risk: RiskRead,
		Description: "Search the web; returns titles, URLs and snippets.",
		Input: obj([]string{"query"}, map[string]any{
			"query": prop("string", "What to search for"), "count": prop("integer", "1 to 20 results, default 8"),
		})},
	"web.fetch": {Name: "web.fetch", Risk: RiskRead,
		Description: "Download a public web page and return its title and readable text.",
		Input:       obj([]string{"url"}, map[string]any{"url": prop("string", "http or https URL")})},
}

// SearchResult is one web search hit.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func (h *Hub) webActions(c *Conn) map[string]actionImpl {
	return map[string]actionImpl{
		"web.search": {info: webActionInfos["web.search"], run: func(ctx context.Context, in map[string]any) (any, error) {
			count := intOr(in, "count", 8)
			if count < 1 || count > 20 {
				count = 8
			}
			return h.webSearch(ctx, c, str(in, "query"), count)
		}},
		"web.fetch": {info: webActionInfos["web.fetch"], run: func(ctx context.Context, in map[string]any) (any, error) {
			return h.webFetch(ctx, str(in, "url"))
		}},
	}
}

func (h *Hub) getJSON(ctx context.Context, method, u string, headers map[string]string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return errors.New("request failed")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, oneLine(string(raw)))
	}
	return json.Unmarshal(raw, out)
}

func (h *Hub) webSearch(ctx context.Context, c *Conn, query string, count int) ([]SearchResult, error) {
	out := []SearchResult{}
	switch provider := str(c.Config, "provider"); provider {
	case "brave":
		var r struct {
			Web struct {
				Results []struct {
					Title       string `json:"title"`
					URL         string `json:"url"`
					Description string `json:"description"`
				} `json:"results"`
			} `json:"web"`
		}
		u := h.endpoint("brave", "https://api.search.brave.com") + "/res/v1/web/search?" + url.Values{"q": {query}, "count": {fmt.Sprint(count)}}.Encode()
		if err := h.getJSON(ctx, http.MethodGet, u, map[string]string{"X-Subscription-Token": c.Secrets["apiKey"]}, nil, &r); err != nil {
			return nil, fmt.Errorf("brave search: %w", err)
		}
		for _, x := range r.Web.Results {
			out = append(out, SearchResult{Title: x.Title, URL: x.URL, Snippet: htmlToText(x.Description)})
		}
	case "tavily":
		var r struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
			} `json:"results"`
		}
		if err := h.getJSON(ctx, http.MethodPost, h.endpoint("tavily", "https://api.tavily.com")+"/search",
			map[string]string{"Authorization": "Bearer " + c.Secrets["apiKey"]}, map[string]any{"query": query, "max_results": count}, &r); err != nil {
			return nil, fmt.Errorf("tavily search: %w", err)
		}
		for _, x := range r.Results {
			out = append(out, SearchResult{Title: x.Title, URL: x.URL, Snippet: x.Content})
		}
	case "searxng":
		var r struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
			} `json:"results"`
		}
		base := strings.TrimRight(str(c.Config, "baseURL"), "/")
		if base == "" {
			return nil, errors.New("searxng needs baseURL")
		}
		if err := h.getJSON(ctx, http.MethodGet, base+"/search?"+url.Values{"q": {query}, "format": {"json"}}.Encode(), nil, nil, &r); err != nil {
			return nil, fmt.Errorf("searxng: %w", err)
		}
		for i, x := range r.Results {
			if i >= count {
				break
			}
			out = append(out, SearchResult{Title: x.Title, URL: x.URL, Snippet: x.Content})
		}
	default:
		return nil, fmt.Errorf("unknown search provider %q: use brave, tavily or searxng", provider)
	}
	return out, nil
}

func (h *Hub) webTest(ctx context.Context, c *Conn) (string, error) {
	res, err := h.webSearch(ctx, c, "pendolo automation", 1)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("search works (%d result)", len(res)), nil
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// ErrPrivate is returned for addresses a public-only client refuses.
var ErrPrivate = errors.New("refused: private, loopback, link-local and shared addresses are not contacted")

// nonPublic lists ranges that are not reachable public internet, beyond
// what net.IP's own predicates cover.
var nonPublic = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16"} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// publicOnly refuses connections to private networks, so a page or a
// prompt cannot make Pendolo read internal services.
func publicOnly(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return ErrPrivate
	}
	for _, n := range nonPublic {
		if n.Contains(ip) {
			return ErrPrivate
		}
	}
	return nil
}

// PublicOnlyClient returns an HTTP client that connects only to public
// addresses. It connects directly, without a proxy, so the check sees the
// real destination.
func PublicOnlyClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: publicOnly}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{Proxy: nil, DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

func (h *Hub) fetchClient() *http.Client {
	if h.AllowPrivateFetch {
		return h.client()
	}
	return PublicOnlyClient(30 * time.Second)
}

func (h *Hub) webFetch(ctx context.Context, raw string) (any, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("web.fetch needs an http or https URL, got %q", raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pendolo (+https://github.com/enthonysclafani/automatizzatore)")
	resp, err := h.fetchClient().Do(req)
	if err != nil {
		if errors.Is(err, ErrPrivate) {
			return nil, ErrPrivate
		}
		return nil, fmt.Errorf("fetch %s: %w", u.Host, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	text := string(body)
	title := ""
	if strings.Contains(resp.Header.Get("Content-Type"), "html") {
		if m := titleRe.FindStringSubmatch(text); m != nil {
			title = strings.TrimSpace(htmlUnescape(m[1]))
		}
		text = htmlToText(text)
	}
	if len(text) > 200_000 {
		text = text[:200_000] + "\n[truncated]"
	}
	return map[string]any{"url": resp.Request.URL.String(), "status": resp.StatusCode, "title": title, "text": text}, nil
}
