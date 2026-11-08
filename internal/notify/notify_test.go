package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

func TestShouldNotify(t *testing.T) {
	ok := &store.Run{Status: store.RunSucceeded}
	degraded := &store.Run{Status: store.RunSucceeded, Anomalies: 1}
	failed := &store.Run{Status: store.RunFailed}
	cases := []struct {
		on   string
		run  *store.Run
		want bool
	}{
		{"", ok, false}, {"", failed, true}, {"failure", degraded, false},
		{"anomaly", degraded, true}, {"anomaly", ok, false},
		{"always", ok, true}, {"never", failed, false},
	}
	for _, tc := range cases {
		if got := ShouldNotify(tc.on, tc.run); got != tc.want {
			t.Errorf("ShouldNotify(%q, %+v) = %v", tc.on, tc.run, got)
		}
	}
}

func TestWebhookWithSecretURL(t *testing.T) {
	var got Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
	}))
	defer srv.Close()

	n := &Notifier{Secrets: secrets.Static{"alerts": srv.URL}}
	a := engine.Automation{Name: "site-checks", Manifest: &manifest.Manifest{
		Notifications: &manifest.Notifications{On: "failure", Channels: []string{"webhook:secret://alerts"}},
	}}
	n.Notify(context.Background(), a, &store.Run{ID: "r1", Status: store.RunFailed, Error: "step homepage: HTTP 503"})
	if got.RunID != "r1" || got.Text != "site-checks: run failed - step homepage: HTTP 503" || got.Content != got.Text {
		t.Fatalf("payload = %+v", got)
	}
}

func TestSlackAndTelegram(t *testing.T) {
	var slackAuth, slackChannel, telegramPath, telegramChat string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat.postMessage"):
			slackAuth, slackChannel = r.Header.Get("Authorization"), body["channel"].(string)
			_, _ = io.WriteString(w, `{"ok":true}`)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			telegramPath, telegramChat = r.URL.Path, body["chat_id"].(string)
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	}))
	defer api.Close()

	n := &Notifier{
		Secrets:  secrets.Static{"slack": "xoxb-1", "tg": "123:abc"},
		Config:   config.Notify{Slack: config.SlackNotify{Token: "secret://slack"}, Telegram: config.TelegramNotify{BotToken: "secret://tg"}},
		SlackAPI: api.URL, TelegramAPI: api.URL,
	}
	p := Payload{Text: "x: run failed", Automation: "x", RunID: "r", Status: "failed"}
	if err := n.Send(context.Background(), "slack:#ops", p); err != nil {
		t.Fatal(err)
	}
	if err := n.Send(context.Background(), "telegram:-100200", p); err != nil {
		t.Fatal(err)
	}
	if slackAuth != "Bearer xoxb-1" || slackChannel != "#ops" || telegramPath != "/bot123:abc/sendMessage" || telegramChat != "-100200" {
		t.Fatalf("slack %q %q telegram %q %q", slackAuth, slackChannel, telegramPath, telegramChat)
	}

	bare := &Notifier{}
	for _, ch := range []string{"slack:#ops", "telegram:1", "email:a@b.c", "fax:1"} {
		if err := bare.Send(context.Background(), ch, p); err == nil {
			t.Errorf("%s without configuration: no error", ch)
		}
	}
}

func TestEmail(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go fakeSMTP(ln, got)

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	n := &Notifier{Config: config.Notify{Email: config.EmailNotify{Host: host, Port: p, From: "pendolo@example.com", Security: "none"}}}
	if err := n.Send(context.Background(), "email:me@example.com", Payload{Text: "x: run failed", Automation: "x", RunID: "r9", Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	msg := <-got
	for _, want := range []string{"MAIL FROM:<pendolo@example.com>", "RCPT TO:<me@example.com>", "Subject: [Pendolo] x: run failed", "pendolo show r9"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q:\n%s", want, msg)
		}
	}
	if err := n.Send(context.Background(), "email:bad\r\nBcc: x@y", Payload{}); err == nil {
		t.Fatal("header injection accepted")
	}
}

// fakeSMTP accepts one message and sends the whole conversation to got.
func fakeSMTP(ln net.Listener, got chan<- string) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	var log strings.Builder
	r := bufio.NewReader(conn)
	reply := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
	reply("220 fake")
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			got <- log.String()
			return
		}
		log.WriteString(line)
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case inData:
			if cmd == "." {
				inData = false
				reply("250 queued")
			}
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			reply("250 fake")
		case strings.HasPrefix(cmd, "DATA"):
			inData = true
			reply("354 go")
		case strings.HasPrefix(cmd, "QUIT"):
			reply("221 bye")
			got <- log.String()
			return
		default:
			reply("250 ok")
		}
	}
}
