// Package processor prioritises goals, delegates to autonomous agents, and
// coordinates execution results back to the goal-fulfilment system.
package processor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/memory"
	"github.com/isellar/hyperios/internal/types"
)

// AgentState is the lifecycle state of a spawned agent.
type AgentState string

const (
	AgentStateRunning   AgentState = "running"
	AgentStateCompleted AgentState = "completed"
	AgentStateFailed    AgentState = "failed"
)

// AgentResult holds the outcome of an agent run.
type AgentResult struct {
	// GoalID identifies which goal this result belongs to. Populated by
	// Processor.RunNext before the result is returned to the caller.
	GoalID  string `json:"goal_id,omitempty"`
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
	// Steps records each tool call the agent made and its result, in order.
	// Useful for surfacing "what actually happened" to a caller/UI.
	Steps []AgentStep `json:"steps,omitempty"`
}

// AgentStep is a single tool invocation made by the agent while working a goal.
type AgentStep struct {
	Tool    string `json:"tool"`
	Input   string `json:"input"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error"`
}

// Agent represents a spawned autonomous agent.
type Agent struct {
	ID        string
	GoalID    string
	State     AgentState
	Result    *AgentResult
	StartedAt time.Time
}

// MemoryQuerier is a narrow interface that lets the agent query memory without
// creating a direct dependency on *memory.Memory (which would risk circular
// imports if memory ever imports processor).
type MemoryQuerier interface {
	RecallContext(key string) (interface{}, bool)
	SearchContext(query string) ([]*memory.MemoryEntry, error)
	// ListDirectives returns every standing behavioral directive currently
	// in effect (user-authored or learned via SelfImprovement). Processor
	// fetches these once per RunNext call and passes them into Spawn so they
	// apply to every goal, not just the one that created them.
	ListDirectives() ([]types.Directive, error)
}

// ToolCaller is the narrow interface the agent uses to discover and invoke
// I/O tools. *io_toolbox.IOToolbox implements this interface.
type ToolCaller interface {
	// ListTools returns the names of all registered tools.
	ListTools() []string
	// DescribeTool returns the human-readable description for a tool, or
	// ("", false) if the tool is not registered.
	DescribeTool(name string) (string, bool)
	// ExecuteTool runs the named tool with the given input and returns its output.
	ExecuteTool(name, input string) (string, error)
}

// DefaultMaxToolIterations bounds how many tool-call round-trips a single
// agent run may perform before being forced to stop, preventing runaway
// loops. This is deliberately generous by default (a local model working
// through a genuinely multi-step goal — e.g. "install and configure X, then
// verify it's running" — can easily need a dozen+ tool calls); override via
// AgentSpawner.SetMaxToolIterations for goals that need to run even longer.
const DefaultMaxToolIterations = 30

// AgentSpawner creates and runs autonomous agents backed by an LLM.
type AgentSpawner struct {
	llmClient         llm.Completer
	toolbox           ToolCaller
	maxToolIterations int
}

// NewAgentSpawner returns an AgentSpawner that uses llmClient for completions.
func NewAgentSpawner(llmClient llm.Completer) *AgentSpawner {
	return &AgentSpawner{llmClient: llmClient, maxToolIterations: DefaultMaxToolIterations}
}

// SetToolbox injects the ToolCaller used for tool-use. If unset, or if the
// underlying llmClient does not support tool-use (llm.ToolCompleter), the
// agent falls back to a single-shot narrative response.
func (s *AgentSpawner) SetToolbox(tb ToolCaller) {
	s.toolbox = tb
}

// SetMaxToolIterations overrides the number of tool-call round-trips a
// single agent run may perform before being forced to conclude. n <= 0 is
// ignored (keeps the current value).
func (s *AgentSpawner) SetMaxToolIterations(n int) {
	if n > 0 {
		s.maxToolIterations = n
	}
}

