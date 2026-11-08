package connectors

import "strings"

// Field describes a setting of a connection kind, for the dashboard form.
type Field struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // string, string[], map
	Secret   bool     `json:"secret"`
	Required bool     `json:"required"`
	Help     string   `json:"help,omitempty"`
	Options  []string `json:"options,omitempty"`
	Default  any      `json:"default,omitempty"`
}

// KindInfo describes a connection kind for the dashboard.
type KindInfo struct {
	Kind        string       `json:"kind"`
	Label       string       `json:"label"`
	Description string       `json:"description"`
	Fields      []Field      `json:"fields"`
	OAuth       bool         `json:"oauth"`
	SetupSteps  []string     `json:"setupSteps"`
	Actions     []ActionInfo `json:"actions"`
}

func infosWithPrefix(m map[string]ActionInfo, prefixes ...string) []ActionInfo {
	var out []ActionInfo
	for _, k := range sortedKeys(m) {
		for _, p := range prefixes {
			if strings.HasPrefix(k, p+".") {
				out = append(out, m[k])
			}
		}
	}
	return out
}

// Catalog lists the connection kinds Pendolo supports.
func Catalog(redirectURL string) []KindInfo {
	return []KindInfo{
		{
			Kind: KindGoogle, Label: "Google (Gmail and Docs)", OAuth: true,
			Description: "Read, label and draft email in Gmail, send email, and create Google Docs. You use your own Google OAuth client, so nobody else holds access to your account.",
			Fields: []Field{
				{Name: "clientId", Label: "OAuth client ID", Type: "string", Required: true, Help: "Ends with .apps.googleusercontent.com"},
				{Name: "clientSecret", Label: "OAuth client secret", Type: "string", Secret: true, Required: true},
				{Name: "scopes", Label: "Scopes", Type: "string[]", Default: DefaultGoogleScopes, Help: "Leave as is unless you know you need fewer"},
			},
			SetupSteps: []string{
				"Open https://console.cloud.google.com/projectcreate and create a project (for example \"Pendolo\").",
				"Enable the Gmail API (https://console.cloud.google.com/apis/library/gmail.googleapis.com) and the Google Docs API (https://console.cloud.google.com/apis/library/docs.googleapis.com).",
				"Configure the OAuth consent screen (https://console.cloud.google.com/apis/credentials/consent): user type External, then add your own address under Test users.",
				"Create credentials (https://console.cloud.google.com/apis/credentials): OAuth client ID, application type Web application.",
				"Under Authorized redirect URIs add: " + redirectURL,
				"Copy the client ID and client secret here, save, then choose Sign in with Google.",
				"While the app is in Testing, Google asks you to sign in again every 7 days; publish the consent screen to avoid that.",
			},
			Actions: infosWithPrefix(googleActionInfos, "gmail", "google_docs"),
		},
		{
			Kind: KindSlack, Label: "Slack",
			Description: "Post messages to Slack channels with a bot.",
			Fields: []Field{
				{Name: "token", Label: "Bot token", Type: "string", Secret: true, Required: true, Help: "Starts with xoxb-"},
			},
			SetupSteps: []string{
				"Open https://api.slack.com/apps and choose Create New App, From scratch.",
				"Under OAuth & Permissions, add the bot token scope chat:write.",
				"Install the app to your workspace and copy the Bot User OAuth Token (xoxb-...).",
				"In Slack, invite the bot to each channel it should post to: /invite @your-app.",
			},
			Actions: infosWithPrefix(slackActionInfos, "slack"),
		},
		{
			Kind: KindWebSearch, Label: "Web search",
			Description: "Search the web from agent steps (web.search). web.fetch works without a connection.",
			Fields: []Field{
				{Name: "provider", Label: "Provider", Type: "string", Required: true, Options: []string{"brave", "tavily", "searxng"}, Default: "brave"},
				{Name: "apiKey", Label: "API key", Type: "string", Secret: true, Help: "Brave Search or Tavily key; not needed for SearXNG"},
				{Name: "baseURL", Label: "SearXNG URL", Type: "string", Help: "Only for SearXNG, e.g. http://searxng:8080 (JSON output enabled)"},
			},
			SetupSteps: []string{
				"Brave Search: get a key at https://brave.com/search/api/ (free tier available).",
				"Tavily: get a key at https://app.tavily.com/.",
				"SearXNG: run your own instance and enable the json format in its settings.",
			},
			Actions: infosWithPrefix(webActionInfos, "web"),
		},
		{
			Kind: KindMCP, Label: "MCP server",
			Description: "Any server that speaks the Model Context Protocol over HTTP (for example OpenSEO). Its tools become actions named <connection>.<tool>.",
			Fields: []Field{
				{Name: "url", Label: "Server URL", Type: "string", Required: true, Help: "The Streamable HTTP endpoint, e.g. https://example.com/mcp"},
				{Name: "headers", Label: "Headers", Type: "map", Secret: true, Help: "Sent with every request, e.g. Authorization: Bearer <key>"},
				{Name: "risks", Label: "Risk overrides", Type: "map", Help: "tool name → read, write, send or destructive, when the server's own hints are wrong"},
			},
			SetupSteps: []string{
				"Find the server's HTTP endpoint and API key in its documentation.",
				"Give the connection a short name: actions will be <name>.<tool>.",
				"Save and choose Test: the tools the server offers appear on the card.",
				"Tools are treated as writes unless the server marks them read-only; adjust with risk overrides if needed.",
			},
			Actions: []ActionInfo{},
		},
	}
}
