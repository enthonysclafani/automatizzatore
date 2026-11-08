// Package manifest defines the automation manifest (apiVersion pendolo/v1)
// and validates it.
//
// A manifest describes when an automation runs (triggers), what it does
// (steps) and how much autonomy it has. The JSON Schema in /spec is the
// source of truth for its shape; this package adds the checks a schema
// cannot express, such as step references and cron syntax.
package manifest

// APIVersionV1 is the only manifest version supported today.
const APIVersionV1 = "pendolo/v1"

// FileName is the conventional name of a manifest inside an automation folder.
const FileName = "automation.yaml"

// Autonomy levels, from the most to the least restrictive.
const (
	AutonomyObserve = "observe"
	AutonomyDraft   = "draft"
	AutonomyAsk     = "ask"
	AutonomyAuto    = "auto"
)

// Step types.
const (
	StepConnector = "connector"
	StepHTTP      = "http"
	StepShell     = "shell"
	StepLLM       = "llm"
	StepAgent     = "agent"
	StepApproval  = "approval"
	StepForeach   = "foreach"
)

// Manifest is a parsed automation.
type Manifest struct {
	APIVersion    string         `yaml:"apiVersion"`
	Kind          string         `yaml:"kind"`
	Metadata      Metadata       `yaml:"metadata"`
	Triggers      []Trigger      `yaml:"triggers"`
	Autonomy      string         `yaml:"autonomy,omitempty"`
	Budget        *Budget        `yaml:"budget,omitempty"`
	Notifications *Notifications `yaml:"notifications,omitempty"`
	Steps         []Step         `yaml:"steps"`
}

// EffectiveAutonomy returns the autonomy level, defaulting to "ask".
func (m *Manifest) EffectiveAutonomy() string {
	if m.Autonomy == "" {
		return AutonomyAsk
	}
	return m.Autonomy
}

// Metadata identifies an automation.
type Metadata struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Workspace   string            `yaml:"workspace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
}

// Budget caps what one run may spend.
type Budget struct {
	MaxCostEur float64 `yaml:"maxCostEur,omitempty"`
	Timeout    string  `yaml:"timeout,omitempty"`
}

// Notifications says who is told about a run, and when.
type Notifications struct {
	On       string   `yaml:"on,omitempty"`
	Channels []string `yaml:"channels"`
}

// Trigger starts a run. Exactly one field is set.
type Trigger struct {
	Schedule *ScheduleTrigger `yaml:"schedule,omitempty"`
	Webhook  *WebhookTrigger  `yaml:"webhook,omitempty"`
	Email    *EmailTrigger    `yaml:"email,omitempty"`
	Manual   *ManualTrigger   `yaml:"manual,omitempty"`
}

// ScheduleTrigger runs on a cron schedule.
type ScheduleTrigger struct {
	Cron     string `yaml:"cron"`
	Timezone string `yaml:"timezone,omitempty"`
	Misfire  string `yaml:"misfire,omitempty"`
}

// WebhookTrigger runs when a signed HTTP request arrives.
type WebhookTrigger struct {
	Secret string `yaml:"secret,omitempty"`
}

// EmailTrigger runs when new email matching Query arrives.
type EmailTrigger struct {
	Connection string `yaml:"connection"`
	Query      string `yaml:"query,omitempty"`
	Interval   string `yaml:"interval,omitempty"`
}

// ManualTrigger runs only when started by a person or the CLI.
type ManualTrigger struct{}

// Step is one unit of work. Which fields apply depends on Type.
type Step struct {
	// Common to every step.
	ID              string `yaml:"id"`
	Type            string `yaml:"type"`
	Name            string `yaml:"name,omitempty"`
	If              string `yaml:"if,omitempty"`
	Timeout         string `yaml:"timeout,omitempty"`
	Retry           *Retry `yaml:"retry,omitempty"`
	ContinueOnError bool   `yaml:"continueOnError,omitempty"`

	// connector
	Action     string         `yaml:"action,omitempty"`
	Connection string         `yaml:"connection,omitempty"`
	With       map[string]any `yaml:"with,omitempty"`

	// http
	Request *HTTPRequest `yaml:"request,omitempty"`
	Expect  *HTTPExpect  `yaml:"expect,omitempty"`

	// shell
	Run    string            `yaml:"run,omitempty"`
	Script string            `yaml:"script,omitempty"`
	Args   []string          `yaml:"args,omitempty"`
	Env    map[string]string `yaml:"env,omitempty"`

	// llm and agent
	Model         string         `yaml:"model,omitempty"`
	Prompt        string         `yaml:"prompt,omitempty"`
	PromptText    string         `yaml:"promptText,omitempty"`
	System        string         `yaml:"system,omitempty"`
	Input         any            `yaml:"input,omitempty"`
	OutputSchema  map[string]any `yaml:"outputSchema,omitempty"`
	Temperature   *float64       `yaml:"temperature,omitempty"`
	Tools         []string       `yaml:"tools,omitempty"`
	MaxIterations int            `yaml:"maxIterations,omitempty"`
	MaxCostEur    float64        `yaml:"maxCostEur,omitempty"`

	// approval
	Message   string   `yaml:"message,omitempty"`
	Approvers []string `yaml:"approvers,omitempty"`
	OnTimeout string   `yaml:"onTimeout,omitempty"`

	// foreach
	Items       string `yaml:"items,omitempty"`
	Concurrency int    `yaml:"concurrency,omitempty"`
	Steps       []Step `yaml:"steps,omitempty"`
}

// Retry controls how a failed step is retried.
type Retry struct {
	Max     int    `yaml:"max"`
	Backoff string `yaml:"backoff,omitempty"`
}

// HTTPRequest is the request an http step sends.
type HTTPRequest struct {
	Method  string            `yaml:"method,omitempty"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Body    any               `yaml:"body,omitempty"`
}

// HTTPExpect is what an http step checks in the response.
type HTTPExpect struct {
	Status       []int  `yaml:"status,omitempty"`
	MaxLatency   string `yaml:"maxLatency,omitempty"`
	BodyContains string `yaml:"bodyContains,omitempty"`
}