const agentSystem = `You are an autonomous execution agent for HyperiOS.
You receive a single goal. Accomplish it completely using the tools available.

Rules:
- Use "shell" to run commands, install packages, read/write files, inspect the system.
- Use "notify" to send a desktop notification when the task is done or needs attention.
- Use "schedule" to set up a recurring task (format: "cron_expr|command").
- Call tools as many times as needed. Always check output before deciding you are done.
- Never ask for permission or confirmation. Just do the work.
- When the goal is fully complete, reply with a plain-text summary (≤200 words) of what you did.
  No markdown, no code blocks in the summary.`

const agentSystemNoTools = `You are an autonomous execution agent for HyperiOS.
You receive a goal and a set of directives that constrain your behaviour.
Your task is to reason about how to fulfil the goal, taking the directives
into account, and return a structured plain-text execution summary.

Return ONLY a short paragraph (<=200 words) describing what you would do to
achieve the goal. Do not include JSON, markdown headers, or code blocks.`

// Spawn creates a new Agent and runs it to completion.
//
// If a ToolCaller is wired and the LLM client supports tool-use
// (llm.ToolCompleter), the agent runs a bounded tool-call loop: it may call
// shell/notify/schedule tools, observe their output, and iterate up to
// maxToolIterations times before being forced to conclude. Otherwise it falls
// back to a single-shot narrative response (no real execution).
func (s *AgentSpawner) Spawn(
	ctx context.Context,
	goal *types.Goal,
	directives []types.Directive,
	mem MemoryQuerier,
) (*Agent, error) {
	if goal == nil {
		return nil, fmt.Errorf("agent spawner: goal must not be nil")
	}

	agent := &Agent{
		ID:        fmt.Sprintf("agent-%s-%d", goal.ID, time.Now().UnixNano()),
		GoalID:    goal.ID,
		State:     AgentStateRunning,
		StartedAt: time.Now(),
	}

	toolCompleter, canUseTools := s.llmClient.(llm.ToolCompleter)
	if canUseTools && s.toolbox != nil {
		agent.Result = s.runWithTools(ctx, toolCompleter, goal, directives, mem)
	} else {
		agent.Result = s.runNarrative(ctx, goal, directives, mem)
	}

	if agent.Result.Success {
		agent.State = AgentStateCompleted
	} else {
		agent.State = AgentStateFailed
	}
	return agent, nil
}

// runWithTools drives the tool-use loop: it repeatedly calls the model,
// executes any requested tool calls, feeds the results back, and stops when
// the model produces a final text-only turn or maxToolIterations is reached.
func (s *AgentSpawner) runWithTools(
	ctx context.Context,
	client llm.ToolCompleter,
	goal *types.Goal,
	directives []types.Directive,
	mem MemoryQuerier,
) *AgentResult {
	tools := s.toolDefs()
	messages := []llm.Message{llm.UserMessage(llm.TextPart(buildAgentPrompt(goal, directives, mem)))}

	var steps []AgentStep
	var lastText string

	for i := 0; i < s.maxToolIterations; i++ {
		resp, err := client.CompleteWithTools(ctx, agentSystem, messages, tools)
		if err != nil {
			return &AgentResult{
				Success: false,
				Error:   fmt.Sprintf("llm error: %v", err),
				Steps:   steps,
			}
		}

		if resp.Text != "" {
			lastText = resp.Text
		}
		messages = append(messages, resp.Assistant)

		if len(resp.ToolCalls) == 0 {
			// Model concluded its turn — done.
			return &AgentResult{
				Success: true,
				Output:  strings.TrimSpace(lastText),
				Steps:   steps,
			}
		}

		// Execute each requested tool call and feed results back as a single
		// user turn containing one tool_result per tool_use.
		var resultParts []llm.ContentPart
		for _, call := range resp.ToolCalls {
			input, _ := llm.SimpleToolInput(call.Input, "input")
			output, execErr := s.toolbox.ExecuteTool(call.Name, input)
			isErr := execErr != nil
			if isErr {
				output = execErr.Error()
			}
			steps = append(steps, AgentStep{
				Tool:    call.Name,
				Input:   input,
				Output:  output,
				IsError: isErr,
			})
			resultParts = append(resultParts, llm.ToolResultPart(call.ID, output, isErr))
		}
		messages = append(messages, llm.UserMessage(resultParts...))
	}

	// Exhausted iterations without a final answer — return what we have.
	return &AgentResult{
		Success: true,
		Output:  strings.TrimSpace(fmt.Sprintf("%s\n(stopped after %d tool-call rounds without a final summary)", lastText, s.maxToolIterations)),
		Steps:   steps,
	}
}

