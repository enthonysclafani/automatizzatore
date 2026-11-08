package connectors

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// DefaultGoogleScopes cover Gmail (read, label, draft, send) and documents
// Pendolo creates.
var DefaultGoogleScopes = []string{
	"https://www.googleapis.com/auth/gmail.modify",
	"https://www.googleapis.com/auth/documents",
	"https://www.googleapis.com/auth/drive.file",
}

// OAuthCallbackPath is where providers send the browser back.
const OAuthCallbackPath = "/api/v1/oauth/callback"

func (h *Hub) googleOAuth(c *Conn) *oauth2.Config {
	scopes := strList(c.Config, "scopes")
	if len(scopes) == 0 {
		scopes = DefaultGoogleScopes
	}
	return &oauth2.Config{
		ClientID:     str(c.Config, "clientId"),
		ClientSecret: c.Secrets["clientSecret"],
		Endpoint: oauth2.Endpoint{
			AuthURL:  h.endpoint("google_auth", "https://accounts.google.com/o/oauth2/v2/auth"),
			TokenURL: h.endpoint("google_token", "https://oauth2.googleapis.com/token"),
		},
		RedirectURL: strings.TrimRight(h.PublicURL, "/") + OAuthCallbackPath,
		Scopes:      scopes,
	}
}

// OAuthStart returns the provider URL where the browser signs in.
func (h *Hub) OAuthStart(ctx context.Context, c *store.Connection, state, verifier string) (string, error) {
	conn, err := h.open(c)
	if err != nil {
		return "", err
	}
	if c.Kind != KindGoogle {
		return "", fmt.Errorf("%s connections do not sign in with OAuth", c.Kind)
	}
	if str(conn.Config, "clientId") == "" || conn.Secrets["clientSecret"] == "" {
		return "", errors.New("set the OAuth client ID and secret first")
	}
	cfg := h.googleOAuth(conn)
	// offline + consent: Google returns a refresh token, so runs keep working.
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.S256ChallengeOption(verifier)), nil
}

// OAuthFinish exchanges the code and stores the token.
func (h *Hub) OAuthFinish(ctx context.Context, c *store.Connection, code, verifier string) error {
	conn, err := h.open(c)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, h.client())
	tok, err := h.googleOAuth(conn).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return fmt.Errorf("google refused the sign-in: %s", oneLine(err.Error()))
	}
	if tok.RefreshToken == "" {
		return errors.New("google returned no refresh token; remove Pendolo's access at myaccount.google.com/permissions and sign in again")
	}
	b, _ := json.Marshal(tok) //nolint:gosec // the token is sealed with the master key before storage
	conn.Secrets["token"] = string(b)
	account := ""
	if prof, err := h.gmailProfile(ctx, conn); err == nil {
		account = prof
	}
	h.saveSecrets(ctx, conn, store.ConnReady, "", account)
	return nil
}

// googleHTTP returns a client that sends the connection's token,
// refreshing and saving it when it expires.
func (h *Hub) googleHTTP(ctx context.Context, c *Conn) (*http.Client, error) {
	raw := c.Secrets["token"]
	if raw == "" {
		return nil, fmt.Errorf("connection %s is not signed in; use Sign in with Google on the Connections page", c.Name)
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, fmt.Errorf("connection %s: stored token unreadable; sign in again", c.Name)
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, h.client())
	fresh, err := h.googleOAuth(c).TokenSource(ctx, &tok).Token()
	if err != nil {
		h.saveSecrets(context.Background(), c, store.ConnError, "token refresh failed; sign in again", "")
		return nil, fmt.Errorf("connection %s: the Google sign-in expired or was revoked; sign in again", c.Name)
	}
	if fresh.AccessToken != tok.AccessToken {
		b, _ := json.Marshal(fresh) //nolint:gosec // sealed before storage
		c.Secrets["token"] = string(b)
		h.saveSecrets(context.Background(), c, store.ConnReady, "", "")
	}
	return oauth2.NewClient(ctx, oauth2.StaticTokenSource(fresh)), nil
}

