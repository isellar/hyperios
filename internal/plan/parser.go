package plan

import (
	"bufio"
	"os"
	"strings"
)

// StageStatus represents the execution state of a pipeline stage.
type StageStatus struct {
	Stage     string
	Status    string // "in-progress", "completed", "failed"
	Started   string
	Completed string
	Error     string
}

// StepState represents the execution state of a single action step.
type StepState struct {
	StepID     string
	Status     string // "in-progress", "completed", "skipped", "halted", "pending"
	Result     string // "success", "failure", "skipped", ""
	OnFailure  string
	DurationMS string
}

// PlanState is the parsed state of a plan document.
// Used by crash recovery and resume logic.
type PlanState struct {
	SessionID string
	Name      string // human-readable plan name from frontmatter
	Status    string // document-level status from frontmatter
	Attempt   int

	// Stages contains the status of each pipeline stage, keyed by stage name.
	Stages map[string]StageStatus

	// Steps contains the execution state of each action step, keyed by step ID.
	Steps map[string]StepState

	// PendingApproval is the step ID of a step waiting for user approval, if any.
	PendingApproval string
}

// ParsePlanDoc parses a plan document at path and returns the current execution state.
//
// The parser only reads content inside ```hyperi-meta blocks. All other content
// (LLM output, command stdout, markdown prose) is ignored entirely. This prevents
// false positives where command output or LLM prose contains field-like strings.
func ParsePlanDoc(path string) (*PlanState, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	state := &PlanState{
		Stages: make(map[string]StageStatus),
		Steps:  make(map[string]StepState),
	}

	scanner := bufio.NewScanner(f)
	var inMeta bool
	var currentMeta []string
	var attempt int

	for scanner.Scan() {
		line := scanner.Text()

		// Parse frontmatter fields (lines before the first ## heading)
		if strings.HasPrefix(line, "Session: ") {
			state.SessionID = strings.TrimPrefix(line, "Session: ")
			continue
		}
		if strings.HasPrefix(line, "Plan: ") {
			state.Name = strings.TrimPrefix(line, "Plan: ")
			continue
		}
		if strings.HasPrefix(line, "Status: ") {
			state.Status = strings.TrimPrefix(line, "Status: ")
			continue
		}
		if strings.HasPrefix(line, "Attempt: ") {
			n := 0
			_, _ = strings.NewReader(strings.TrimPrefix(line, "Attempt: ")), &n
			// simple int parse
			attempt = parseIntFast(strings.TrimPrefix(line, "Attempt: "))
			state.Attempt = attempt
			continue
		}

		// Track hyperi-meta fence blocks
		if line == "```hyperi-meta" {
			inMeta = true
			currentMeta = nil
			continue
		}
		if inMeta && line == "```" {
			inMeta = false
			state.processMeta(currentMeta)
			currentMeta = nil
			continue
		}
		if inMeta {
			currentMeta = append(currentMeta, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return state, nil
}

// processMeta parses a slice of key: value lines from a hyperi-meta block
// and updates the PlanState accordingly.
func (s *PlanState) processMeta(lines []string) {
	fields := parseFields(lines)

	// Stage block
	if stage, ok := fields["stage"]; ok {
		status := fields["status"]
		ss := StageStatus{
			Stage:     stage,
			Status:    status,
			Started:   fields["started"],
			Completed: fields["completed"],
			Error:     fields["error"],
		}
		s.Stages[stage] = ss
		return
	}

	// Step block
	if stepID, ok := fields["step_id"]; ok {
		result := fields["result"]
		status := fields["status"]

		// Map result to canonical status for the resume parser
		if status == "" {
			switch result {
			case "success":
				status = "completed"
			case "failure":
				status = "failed"
			case "skipped":
				status = "skipped"
			default:
				status = "in-progress"
			}
		}

		ss := StepState{
			StepID:     stepID,
			Status:     status,
			Result:     result,
			OnFailure:  fields["on_failure"],
			DurationMS: fields["duration_ms"],
		}

		// Detect pending approval: step is in-progress with no result
		if status == "in-progress" && result == "" {
			s.PendingApproval = stepID
		}

		s.Steps[stepID] = ss
	}
}

// NextPendingStage returns the name of the first pipeline stage that is not
// completed, or "" if all stages are complete.
// Order: intent → plan → adversarial → arbiter
func (s *PlanState) NextPendingStage() string {
	stageOrder := []string{"intent", "plan", "adversarial", "arbiter"}
	for _, stage := range stageOrder {
		ss, ok := s.Stages[stage]
		if !ok || ss.Status != "completed" {
			return stage
		}
	}
	return ""
}

// NextPendingStep returns the ID of the first step that has not completed
// successfully. Returns "" if all steps are done.
func (s *PlanState) NextPendingStep(planStepIDs []string) string {
	for _, id := range planStepIDs {
		ss, ok := s.Steps[id]
		if !ok {
			// Step has no record at all — not yet started
			return id
		}
		if ss.Status != "completed" && ss.Status != "skipped" {
			return id
		}
	}
	return ""
}

// IsStageComplete reports whether a pipeline stage has completed successfully.
func (s *PlanState) IsStageComplete(stage string) bool {
	ss, ok := s.Stages[stage]
	return ok && ss.Status == "completed"
}

// AllStepsComplete reports whether all steps in planStepIDs are completed or skipped.
func (s *PlanState) AllStepsComplete(planStepIDs []string) bool {
	for _, id := range planStepIDs {
		ss, ok := s.Steps[id]
		if !ok {
			return false
		}
		if ss.Status != "completed" && ss.Status != "skipped" {
			return false
		}
	}
	return true
}

// parseFields parses key: value lines into a map.
func parseFields(lines []string) map[string]string {
	fields := make(map[string]string, len(lines))
	for _, line := range lines {
		idx := strings.Index(line, ": ")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+2:])
		fields[key] = val
	}
	return fields
}

// parseIntFast parses a simple non-negative integer string. Returns 0 on failure.
func parseIntFast(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