// runNarrative is the fallback single-shot path used when tool-use is
// unavailable (no toolbox wired, or the LLM client doesn't support it).
func (s *AgentSpawner) runNarrative(
	ctx context.Context,
	goal *types.Goal,
	directives []types.Directive,
	mem MemoryQuerier,
) *AgentResult {
	user := buildAgentPrompt(goal, directives, mem)

	output, err := s.llmClient.CompleteWithRetry(ctx, agentSystemNoTools, user)
	if err != nil {
		return &AgentResult{
			Success: false,
			Error:   fmt.Sprintf("llm error: %v", err),
		}
	}

	return &AgentResult{
		Success: true,
		Output:  strings.TrimSpace(output),
	}
}

// toolDefs converts the wired ToolCaller's registered tools into llm.ToolDef
// definitions. Each built-in tool (shell, notify, schedule) accepts a single
// free-form string argument named "input".
func (s *AgentSpawner) toolDefs() []llm.ToolDef {
	if s.toolbox == nil {
		return nil
	}
	names := s.toolbox.ListTools()
	defs := make([]llm.ToolDef, 0, len(names))
	for _, name := range names {
		desc, _ := s.toolbox.DescribeTool(name)
		defs = append(defs, llm.ToolDef{
			Name:        name,
			Description: desc,
			Properties: map[string]any{
				"input": map[string]any{
					"type":        "string",
					"description": "The input for this tool (e.g. a shell command, a notification message, or a 'cron_expr|command' schedule spec).",
				},
			},
			Required: []string{"input"},
		})
	}
	return defs
}

// buildAgentPrompt assembles the user-turn prompt for the LLM agent.
func buildAgentPrompt(
	goal *types.Goal,
	directives []types.Directive,
	mem MemoryQuerier,
) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Goal ID: %s\n", goal.ID))
	sb.WriteString(fmt.Sprintf("Goal description: %s\n", goal.Description))
	sb.WriteString(fmt.Sprintf("Goal state: %s\n\n", goal.State))

	if len(directives) > 0 {
		sb.WriteString("Directives (must be respected):\n")
		for _, d := range directives {
			immutable := ""
			if d.Immutable {
				immutable = " [immutable]"
			}
			sb.WriteString(fmt.Sprintf("  - [priority %d%s] %s\n", d.Priority, immutable, d.Description))
		}
		sb.WriteString("\n")
	}

	if mem != nil {
		// Search for related memory entries (e.g. past goal outcomes) whose
		// content overlaps with this goal's description. SearchContext
		// ranks by relevance and caps the result count (see
		// memory.Memory.DefaultSearchLimit), so this is always a small,
		// most-relevant slice rather than an unbounded dump — important
		// for local models with a constrained context window (see
		// AGENTS.md's note on Ollama silently truncating context that
		// overflows num_ctx).
		//
		// Note: an exact-key RecallContext(goal.Description) lookup was
		// removed here — goal outcomes are stored under a key derived from
		// the goal ID (see apiserver.storeOutcomeMemory), never under the
		// literal description text, so that lookup never hit in practice.
		entries, err := mem.SearchContext(goal.Description)
		if err == nil && len(entries) > 0 {
			sb.WriteString("Related memory entries (most relevant first):\n")
			for _, e := range entries {
				sb.WriteString(fmt.Sprintf("  - [%s] %v\n", e.Key, e.Value))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString("Accomplish this goal now using the tools available to you, then summarize the outcome.")
	return sb.String()
}
