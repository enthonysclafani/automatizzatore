// Package testfakes holds in-memory stand-ins for outside services (Gmail,
// Google Docs, Slack, Brave Search, an MCP server, a language model), so
// tests exercise real connectors end to end without credentials.
package testfakes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/enthonysclafani/automatizzatore/internal/llm"
)

// Message is a fake Gmail message.
type Message struct {
	ID, ThreadID, From, To, Subject, Body, MessageID string
	Labels                                           []string
}

// Google fakes the Gmail and Docs APIs.
type Google struct {
	Server *httptest.Server
	mu     sync.Mutex
	msgs   []*Message
	labels map[string]string // name -> id
	Drafts []string          // raw RFC 822
	Sent   []string
	Docs   map[string]string // id -> text
	nextID int
}

// NewGoogle starts the fake with some messages.
func NewGoogle(t *testing.T, msgs ...*Message) *Google {
	g := &Google{msgs: msgs, labels: map[string]string{"INBOX": "INBOX", "UNREAD": "UNREAD"}, Docs: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/profile", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"emailAddress": "me@example.com"})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages", func(w http.ResponseWriter, _ *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		var list []map[string]any
		for i := len(g.msgs) - 1; i >= 0; i-- { // newest first
			list = append(list, map[string]any{"id": g.msgs[i].ID, "threadId": g.msgs[i].ThreadID})
		}
		writeJSON(w, map[string]any{"messages": list})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		m := g.find(r.PathValue("id"))
		if m == nil {
			http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{
			"id": m.ID, "threadId": m.ThreadID, "labelIds": m.Labels, "snippet": firstWords(m.Body),
			"payload": map[string]any{
				"mimeType": "multipart/alternative",
				"headers": []map[string]string{{"name": "From", "value": m.From}, {"name": "To", "value": m.To},
					{"name": "Subject", "value": m.Subject}, {"name": "Date", "value": "Mon, 9 Nov 2026 09:00:00 +0100"},
					{"name": "Message-ID", "value": m.MessageID}},
				"parts": []map[string]any{
					{"mimeType": "text/html", "body": map[string]any{"data": b64("<p>" + m.Body + "</p>")}},
					{"mimeType": "text/plain", "body": map[string]any{"data": b64(m.Body)}},
				},
			},
		})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/labels", func(w http.ResponseWriter, _ *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		var list []map[string]string
		for n, id := range g.labels {
			list = append(list, map[string]string{"id": id, "name": n})
		}
		writeJSON(w, map[string]any{"labels": list})
	})
	mux.HandleFunc("POST /gmail/v1/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		id := "Label_" + body.Name
		g.labels[body.Name] = id
		g.mu.Unlock()
		writeJSON(w, map[string]any{"id": id, "name": body.Name})
	})
	mux.HandleFunc("POST /gmail/v1/users/me/messages/{id}/modify", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AddLabelIDs    []string `json:"addLabelIds"`
			RemoveLabelIDs []string `json:"removeLabelIds"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		m := g.find(r.PathValue("id"))
		g.mu.Lock()
		keep := []string{}
		for _, l := range m.Labels {
			if !contains(body.RemoveLabelIDs, l) {
				keep = append(keep, l)
			}
		}
		keep = append(keep, body.AddLabelIDs...)
		m.Labels = keep
		g.mu.Unlock()
		writeJSON(w, map[string]any{"id": m.ID, "labelIds": m.Labels})
	})
	mux.HandleFunc("POST /gmail/v1/users/me/drafts", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Message struct{ Raw, ThreadID string } `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := base64.URLEncoding.DecodeString(body.Message.Raw)
		g.mu.Lock()
		g.Drafts = append(g.Drafts, string(raw))
		g.mu.Unlock()
		writeJSON(w, map[string]any{"id": "draft-1", "message": map[string]any{"id": "m-draft", "threadId": body.Message.ThreadID}})
	})
	mux.HandleFunc("POST /gmail/v1/users/me/messages/send", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Raw, ThreadID string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := base64.URLEncoding.DecodeString(body.Raw)
		g.mu.Lock()
		g.Sent = append(g.Sent, string(raw))
		g.mu.Unlock()
		writeJSON(w, map[string]any{"id": "m-sent", "threadId": body.ThreadID})
	})
	mux.HandleFunc("POST /v1/documents", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Title string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		g.nextID++
		id := fmt.Sprintf("doc-%d", g.nextID)
		g.Docs[id] = "# " + body.Title + "\n"
		g.mu.Unlock()
		writeJSON(w, map[string]any{"documentId": id})
	})
	mux.HandleFunc("POST /v1/documents/{op}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := strings.CutSuffix(r.PathValue("op"), ":batchUpdate")
		if !ok {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Requests []struct {
				InsertText struct{ Text string } `json:"insertText"`
			} `json:"requests"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		for _, req := range body.Requests {
			g.Docs[id] += req.InsertText.Text
		}
		g.mu.Unlock()
		writeJSON(w, map[string]any{})
	})
	g.Server = httptest.NewServer(mux)
	t.Cleanup(g.Server.Close)
	return g
}

// Add delivers a new message.
func (g *Google) Add(m *Message) {
	g.mu.Lock()
	g.msgs = append(g.msgs, m)
	g.mu.Unlock()
}

// Labels returns a message's label ids.
func (g *Google) Labels(id string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, m := range g.msgs {
		if m.ID == id {
			return append([]string(nil), m.Labels...)
		}
	}
	return nil
}

func (g *Google) find(id string) *Message {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, m := range g.msgs {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// Slack fakes chat.postMessage.
type Slack struct {
	Server   *httptest.Server
	mu       sync.Mutex
	Messages []map[string]string
}

// NewSlack starts the fake.
func NewSlack(t *testing.T) *Slack {
	s := &Slack{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Header.Get("Authorization") != "Bearer xoxb-test" {
			writeJSON(w, map[string]any{"ok": false, "error": "invalid_auth"})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/chat.postMessage") {
			s.mu.Lock()
			s.Messages = append(s.Messages, body)
			s.mu.Unlock()
		}
		writeJSON(w, map[string]any{"ok": true, "channel": body["channel"], "ts": "1.2", "team": "Acme", "user": "pendolo"})
	}))
	t.Cleanup(s.Server.Close)
	return s
}

// Sent returns the posted messages.
func (s *Slack) Sent() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]string(nil), s.Messages...)
}

// Brave fakes the Brave Search API.
func Brave(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		writeJSON(w, map[string]any{"web": map[string]any{"results": []map[string]string{
			{"title": "Trends: " + q, "url": "https://example.com/trends", "description": "<b>Popular</b> topics about " + q},
		}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// MCP starts a real MCP server (official SDK) with SEO-like tools that
// require a bearer key.
func MCP(t *testing.T, key string) *httptest.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-seo", Version: "1.0.0"}, nil)
	type kwIn struct {
		Keywords []string `json:"keywords" jsonschema:"keywords to look up"`
	}
	type seedIn struct {
		Seed string `json:"seed" jsonschema:"topic to research"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "research_keywords", Description: "Suggest keywords for a topic",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, in seedIn) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"keywords": []string{in.Seed + " guide", in.Seed + " 2027"}}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "get_keyword_metrics", Description: "Search volume and difficulty",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, in kwIn) (*mcp.CallToolResult, map[string]any, error) {
			metrics := []map[string]any{}
			for i, k := range in.Keywords {
				metrics = append(metrics, map[string]any{"keyword": k, "volume": 1000 * (i + 1), "difficulty": 20 + i})
			}
			return nil, map[string]any{"metrics": metrics}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "delete_project", Description: "Delete a project",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true)}},
		func(_ context.Context, _ *mcp.CallToolRequest, _ seedIn) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"deleted": true}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Script is a fake language model: it answers in order, by request kind.
