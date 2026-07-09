// Package plan manages plan documents — the persistent, append-only execution
// record for a HyperiOS task. Each pipeline stage writes its output here in
// real time. The executor annotates each step as it runs. The resume parser
// reads the document on restart to determine where execution left off.
//
// Plan documents use a structured markdown format:
//   - Pipeline stage outputs are wrapped in named fenced blocks (hyperi-meta,
//     hyperi-intent, hyperi-plan, etc.) that the parser can locate reliably.
//   - Command output is always in ```output blocks, never parsed for fields.
//   - Machine-readable fields live exclusively in ```hyperi-meta blocks.
//
// This separation ensures that LLM prose or command stdout can never produce
// false positives when the resume parser scans for execution state.
package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

// Status values written to plan doc frontmatter.
const (
	StatusInProgress = "in-progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusHalted     = "halted"
)

// Writer is an append-only writer for a single plan document.
// All methods are safe for sequential use within a single pipeline run.
// The pipeline is sequential per task, so concurrent writes do not occur.
type Writer struct {
	mu      sync.Mutex
	path    string
	session string
	intent  string
	attempt int
}

// NewWriter creates a Writer for a new plan document at path.
// It writes the frontmatter header immediately.
func NewWriter(path, sessionID, intent string) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("plan writer: create dir: %w", err)
	}

	w := &Writer{
		path:    path,
		session: sessionID,
		intent:  intent,
		attempt: 1,
	}

	header := fmt.Sprintf(`# Task: %s
Session: %s
Status: %s
Attempt: 1
Created: %s
Updated: %s

`,
		intent,
		sessionID,
		StatusInProgress,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	)

	if err := os.WriteFile(path, []byte(header), 0o640); err != nil {
		return nil, fmt.Errorf("plan writer: write header: %w", err)
	}

	return w, nil
}

// OpenWriter opens an existing plan document for appending (used by re-plan).
func OpenWriter(path, sessionID, intent string, attempt int) (*Writer, error) {
	return &Writer{
		path:    path,
		session: sessionID,
		intent:  intent,
		attempt: attempt,
	}, nil
}

// Path returns the path to the plan document.
func (w *Writer) Path() string {
	return w.path
}

// WritePlanName updates the frontmatter to include the human-readable plan name.
func (w *Writer) WritePlanName(name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if name == "" {
		return nil
	}

	data, err := os.ReadFile(w.path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	// Insert Plan: line after the Task: line if not already present.
	var out []string
	var inserted bool
	for _, line := range lines {
		if !inserted && strings.HasPrefix(line, "# Task: ") {
			out = append(out, line)
			out = append(out, "Plan: "+name)
			inserted = true
			continue
		}
		if strings.HasPrefix(line, "Plan: ") {
			out = append(out, "Plan: "+name)
			inserted = true
			continue
		}
		out = append(out, line)
	}

	return os.WriteFile(w.path, []byte(strings.Join(out, "\n")), 0o640)
}

// WriteStageStart appends a stage section header with status: in-progress.
// The stage name becomes a markdown heading and a hyperi-meta block is opened.
func (w *Writer) WriteStageStart(stage string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	content := fmt.Sprintf(`## %s

`+"```hyperi-meta"+`
stage: %s
status: in-progress
started: %s
`+"```"+`

`,
		stageHeading(stage),
		stage,
		time.Now().UTC().Format(time.RFC3339),
	)

	return w.append(content)
}

// WriteStageComplete closes a stage as completed and appends its output.
// fenceLabel is the fence language tag for the output block (e.g. "hyperi-intent").
func (w *Writer) WriteStageComplete(stage, output, fenceLabel string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	content := fmt.Sprintf(
		"```hyperi-meta"+`
stage: %s
status: completed
completed: %s
`+"```"+`

`+"```%s"+`
%s
`+"```"+`

`,
		stage,
		time.Now().UTC().Format(time.RFC3339),
		fenceLabel,
		strings.TrimSpace(output),
	)

	if err := w.append(content); err != nil {
		return err
	}
	return w.updateStatus(StatusInProgress)
}

// WriteStageFailed records a stage failure.
func (w *Writer) WriteStageFailed(stage string, stageErr error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	content := fmt.Sprintf(
		"```hyperi-meta"+`
stage: %s
status: failed
failed: %s
error: %s
`+"```"+`

`,
		stage,
		time.Now().UTC().Format(time.RFC3339),
		stageErr.Error(),
	)

	if err := w.append(content); err != nil {
		return err
	}
	return w.updateStatus(StatusHalted)
}

// WriteStepVerdict appends a step section with its arbiter verdict.
func (w *Writer) WriteStepVerdict(step types.ActionStep, verdict, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	content := fmt.Sprintf(`### Step %s: %s
Verdict: %s — %s

`,
		step.ID,
		step.Description,
		verdict,
		reason,
	)

	return w.append(content)
}

// WriteStepApproval appends the approval decision for a modified-verdict step.
func (w *Writer) WriteStepApproval(stepID string, granted bool, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	status := "granted"
	if !granted {
		status = reason // "denied", "timed out", etc.
	}

	content := fmt.Sprintf("Approval: %s at %s\n\n", status, time.Now().UTC().Format(time.RFC3339))
	return w.append(content)
}

// WriteStepStart records that a step has begun executing.
func (w *Writer) WriteStepStart(step types.ActionStep) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	content := fmt.Sprintf(
		"```hyperi-meta"+`
step_id: %s
status: in-progress
started: %s
command: %s
`+"```"+`

`,
		step.ID,
		time.Now().UTC().Format(time.RFC3339),
		strings.Join(step.Command, " "),
	)

	return w.append(content)
}

