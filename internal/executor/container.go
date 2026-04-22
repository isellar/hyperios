package executor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/capability"
	"github.com/isellar/hyperios/internal/types"
)

type ContainerExecutor struct {
	registry  *capability.Registry
	enforcer  *capability.Enforcer
	workspace string
	image     string
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

func (e *ContainerExecutor) Name() string {
	return "container"
}

func (e *ContainerExecutor) Validate(step types.ActionStep) error {
	return e.enforcer.Validate(step)
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

	output, execErr := e.executeInContainer(step)

	result := &types.ExecutionResult{
		StepID:   step.ID,
		Duration: time.Since(start).Milliseconds(),
	}

	if execErr != nil {
		result.Success = false
		result.Error = execErr.Error()
	} else {
		result.Success = true
		result.Output = output
	}

	return result, nil
}

func (e *ContainerExecutor) executeInContainer(step types.ActionStep) (string, error) {
	desc := step.Description
	scope := step.Capability.Scope

	switch {
	case strings.HasPrefix(step.Capability.Type, "execute:shell"):
		return e.runShellInContainer(desc, scope)
	case strings.HasPrefix(step.Capability.Type, "execute:git"):
		return e.runGitInContainer(desc)
	case strings.HasPrefix(step.Capability.Type, "read:file"):
		return e.runReadFileInContainer(scope)
	default:
		return "", &UnsupportedCapabilityError{Type: step.Capability.Type}
	}
}

func (e *ContainerExecutor) runShellInContainer(desc, scope string) (string, error) {
	command := extractCommand(desc, scope)
	args := buildContainerShellArgs(desc, scope)

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

func (e *ContainerExecutor) runGitInContainer(desc string) (string, error) {
	gitArgs := extractGitCommand(desc)

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

func (e *ContainerExecutor) runReadFileInContainer(scope string) (string, error) {
	path := scope
	if strings.HasPrefix(path, "{workspace}") {
		path = strings.Replace(path, "{workspace}", e.workspace, 1)
	}
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