type Script struct {
	mu sync.Mutex
	// Answers are for llm steps (text or JSON), in order; the last repeats.
	Answers []string
	// Turns are for agent steps, in order; the last repeats.
	Turns []llm.ChatMessage
	// Seen records the requests.
	SeenComplete []llm.ProviderRequest
	SeenChat     []llm.ChatRequest
}

// Complete implements llm.Provider.
func (s *Script) Complete(_ context.Context, req llm.ProviderRequest) (llm.ProviderResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := min(len(s.SeenComplete), len(s.Answers)-1)
	s.SeenComplete = append(s.SeenComplete, req)
	return llm.ProviderResponse{Text: s.Answers[i], InputTokens: 1000, OutputTokens: 200}, nil
}

// Chat implements llm.ChatProvider.
func (s *Script) Chat(_ context.Context, _ string, req llm.ChatRequest, _ int, _, _ string) (llm.ChatMessage, llm.ProviderResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := min(len(s.SeenChat), len(s.Turns)-1)
	s.SeenChat = append(s.SeenChat, req)
	return s.Turns[i], llm.ProviderResponse{InputTokens: 2000, OutputTokens: 300}, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func b64(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

func firstWords(s string) string {
	if len(s) > 60 {
		return s[:60]
	}
	return s
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func ptr[T any](v T) *T { return &v }