// WriteStepResult records the result of a step execution.
func (w *Writer) WriteStepResult(step types.ActionStep, result *types.ExecutionResult) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	resultStr := "success"
	if !result.Success {
		resultStr = "failure"
	}

	meta := fmt.Sprintf(
		"```hyperi-meta"+`
step_id: %s
status: completed
result: %s
exit_code: %d
started: %s
duration_ms: %d
on_failure: %s
`+"```"+`
`,
		step.ID,
		resultStr,
		exitCode(result),
		time.Now().UTC().Format(time.RFC3339),
		result.Duration,
		step.OnFailure,
	)

	output := ""
	if result.Output != "" || result.Error != "" {
		combined := result.Output
		if result.Error != "" {
			if combined != "" {
				combined += "\n"
			}
			combined += result.Error
		}
		output = fmt.Sprintf("\n```output\n%s\n```\n", strings.TrimSpace(combined))
	}

	return w.append(meta + output + "\n")
}

// WriteStepSkipped records that a step was skipped.
func (w *Writer) WriteStepSkipped(step types.ActionStep, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	content := fmt.Sprintf(
		"```hyperi-meta"+`
step_id: %s
status: skipped
result: skipped
reason: %s
timestamp: %s
`+"```"+`

`,
		step.ID,
		reason,
		time.Now().UTC().Format(time.RFC3339),
	)

	return w.append(content)
}

// WriteReplanHeader appends a Re-plan N section to the document.
func (w *Writer) WriteReplanHeader(n int, triggerStepID string, attempt int, requiresUserConfirmation bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	confirmation := "not required"
	if requiresUserConfirmation {
		confirmation = "required"
	}

	content := fmt.Sprintf(`## Re-plan %d
Triggered by: Step %s failure
Attempt: %d of 3
User confirmation: %s
Timestamp: %s

`,
		n,
		triggerStepID,
		attempt,
		confirmation,
		time.Now().UTC().Format(time.RFC3339),
	)

	w.attempt = attempt
	return w.append(content)
}

// Finalize sets the plan document status to completed or failed.
func (w *Writer) Finalize(status string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.updateStatus(status)
}

// append writes content to the end of the plan document.
// Caller must hold w.mu.
func (w *Writer) append(content string) error {
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("plan writer: open: %w", err)
	}
	defer f.Close()
	_, err = fmt.Fprint(f, content)
	return err
}

// updateStatus rewrites the Status: line in the plan document frontmatter.
// This is the one non-append operation — it rewrites only the status line.
// Caller must hold w.mu when called from within a lock scope, or acquire it.
func (w *Writer) updateStatus(status string) error {
	data, err := os.ReadFile(w.path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "Status: ") {
			lines[i] = "Status: " + status
		}
		if strings.HasPrefix(line, "Updated: ") {
			lines[i] = "Updated: " + time.Now().UTC().Format(time.RFC3339)
		}
	}

	return os.WriteFile(w.path, []byte(strings.Join(lines, "\n")), 0o640)
}

// stageHeading returns a human-readable heading for a pipeline stage.
func stageHeading(stage string) string {
	switch stage {
	case "intent":
		return "Intent"
	case "plan":
		return "Plan"
	case "adversarial":
		return "Risk Report"
	case "arbiter":
		return "Arbiter Verdicts"
	case "execution":
		return "Execution"
	default:
		return strings.Title(stage)
	}
}

// exitCode extracts an exit code from an ExecutionResult.
// Returns 0 on success, 1 on failure (actual exit codes not yet captured).
func exitCode(r *types.ExecutionResult) int {
	if r.Success {
		return 0
	}
	return 1
}
