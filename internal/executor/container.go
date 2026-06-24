package executor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/events"
	"github.com/isellar/hyperios/internal/capability"
	"github.com/isellar/hyperios/internal/types"
)

type ContainerExecutor struct {
	registry  *capability.Registry
	enforcer  *capability.Enforcer
	workspace string
	image     string
	notifier  *events.Notifier
	sessionID string
}

func NewContainer(registry *capability.Registry, workspace string, image string) *ContainerExecutor {
	if image == "" {
		image = "ubuntu:24.04"
	}
	return &ContainerExecutor{
		registry:  registry,
		enforcer:  capability.NewEnforcer(registry),
		workspace: workspace,
		image:     image,
	}
}

// NewContainerWithNotifier creates a ContainerExecutor with event notifier support.
func NewContainerWithNotifier(registry *capability.Registry, workspace, image string, n *events.Notifier, sessionID string) *ContainerExecutor {
	e := NewContainer(registry, workspace, image)
	e.notifier = n
	e.sessionID = sessionID
	return e
}

func (e *ContainerExecutor) Name() string {
	return "container"
}

func (e *ContainerExecutor) Validate(step types.ActionStep) error {
	return e.enforcer.Validate(step)
}

func (e *ContainerExecutor) publish(kind events.EventKind, stepID string, payload any) {
	if e.notifier == nil {
		return
	}
	e.notifier.PublishStep(kind, e.sessionID, stepID, payload)
}

func (e *ContainerExecutor) Execute(ctx context.Context, step types.ActionStep) (*types.ExecutionResult, error) {
	start := time.Now()

	if err := e.enforcer.Validate(step); err != nil {
		return &types.ExecutionResult{
			StepID:   step.ID,
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start).Milliseconds(),
		}, err
	}

	e.publish(events.EventStepStarted, step.ID, step.Description)

	output, execErr := e.executeInContainer(step)

	result := &types.ExecutionResult{
		StepID:   step.ID,
		Duration: time.Since(start).Milliseconds(),
	}

	if execErr != nil {
		result.Success = false
		result.Error = execErr.Error()
		// Apply on_failure policy (mirror of LocalExecutor behaviour)
		switch step.OnFailure {
		case "skip":
			result.Error = fmt.Sprintf("skipped: %s", execErr)
			e.publish(events.EventStepSkipped, step.ID, result.Error)
			return result, ErrStepSkipped
		case "replan":
			e.publish(events.EventStepFailed, step.ID, result.Error)
			return result, ErrReplan
		default:
			e.publish(events.EventStepFailed, step.ID, result.Error)
			return result, execErr
		}
	}

	result.Success = true
	result.Output = output
	e.publish(events.EventStepCompleted, step.ID, result)
	return result, nil
}

func (e *ContainerExecutor) executeInContainer(step types.ActionStep) (string, error) {
	switch {
	case strings.HasPrefix(step.Capability.Type, "execute:shell"):
		return e.runShellInContainer(step)
	case strings.HasPrefix(step.Capability.Type, "execute:git"):
		return e.runGitInContainer(step)
	case strings.HasPrefix(step.Capability.Type, "read:file"):
		return e.runReadFileInContainer(step)
	default:
		return "", &UnsupportedCapabilityError{Type: step.Capability.Type}
	}
}

func (e *ContainerExecutor) runShellInContainer(step types.ActionStep) (string, error) {
	// Use literal Command[] if provided (Phase 1A+ path)
	if len(step.Command) > 0 {
		containerArgs := []string{
			"run", "--rm",
			"-v", e.workspace + ":/workspace",
			"-w", "/workspace",
			e.image,
		}
		containerArgs = append(containerArgs, step.Command...)
		cmd := exec.Command("docker", containerArgs...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return string(output), fmt.Errorf("docker: %w", err)
		}
		return string(output), nil
	}

	// Legacy fallback: NL keyword extraction
	command := extractCommand(step.Description, step.Capability.Scope)
	args := buildContainerShellArgs(step.Description, step.Capability.Scope)
	containerArgs := []string{
		"run", "--rm",
		"-v", e.workspace + ":/workspace",
		"-w", "/workspace",
		e.image,
		command,
	}
	containerArgs = append(containerArgs, args...)
	cmd := exec.Command("docker", containerArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("docker: %w", err)
	}
	return string(output), nil
}

func (e *ContainerExecutor) runGitInContainer(step types.ActionStep) (string, error) {
	// Use literal Command[] if provided (must start with "git")
	var gitArgs []string
	if len(step.Command) > 0 {
		if step.Command[0] != "git" {
			return "", fmt.Errorf("execute:git: Command[0] must be 'git', got %q", step.Command[0])
		}
		gitArgs = step.Command[1:]
	} else {
		gitArgs = extractGitCommand(step.Description)
	}

	containerArgs := []string{
		"run", "--rm",
		"-v", e.workspace + ":/workspace",
		"-w", "/workspace",
		"--entrypoint", "git",
		e.image,
	}
	containerArgs = append(containerArgs, gitArgs...)
	cmd := exec.Command("docker", containerArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("docker git: %w", err)
	}
	return string(output), nil
}

func (e *ContainerExecutor) runReadFileInContainer(step types.ActionStep) (string, error) {
	// Prefer Command[0] as the path if provided
	scope := step.Capability.Scope
	if len(step.Command) > 0 {
		scope = step.Command[0]
	}

	path := capability.ExpandWorkspace(scope, e.workspace)
	// Make path relative to the workspace mount inside the container
	path = strings.TrimPrefix(path, e.workspace)
	if !strings.HasPrefix(path, "/") {
		path = "/workspace/" + path
	}

	containerArgs := []string{
		"run", "--rm",
		"-v", e.workspace + ":/workspace",
		"-w", "/workspace",
		"--entrypoint", "cat",
		e.image,
		path,
	}

	cmd := exec.Command("docker", containerArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("docker cat: %w", err)
	}
	return string(output), nil
}

func buildContainerShellArgs(desc, scope string) []string {
	descLower := strings.ToLower(desc)
	if strings.Contains(descLower, "grep") || strings.Contains(descLower, "search") {
		pattern := "TODO"
		if strings.Contains(descLower, "fixme") {
			pattern = "FIXME"
		}
		return []string{"-rn", "--", pattern, "."}
	}
	if strings.Contains(descLower, "list") || strings.Contains(descLower, "ls") {
		return []string{"-la"}
	}
	return []string{}
}

func (e *ContainerExecutor) PullImage() error {
	cmd := exec.Command("docker", "pull", e.image)
	return cmd.Run()
}

func (e *ContainerExecutor) ImageAvailable() bool {
	cmd := exec.Command("docker", "image", "inspect", e.image)
	return cmd.Run() == nil
}
