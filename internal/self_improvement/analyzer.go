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
	Patterns         []string `json:"patterns"`
	Suggestions      []string `json:"suggestions"`
	ImprovementGoals []string `json:"improvement_goals"`
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

const analyzerSystem = `You are an AI system analyst for HyperiOS. Your job is to review goal execution results and identify patterns that indicate where the system could improve.

Respond ONLY with valid JSON in this exact format — no markdown, no extra text:
{
  "patterns": ["<pattern observed>", ...],
  "suggestions": ["<concrete suggestion>", ...],
  "improvement_goals": ["<actionable improvement goal for the system>", ...]
}

Keep each string concise (one sentence). Return empty arrays if no patterns are found.`

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

	return &analysis, nil
}
