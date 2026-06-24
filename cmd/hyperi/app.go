package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/isellar/hyperios/internal/self_improvement"
)

// App is the top-level application object. It owns the wired module set and
// runs the main interaction loop.
type App struct {
	modules    *Modules
	goalCount  int
}

// NewApp constructs an App from the wired module set.
func NewApp(modules *Modules) *App {
	return &App{modules: modules}
}

// Run starts the main interaction loop.
//
// It reads user input line-by-line from stdin, submits each line as a goal,
// executes it via the Processor, feeds the result to SelfImprovement, and
// prints the outcome to stdout.
//
// Special inputs:
//   - "quit" or "exit" — terminates the loop cleanly.
//
// Every 10 completed goals the SelfImprovement.Analyze cycle is triggered.
// The loop also exits on context cancellation.
func (a *App) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("HyperiOS agent shell. Type your intent, or \"exit\" to quit.")
	fmt.Print("> ")

	for {
		// Respect context cancellation between reads.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !scanner.Scan() {
			// EOF or scanner error — exit cleanly.
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("app: read input: %w", err)
			}
			return nil
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}

		if line == "quit" || line == "exit" {
			fmt.Println("Goodbye.")
			return nil
		}

		result, err := a.runGoal(ctx, line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		} else if result != "" {
			fmt.Println(result)
		}

		fmt.Print("> ")
	}
}

// runGoal submits a single goal through the full pipeline and returns the
// output string. It also records the result with SelfImprovement and
// triggers an analysis cycle every 10 goals.
func (a *App) runGoal(ctx context.Context, description string) (string, error) {
	// 1. Submit to GoalFulfillment — creates a tracked Goal.
	goal, err := a.modules.GoalFulfillment.SubmitGoal(description)
	if err != nil {
		return "", fmt.Errorf("submit goal: %w", err)
	}

	// 2. Queue through Processor (governor review + prioritisation).
	if err := a.modules.Processor.QueueGoal(goal); err != nil {
		return "", fmt.Errorf("queue goal: %w", err)
	}

	// 3. Execute the highest-priority queued goal.
	agentResult, err := a.modules.Processor.RunNext()
	if err != nil {
		return "", fmt.Errorf("run goal: %w", err)
	}

	// Build result values for recording.
	var output, errMsg string
	success := true
	if agentResult != nil {
		output = agentResult.Output
		success = agentResult.Success
		errMsg = agentResult.Error
	}

	// 4. Record result with SelfImprovement.
	a.modules.SelfImprovement.RecordResult(self_improvement.GoalResult{
		GoalID:      goal.ID,
		Description: description,
		Success:     success,
		Output:      output,
		ErrorMsg:    errMsg,
	})

	// 5. Periodically trigger analysis (every 10 goals).
	a.goalCount++
	if a.goalCount%10 == 0 {
		if analyzeErr := a.modules.SelfImprovement.Analyze(); analyzeErr != nil {
			// Non-fatal: log but don't surface to the user.
			fmt.Fprintf(os.Stderr, "self-improvement analyze: %v\n", analyzeErr)
		}
	}

	// 6. Return the output (may be empty for stub agents).
	if !success && errMsg != "" {
		return "", fmt.Errorf("%s", errMsg)
	}
	return output, nil
}