// googleJSON calls a Google API and decodes the JSON answer into out.
func (h *Hub) googleJSON(ctx context.Context, c *Conn, method, u string, body, out any) error {
	client, err := h.googleHTTP(ctx, c)
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("google: request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = oneLine(string(raw))
		}
		return fmt.Errorf("google: HTTP %d: %s", resp.StatusCode, msg)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (h *Hub) gmailBase() string {
	return h.endpoint("gmail", "https://gmail.googleapis.com") + "/gmail/v1/users/me"
}

func (h *Hub) gmailProfile(ctx context.Context, c *Conn) (string, error) {
	var p struct {
		EmailAddress string `json:"emailAddress"`
	}
	err := h.googleJSON(ctx, c, http.MethodGet, h.gmailBase()+"/profile", nil, &p)
	return p.EmailAddress, err
}

func (h *Hub) googleTest(ctx context.Context, c *Conn) (string, error) {
	email, err := h.gmailProfile(ctx, c)
	if err != nil {
		return "", err
	}
	return "signed in as " + email, nil
}

var googleActionInfos = map[string]ActionInfo{
	"gmail.search": {Name: "gmail.search", Risk: RiskRead,
		Description: "Search messages with Gmail's search syntax; returns sender, subject, date and snippet.",
		Input: obj([]string{"query"}, map[string]any{
			"query": prop("string", "Gmail search, e.g. \"in:inbox is:unread newer_than:1d\""), "maxResults": prop("integer", "1 to 100, default 20"),
		})},
	"gmail.get_message": {Name: "gmail.get_message", Risk: RiskRead,
		Description: "Read one message, with its plain-text body.",
		Input:       obj([]string{"messageId"}, map[string]any{"messageId": prop("string", "Gmail message id")})},
	"gmail.apply_labels": {Name: "gmail.apply_labels", Risk: RiskWrite,
		Description: "Add labels to a message (created if missing), remove others, optionally mark it read.",
		Input: obj([]string{"messageId", "labels"}, map[string]any{
			"messageId": prop("string", "Gmail message id"), "labels": listOf("string", "Label names to add"),
			"removeLabels": listOf("string", "Label names to remove"), "markRead": prop("boolean", "Mark the message as read"),
		})},
	"gmail.create_draft": {Name: "gmail.create_draft", Risk: RiskWrite,
		Description: "Save a draft. With messageId it is a reply in that thread; otherwise give to and subject.",
		Input: obj([]string{"body"}, map[string]any{
			"messageId": prop("string", "Message to reply to"), "threadId": prop("string", "Thread id (optional with messageId)"),
			"to": prop("string", "Recipient address"), "subject": prop("string", "Subject"), "body": prop("string", "Plain-text body"),
		})},
	"gmail.send": {Name: "gmail.send", Risk: RiskSend, DraftAlternative: "gmail.create_draft",
		Description: "Send an email. With messageId it is a reply in that thread; otherwise give to and subject.",
		Input: obj([]string{"body"}, map[string]any{
			"messageId": prop("string", "Message to reply to"), "threadId": prop("string", "Thread id (optional with messageId)"),
			"to": prop("string", "Recipient address"), "subject": prop("string", "Subject"), "body": prop("string", "Plain-text body"),
		})},
	"google_docs.create": {Name: "google_docs.create", Risk: RiskWrite,
		Description: "Create a Google Doc with a title and text; returns its id and URL.",
		Input:       obj([]string{"title"}, map[string]any{"title": prop("string", "Document title"), "body": prop("string", "Text of the document")})},
	"google_docs.append": {Name: "google_docs.append", Risk: RiskWrite,
		Description: "Append text at the end of a Google Doc.",
		Input:       obj([]string{"documentId", "text"}, map[string]any{"documentId": prop("string", "Document id"), "text": prop("string", "Text to append")})},
}

func (h *Hub) googleActions(c *Conn) map[string]actionImpl {
	run := map[string]func(context.Context, map[string]any) (any, error){
		"gmail.search": func(ctx context.Context, in map[string]any) (any, error) { return h.gmailSearch(ctx, c, in) },
		"gmail.get_message": func(ctx context.Context, in map[string]any) (any, error) {
			return h.GmailMessage(ctx, c, str(in, "messageId"))
		},
		"gmail.apply_labels": func(ctx context.Context, in map[string]any) (any, error) { return h.gmailLabels(ctx, c, in) },
		"gmail.create_draft": func(ctx context.Context, in map[string]any) (any, error) { return h.gmailCompose(ctx, c, in, false) },
		"gmail.send":         func(ctx context.Context, in map[string]any) (any, error) { return h.gmailCompose(ctx, c, in, true) },
		"google_docs.create": func(ctx context.Context, in map[string]any) (any, error) { return h.docsCreate(ctx, c, in) },
		"google_docs.append": func(ctx context.Context, in map[string]any) (any, error) { return h.docsAppend(ctx, c, in) },
	}
	out := map[string]actionImpl{}
	for name, info := range googleActionInfos {
		out[name] = actionImpl{info: info, run: run[name]}
	}
	return out
}

// Email is a message as automations see it (trigger.email, gmail actions).
type Email struct {
	ID        string   `json:"id"`
	ThreadID  string   `json:"threadId"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	Cc        string   `json:"cc,omitempty"`
	Subject   string   `json:"subject"`
	Date      string   `json:"date"`
	Snippet   string   `json:"snippet"`
	Body      string   `json:"body,omitempty"`
	Labels    []string `json:"labels"`
	MessageID string   `json:"messageIdHeader,omitempty"`
}

type gmailPart struct {
	MimeType string `json:"mimeType"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body struct {
		Data string `json:"data"`
	} `json:"body"`
	Parts []gmailPart `json:"parts"`
}

type gmailMessage struct {
	ID       string    `json:"id"`
	ThreadID string    `json:"threadId"`
	LabelIDs []string  `json:"labelIds"`
	Snippet  string    `json:"snippet"`
	Payload  gmailPart `json:"payload"`
}

func (p gmailPart) header(name string) string {
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func decodeB64(s string) string {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		b, _ = base64.RawURLEncoding.DecodeString(s)
	}
	return string(b)
}

// text finds the plain-text body, falling back to HTML without tags.
func (p gmailPart) text() string {
	if strings.HasPrefix(p.MimeType, "text/plain") && p.Body.Data != "" {
		return decodeB64(p.Body.Data)
	}
	for _, sub := range p.Parts {
		if t := sub.text(); t != "" && !strings.HasPrefix(sub.MimeType, "text/html") {
			return t
		}
	}
	if strings.HasPrefix(p.MimeType, "text/html") && p.Body.Data != "" {
		return htmlToText(decodeB64(p.Body.Data))
	}
	for _, sub := range p.Parts {
		if t := sub.text(); t != "" {
			return t
		}
	}
	return ""
}

func toEmail(m gmailMessage, withBody bool) Email {
	e := Email{
		ID: m.ID, ThreadID: m.ThreadID, Snippet: m.Snippet, Labels: m.LabelIDs,
		From: m.Payload.header("From"), To: m.Payload.header("To"), Cc: m.Payload.header("Cc"),
		Subject: m.Payload.header("Subject"), Date: m.Payload.header("Date"), MessageID: m.Payload.header("Message-ID"),
	}
	if e.Labels == nil {
		e.Labels = []string{}
	}
	if withBody {
		e.Body = m.Payload.text()
		if len(e.Body) > 100_000 {
			e.Body = e.Body[:100_000] + "\n[truncated]"
		}
	}
	return e
}

// GmailMessage reads one message with its body.
func (h *Hub) GmailMessage(ctx context.Context, c *Conn, id string) (Email, error) {
	var m gmailMessage
	if err := h.googleJSON(ctx, c, http.MethodGet, h.gmailBase()+"/messages/"+url.PathEscape(id)+"?format=full", nil, &m); err != nil {
		return Email{}, err
	}
	return toEmail(m, true), nil
}

// GmailList returns the ids of messages matching a query, newest first.
func (h *Hub) GmailList(ctx context.Context, c *Conn, query string, max int) ([]string, error) {
	var list struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	q := url.Values{"q": {query}, "maxResults": {fmt.Sprint(max)}}
	if err := h.googleJSON(ctx, c, http.MethodGet, h.gmailBase()+"/messages?"+q.Encode(), nil, &list); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list.Messages))
	for _, m := range list.Messages {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

func (h *Hub) gmailSearch(ctx context.Context, c *Conn, in map[string]any) (any, error) {
	max := intOr(in, "maxResults", 20)
	if max < 1 || max > 100 {
		max = 20
	}
	ids, err := h.GmailList(ctx, c, str(in, "query"), max)
	if err != nil {
		return nil, err
	}
	out := make([]Email, 0, len(ids))
	for _, id := range ids {
		var m gmailMessage
		u := h.gmailBase() + "/messages/" + url.PathEscape(id) + "?format=metadata&metadataHeaders=From&metadataHeaders=To&metadataHeaders=Subject&metadataHeaders=Date"
		if err := h.googleJSON(ctx, c, http.MethodGet, u, nil, &m); err != nil {
			return nil, err
		}
		out = append(out, toEmail(m, false))
	}
	return out, nil
}

func (h *Hub) gmailLabelIDs(ctx context.Context, c *Conn, names []string, create bool) ([]string, error) {
	var list struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := h.googleJSON(ctx, c, http.MethodGet, h.gmailBase()+"/labels", nil, &list); err != nil {
		return nil, err
	}
	byName := map[string]string{}
	for _, l := range list.Labels {
		byName[strings.ToLower(l.Name)] = l.ID
	}
	ids := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if id, ok := byName[strings.ToLower(n)]; ok {
			ids = append(ids, id)
			continue
		}
		if !create {
			continue
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := h.googleJSON(ctx, c, http.MethodPost, h.gmailBase()+"/labels",
			map[string]any{"name": n, "labelListVisibility": "labelShow", "messageListVisibility": "show"}, &created); err != nil {
			return nil, err
		}
		byName[strings.ToLower(n)] = created.ID
		ids = append(ids, created.ID)
	}
	return ids, nil
}

func (h *Hub) gmailLabels(ctx context.Context, c *Conn, in map[string]any) (any, error) {
	add, err := h.gmailLabelIDs(ctx, c, strList(in, "labels"), true)
	if err != nil {
		return nil, err
	}
	remove, err := h.gmailLabelIDs(ctx, c, strList(in, "removeLabels"), false)
	if err != nil {
		return nil, err
	}
	if b, _ := in["markRead"].(bool); b {
		remove = append(remove, "UNREAD")
	}
	var m gmailMessage
	if err := h.googleJSON(ctx, c, http.MethodPost, h.gmailBase()+"/messages/"+url.PathEscape(str(in, "messageId"))+"/modify",
		map[string]any{"addLabelIds": add, "removeLabelIds": remove}, &m); err != nil {
		return nil, err
	}
	return map[string]any{"messageId": m.ID, "labels": m.LabelIDs}, nil
}

var headerUnsafe = strings.NewReplacer("\r", " ", "\n", " ")

func (h *Hub) gmailCompose(ctx context.Context, c *Conn, in map[string]any, send bool) (any, error) {
	to, subject, threadID := str(in, "to"), str(in, "subject"), str(in, "threadId")
	var inReplyTo, references string
	if id := str(in, "messageId"); id != "" {
		orig, err := h.GmailMessage(ctx, c, id)
		if err != nil {
			return nil, err
		}
		if to == "" {
			to = orig.From
		}
		if subject == "" {
			subject = orig.Subject
			if !strings.HasPrefix(strings.ToLower(subject), "re:") {
				subject = "Re: " + subject
			}
		}
		if threadID == "" {
			threadID = orig.ThreadID
		}
		inReplyTo, references = orig.MessageID, orig.MessageID
	}
	if to == "" || subject == "" {
		return nil, errors.New("give messageId to reply, or to and subject for a new email")
	}
	var msg strings.Builder
	msg.WriteString("To: " + headerUnsafe.Replace(to) + "\r\n")
	msg.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", headerUnsafe.Replace(subject)) + "\r\n")
	if inReplyTo != "" {
		msg.WriteString("In-Reply-To: " + headerUnsafe.Replace(inReplyTo) + "\r\nReferences: " + headerUnsafe.Replace(references) + "\r\n")
	}
	msg.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	msg.WriteString(strings.ReplaceAll(str(in, "body"), "\n", "\r\n"))
	message := map[string]any{"raw": base64.URLEncoding.EncodeToString([]byte(msg.String()))}
	if threadID != "" {
		message["threadId"] = threadID
	}
	var out struct {
		ID      string `json:"id"`
		Message struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"message"`
		ThreadID string `json:"threadId"`
	}
	if send {
		if err := h.googleJSON(ctx, c, http.MethodPost, h.gmailBase()+"/messages/send", message, &out); err != nil {
			return nil, err
		}
		return map[string]any{"sent": true, "messageId": out.ID, "threadId": out.ThreadID, "to": to, "subject": subject}, nil
	}
	if err := h.googleJSON(ctx, c, http.MethodPost, h.gmailBase()+"/drafts", map[string]any{"message": message}, &out); err != nil {
		return nil, err
	}
	return map[string]any{"draftId": out.ID, "messageId": out.Message.ID, "threadId": out.Message.ThreadID, "to": to, "subject": subject}, nil
}

func (h *Hub) docsCreate(ctx context.Context, c *Conn, in map[string]any) (any, error) {
	base := h.endpoint("docs", "https://docs.googleapis.com") + "/v1/documents"
	var doc struct {
		DocumentID string `json:"documentId"`
	}
	if err := h.googleJSON(ctx, c, http.MethodPost, base, map[string]any{"title": str(in, "title")}, &doc); err != nil {
		return nil, err
	}
	if body := str(in, "body"); body != "" {
		if err := h.docsInsert(ctx, c, doc.DocumentID, body, 1); err != nil {
			return nil, err
		}
	}
	return map[string]any{"documentId": doc.DocumentID, "url": "https://docs.google.com/document/d/" + doc.DocumentID + "/edit"}, nil
}

func (h *Hub) docsInsert(ctx context.Context, c *Conn, id, text string, index int) error {
	u := h.endpoint("docs", "https://docs.googleapis.com") + "/v1/documents/" + url.PathEscape(id) + ":batchUpdate"
	req := map[string]any{"requests": []any{map[string]any{"insertText": map[string]any{"location": map[string]any{"index": index}, "text": text}}}}
	return h.googleJSON(ctx, c, http.MethodPost, u, req, nil)
}

func (h *Hub) docsAppend(ctx context.Context, c *Conn, in map[string]any) (any, error) {
	id := str(in, "documentId")
	var doc struct {
		Body struct {
			Content []struct {
				EndIndex int `json:"endIndex"`
			} `json:"content"`
		} `json:"body"`
	}
	if err := h.googleJSON(ctx, c, http.MethodGet, h.endpoint("docs", "https://docs.googleapis.com")+"/v1/documents/"+url.PathEscape(id), nil, &doc); err != nil {
		return nil, err
	}
	end := 1
	if n := len(doc.Body.Content); n > 0 {
		end = max(doc.Body.Content[n-1].EndIndex-1, 1)
	}
	if err := h.docsInsert(ctx, c, id, str(in, "text"), end); err != nil {
		return nil, err
	}
	return map[string]any{"documentId": id, "url": "https://docs.google.com/document/d/" + id + "/edit"}, nil
}

var (
	tagRe   = regexp.MustCompile(`(?is)<(script|style|title|noscript)[^>]*>.*?</(script|style|title|noscript)>|<[^>]+>`)
	spaceRe = regexp.MustCompile(`[ \t]+`)
	linesRe = regexp.MustCompile(`\n{3,}`)
	blockRe = regexp.MustCompile(`(?i)<(br|/p|/div|/li|/h[1-6]|/tr)[^>]*>`)
)

// htmlToText keeps the readable text of a page.
func htmlToText(s string) string {
	s = blockRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, " ")
	s = htmlUnescape(s)
	s = spaceRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "\n ", "\n")
	return strings.TrimSpace(linesRe.ReplaceAllString(s, "\n\n"))
}

// EmailTriggerData turns a message into what automations read as trigger.email.
func EmailTriggerData(e Email) map[string]any {
	b, _ := json.Marshal(e)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return map[string]any{"email": m, "receivedAt": time.Now().UTC().Format(time.RFC3339)}
}
