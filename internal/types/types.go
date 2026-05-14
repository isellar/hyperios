package types

// GoalGraph is the structured output of the Intent Agent.
type GoalGraph struct {
	Intent  string `json:"intent"`
	Context string `json:"context"`
	Goals   []Goal `json:"goals"`
}

// Goal is a single node in the goal graph.
type Goal struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on"`
}

// Capability describes what OS-level permission a step requires.
type Capability struct {
	Type  string `json:"type"`  // e.g. "read:file", "execute:git", "execute:shell", "execute:package"
	Scope string `json:"scope"` // e.g. "/repo/**", "git:status", "apt:install"
}

// ActionStep is a single proposed action in a plan.
type ActionStep struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Capability  Capability `json:"capability"`
	// Command is the literal command to execute, e.g. ["grep", "-r", "foo", "/etc"].
	// The executor runs Command[0] with Command[1:] as arguments — no shell interpolation.
	// Capability.Scope contains the binary name (for allowlist matching).
	// For execute:config: Command[0]=path, Command[1]=content.
	// For network:outbound: Command[0]=method, Command[1]=url, Command[2]=body (optional).
	Command     []string   `json:"command,omitempty"`
	Reversible  bool       `json:"reversible"`
	DependsOn   []string   `json:"depends_on"`
	// Failure policy — specified by the Planner, enforced by the Executor.
	OnFailure           string `json:"on_failure,omitempty"`           // "halt" | "retry" | "replan" | "skip"
	MaxRetries          int    `json:"max_retries,omitempty"`           // used when OnFailure == "retry"
	RetryBackoffSeconds int    `json:"retry_backoff_seconds,omitempty"` // seconds between retries

	// ReadyCondition — optional. If set, the executor polls this condition after
	// running the command and before marking the step complete.
	ReadyCondition *ReadyCondition `json:"ready_condition,omitempty"`
}

// ReadyCondition describes a condition the executor must poll after a step executes.
type ReadyCondition struct {
	// Type identifies the check to perform.
	// "exit:0"         — re-run Command, check zero exit code
	// "process:active" — check systemctl is-active <Target>
	// "file:exists"    — check os.Stat(<Target>) succeeds
	// "output:contains"— re-run Command, check stdout contains Target
	// "http:ok"        — HTTP GET <Target>, check 2xx response
	// "atspi:present"  — AT-SPI element present (Phase 4, stubbed)
	// "vision:confirms"— LLM vision confirms screen state (Phase 4, stubbed)
	Type                string `json:"type"`
	Target              string `json:"target"`               // service name, file path, URL, substring, etc.
	TimeoutSeconds      int    `json:"timeout_seconds"`      // fail step if condition not met within this window
	PollIntervalSeconds int    `json:"poll_interval_seconds"` // how often to re-check (default 2s)
}

// RiskFlag is a single risk identified by the Adversarial Agent.
type RiskFlag struct {
	StepID         string `json:"step_id"`
	Severity       string `json:"severity"` // "low", "medium", "high", "block"
	Description    string `json:"description"`
	Counterfactual string `json:"counterfactual"`
}

// RiskReport is the structured output of the Adversarial Agent.
type RiskReport struct {
	Flags   []RiskFlag `json:"flags"`
	Summary string     `json:"summary"`
}

// ArbiterVerdict is the Policy Arbiter's decision for a single step.
type ArbiterVerdict struct {
	StepID   string `json:"step_id"`
	Verdict  string `json:"verdict"`  // "approved", "modified", "blocked"
	Reason   string `json:"reason"`
	Autonomy int    `json:"autonomy"` // autonomy level at which verdict was evaluated
}

// WorkspaceContext holds gathered context about the current repo/environment.
type WorkspaceContext struct {
	Cwd       string `json:"cwd"`
	GitBranch string `json:"git_branch"`
	GitLog    string `json:"git_log"`
	GitStatus string `json:"git_status"`
}

// ExecutionResult is the outcome of executing an ActionStep.
type ExecutionResult struct {
	StepID   string `json:"step_id"`
	Success  bool   `json:"success"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
	Duration int64  `json:"duration_ms"`
}

// ExecutorType specifies which execution backend to use.
type ExecutorType string

const (
	ExecutorLocal     ExecutorType = "local"
	ExecutorContainer ExecutorType = "container"
	ExecutorRemote    ExecutorType = "remote"
)

// ActionPlan is the structured output of the Planner Agent.
type ActionPlan struct {
	Executor ExecutorType `json:"executor,omitempty"`
	Steps    []ActionStep `json:"steps"`
}
