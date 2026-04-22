// Package shell implements the HyperiOS persistent terminal shell (TUI).
// This is the primary user-facing interface — a persistent, always-running
// terminal that accepts text (and optionally voice) input and feeds it into
// the agent pipeline.
//
// Phase 2 implementation plan:
//   - Use charmbracelet/bubbletea for the TUI framework
//   - Persistent session context across commands
//   - Streamed agent output (intent → plan → adversarial → arbiter → execution)
//   - Command history with arrow-key navigation
//   - Inline plan display with approval prompts for "modified" verdicts
//
// TODO(Phase 2): Implement the full TUI shell.
package shell

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
)

// Shell is the HyperiOS terminal interface.
// In Phase 2 this will be replaced with a bubbletea TUI.
type Shell struct {
	// handler is called with each line of user input.
	handler func(ctx context.Context, input string) error
}

// New creates a new Shell with the given input handler.
func New(handler func(ctx context.Context, input string) error) *Shell {
	return &Shell{handler: handler}
}

// Run starts the interactive shell loop. It reads lines from stdin
// and passes them to the handler.
func (s *Shell) Run(ctx context.Context) error {
	fmt.Fprintln(os.Stdout, "HyperiOS Shell — type your intent and press Enter. Ctrl+C to exit.")
	fmt.Fprintln(os.Stdout, strings.Repeat("-", 60))

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Fprint(os.Stdout, "hyperi> ")

		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			fmt.Fprintln(os.Stdout, "Goodbye.")
			break
		}

		if err := s.handler(ctx, line); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	}

	return scanner.Err()
}
