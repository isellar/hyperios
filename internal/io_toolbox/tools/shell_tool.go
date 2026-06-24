// Package tools contains concrete Tool implementations for the io_toolbox.
package tools

import (
	"os/exec"
	"strings"
)

// ShellTool executes arbitrary shell commands and returns their combined output.
type ShellTool struct{}

// NewShellTool creates a ShellTool.
func NewShellTool() *ShellTool {
	return &ShellTool{}
}

// Name returns "shell".
func (t *ShellTool) Name() string { return "shell" }

// Description returns a description of the shell tool.
func (t *ShellTool) Description() string {
	return "Execute a shell command and return output"
}

// Execute runs cmd via the system shell and returns the combined stdout+stderr.
// On Linux the command is run through /bin/sh -c; on other platforms it falls
// back to splitting on whitespace and calling exec.Command directly.
func (t *ShellTool) Execute(cmd string) (string, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", nil
	}

	out, err := exec.Command("/bin/sh", "-c", cmd).CombinedOutput()
	return string(out), err
}
