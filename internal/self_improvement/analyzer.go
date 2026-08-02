package self_improvement

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/isellar/hyperios/internal/llm"
)

// GoalResult holds the outcome of a single goal execution, as observed by the
// self-improvement module.
type GoalResult struct {
	GoalID      string `json:"goal_id"`
	Description string `json:"description"`
	Success     bool   `json:"success"`
	Output      string `json:"output,omitempty"`
	ErrorMsg    string `json:"error_msg,omitempty"`
}

// Analysis is the structured output produced by the Analyzer.
type Analysis struct {
	Patterns    []string `json:"patterns"`
	Suggestions []string `json:"suggestions"`
	// ImprovementGoals are one-off actionable tasks to fix a specific,
	// concrete problem right now (e.g. "install curl, which every recent
	// goal has needed and failed without"). Each is submitted as a new goal
	// and runs once by the agent using its normal shell/tool loop.
	ImprovementGoals []string `json:"improvement_goals"`
	// Directives are standing behavioral rules that should apply to every
	// future goal, not just a one-off fix (e.g. "always check available
	// disk space before writing files larger than 100MB"). Each becomes a
	// types.Directive persisted in memory and injected into every future
	// agent prompt.
	Directives []DirectiveSuggestion `json:"directives"`
	// CodeChanges are goals that require modifying HyperiOS's own source
	// code to fix a systemic problem. Each is submitted as a goal and the
	// agent will edit the source, verify, and apply the change using the
	// self_modify tool — no human involvement needed. Only use this when
	// there is a clear, recurring failure that could be fixed in the code
	// itself (e.g. a tool that consistently produces unusable output, a
	// missing capability, a prompt that causes repeated misunderstandings).
	// Be specific: describe what file/behaviour needs to change and why.
	CodeChanges []string `json:"code_changes"`
}

// DirectiveSuggestion is a proposed standing directive, as returned by the
// analyzer LLM.
type DirectiveSuggestion struct {
	Description string `json:"description"`
	// Priority is a rough importance ranking (higher = more important),
	// mirroring types.Directive.Priority.
	Priority int `json:"priority"`
}

// Analyzer calls the LLM to identify patterns across a set of goal results.
type Analyzer struct {
	llmClient llm.Completer
	stats     *Stats
}

// NewAnalyzer returns a new Analyzer.
func NewAnalyzer(llmClient llm.Completer, stats *Stats) *Analyzer {
	return &Analyzer{
		llmClient: llmClient,
		stats:     stats,
	}
}

const analyzerSystem = `You are the self-improvement engine for HyperiOS, an autonomous AI agent system.
Your job is to review recent goal execution results and identify what should change so the system
does not hit the same problem twice.

You have three levers — use whichever apply, independently of each other:

1. improvement_goals — a ONE-OFF task the agent will run immediately to fix a concrete problem.
   Example: "install the 'jq' package, which every recent goal needed but failed without".
   Use this when there is a specific missing dependency, misconfiguration, or setup step.

2. directives — a STANDING behavioral rule injected into every future agent prompt.
   Example: "always verify available disk space before writing files larger than 100MB".
   Use this when the same mistake keeps happening and a standing reminder would prevent it.

3. code_changes — a goal to modify HyperiOS's own source code to fix a systemic problem.
   The agent will edit the source files, run 'self_modify verify', and apply the change automatically.
   Use this when a tool, prompt, or module behaviour is consistently causing failures that cannot be
   fixed by environment changes alone. Be specific: name the file/component and what needs to change.
   Example: "The shell tool truncates output at 4096 bytes causing agents to miss error messages.
   Increase the output buffer in internal/io_toolbox/tools/shell_tool.go to 64KB."

Rules:
- If results show no clear pattern, return empty arrays. Do not invent improvements.
- Prefer code_changes over repeated directives for the same problem — a code fix is permanent,
  a directive is just a reminder that can be forgotten or ignored.
- A single analysis can produce any combination: none, one, two, or all three types.
- Keep each string specific and actionable (one sentence for goals/directives, one paragraph max for code_changes).

Respond ONLY with valid JSON — no markdown, no extra text:
{
  "patterns": ["<pattern observed>"],
  "suggestions": ["<concrete suggestion>"],
  "improvement_goals": ["<one-off task description>"],
  "directives": [{"description": "<standing rule>", "priority": <1-10>}],
  "code_changes": ["<specific code change goal description>"]
}`

// AnalyzeResults sends the goal results to the LLM and returns structured analysis.
// It returns an error if the LLM call fails or the response cannot be parsed.
func (a *Analyzer) AnalyzeResults(results []GoalResult) (*Analysis, error) {
	if len(results) == 0 {
		return &Analysis{}, nil
	}
	if a.llmClient == nil {
		return nil, fmt.Errorf("analyzer: LLM client is not configured")
	}

	userPrompt, err := buildAnalysisPrompt(results, a.stats)
	if err != nil {
		return nil, fmt.Errorf("analyzer: build prompt: %w", err)
	}

	ctx := context.Background()
	raw, err := a.llmClient.CompleteWithRetry(ctx, analyzerSystem, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("analyzer: llm call: %w", err)
	}

	analysis, err := parseAnalysis(raw)
	if err != nil {
		return nil, fmt.Errorf("analyzer: parse response: %w", err)
	}

	return analysis, nil
}

// buildAnalysisPrompt formats results and stats summary into the LLM user prompt.
func buildAnalysisPrompt(results []GoalResult, stats *Stats) (string, error) {
	resultsJSON, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("Given these goal execution results, what patterns do you see and what improvement goals would help the system do better next time?\n\n")
	sb.WriteString("## Goal Execution Results\n\n")
	sb.WriteString(string(resultsJSON))
	sb.WriteString("\n\n")

	if stats != nil {
		summary := stats.Summary()
		summaryJSON, err := json.MarshalIndent(summary, "", "  ")
		if err == nil {
			sb.WriteString("## Aggregate Stats\n\n")
			sb.WriteString(string(summaryJSON))
			sb.WriteString("\n\n")
		}

		patterns := stats.FailurePatterns()
		if len(patterns) > 0 {
			sb.WriteString("## Recurring Failure Patterns (goals that failed more than once)\n\n")
			for _, p := range patterns {
				sb.WriteString("- ")
				sb.WriteString(p)
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString("Respond with JSON only.")
	return sb.String(), nil
}

// parseAnalysis extracts JSON from the LLM response.
// It handles responses that may have leading/trailing whitespace or non-JSON preamble.
func parseAnalysis(raw string) (*Analysis, error) {
	raw = strings.TrimSpace(raw)

	// Find the first '{' and last '}' to tolerate minor preamble/postamble.
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object found in LLM response")
	}
	raw = raw[start : end+1]

	var analysis Analysis
	if err := json.Unmarshal([]byte(raw), &analysis); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	// Normalise nil slices to empty slices.
	if analysis.Patterns == nil {
		analysis.Patterns = []string{}
	}
	if analysis.Suggestions == nil {
		analysis.Suggestions = []string{}
	}
	if analysis.ImprovementGoals == nil {
		analysis.ImprovementGoals = []string{}
	}
	if analysis.Directives == nil {
		analysis.Directives = []DirectiveSuggestion{}
	}
	if analysis.CodeChanges == nil {
		analysis.CodeChanges = []string{}
	}

	return &analysis, nil
}
