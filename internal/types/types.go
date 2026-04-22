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
	Reversible  bool       `json:"reversible"`
	DependsOn   []string   `json:"depends_on"`
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
	StepID  string `json:"step_id"`
	Verdict string `json:"verdict"` // "approved", "modified", "blocked"
	Reason  string `json:"reason"`
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
