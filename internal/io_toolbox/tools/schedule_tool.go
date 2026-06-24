package tools

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/isellar/hyperios/internal/scheduler"
)

// ScheduleTool schedules a recurring shell command via the internal scheduler.
// Input format: "cron_expr|command"
// Example: "0 * * * * *|echo heartbeat"
type ScheduleTool struct {
	sched *scheduler.Scheduler
}

// NewScheduleTool creates a ScheduleTool backed by the given scheduler.
// If sched is nil, a new scheduler is created without an event notifier.
func NewScheduleTool(sched *scheduler.Scheduler) *ScheduleTool {
	if sched == nil {
		sched = scheduler.New(nil)
		sched.Start()
	}
	return &ScheduleTool{sched: sched}
}

// Name returns "schedule".
func (t *ScheduleTool) Name() string { return "schedule" }

// Description returns a description of the schedule tool.
func (t *ScheduleTool) Description() string {
	return "Schedule a task to run at a given time (format: 'cron_expr|command')"
}

// Execute parses input as "cron_expr|command" and registers a recurring job.
// The job name is derived from the command string (trimmed, first 64 chars).
// Returns a confirmation message or an error.
func (t *ScheduleTool) Execute(input string) (string, error) {
	idx := strings.Index(input, "|")
	if idx < 0 {
		return "", fmt.Errorf("schedule: invalid input %q: expected 'cron_expr|command'", input)
	}

	cronExpr := strings.TrimSpace(input[:idx])
	command := strings.TrimSpace(input[idx+1:])

	if cronExpr == "" {
		return "", fmt.Errorf("schedule: cron expression must not be empty")
	}
	if command == "" {
		return "", fmt.Errorf("schedule: command must not be empty")
	}

	// Build a stable job name from the command (truncated for safety).
	jobName := command
	if len(jobName) > 64 {
		jobName = jobName[:64]
	}
	jobName = "tool:" + jobName

	if err := t.sched.Register(jobName, cronExpr, func() {
		_ = exec.Command("/bin/sh", "-c", command).Run()
	}); err != nil {
		return "", fmt.Errorf("schedule: %w", err)
	}

	return fmt.Sprintf("scheduled %q with cron %q", command, cronExpr), nil
}
