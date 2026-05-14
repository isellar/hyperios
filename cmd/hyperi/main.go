// Package main is the CLI entry point for HyperiOS.
// It wires together the full pipeline:
//
//	User Intent → Intent Agent → Planner Agent → Adversarial Agent → Policy Arbiter → Executor
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/isellar/hyperios/internal/agents"
	"github.com/isellar/hyperios/internal/arbiter"
	"github.com/isellar/hyperios/internal/bus"
	"github.com/isellar/hyperios/internal/capability"
	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/executor"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/session"
	"github.com/isellar/hyperios/internal/types"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	root := buildRoot()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// buildRoot constructs the root cobra command and all sub-commands.
func buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "hyperi",
		Short: "HyperiOS agent — AI-driven Linux OS interface",
		Long: `hyperi is the controlling agent of HyperiOS.
It accepts natural-language intent, plans a sequence of OS actions,
evaluates them for risk, and executes the arbiter-approved steps.`,
		Version: version,
	}

	root.AddCommand(buildSessionCmd())
	root.AddCommand(buildVersionCmd())

	return root
}

// buildVersionCmd returns the 'version' subcommand.
func buildVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the hyperi version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("hyperi %s\n", version)
		},
	}
}

// buildSessionCmd returns the 'session' subcommand tree.
func buildSessionCmd() *cobra.Command {
	sessionCmd := &cobra.Command{
		Use:   "session",
		Short: "Manage hyperi agent sessions",
	}

	sessionCmd.AddCommand(buildSessionStartCmd())
	sessionCmd.AddCommand(buildSessionListCmd())

	return sessionCmd
}

// buildSessionStartCmd returns 'session start [intent]'.
func buildSessionStartCmd() *cobra.Command {
	var (
		execute      bool
		dryRun       bool
		autonomyFlag int
		configPath   string
	)

	cmd := &cobra.Command{
		Use:   "start [intent]",
		Short: "Start a new agent session",
		Long: `Run the full agent pipeline for the given intent.
Without --execute, the plan is printed but not executed (same as --dry-run).
With --execute, arbiter-approved steps run automatically at the configured autonomy level.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			intent := ""
			if len(args) > 0 {
				intent = strings.Join(args, " ")
			}

			// Resolve config path
			if configPath == "" {
				configPath = defaultConfigPath()
			}

			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// --autonomy overrides persisted level
			if cmd.Flags().Changed("autonomy") {
				cfg.AutonomyLevel = autonomyFlag
			}

			// --dry-run forces non-execute mode
			if dryRun {
				execute = false
			}

			return runSession(cmd.Context(), intent, cfg, execute)
		},
	}

	cmd.Flags().BoolVar(&execute, "execute", false, "Execute arbiter-approved steps (default: dry-run)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the plan without executing (overrides --execute)")
	cmd.Flags().IntVar(&autonomyFlag, "autonomy", config.AutonomyApproved, "Autonomy level 0–4")
	cmd.Flags().StringVar(&configPath, "config", "", "Path to config.json (default: ~/.hyperi/config.json)")

	return cmd
}

// buildSessionListCmd returns 'session list'.
func buildSessionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List recent sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := session.NewManager("")
			sessions, err := mgr.List()
			if err != nil {
				return fmt.Errorf("list sessions: %w", err)
			}
			if len(sessions) == 0 {
				fmt.Println("No sessions found.")
				return nil
			}
			fmt.Printf("%-36s  %-10s  %s\n", "ID", "STATUS", "INTENT")
			for _, s := range sessions {
				status := s.Status
				if status == "" {
					status = "unknown"
				}
				fmt.Printf("%-36s  %-10s  %s\n", s.ID, status, truncate(s.Intent, 60))
			}
			return nil
		},
	}
}

// runSession executes the full Intent→Plan→Adversarial→Arbiter→Execute pipeline.
func runSession(ctx context.Context, intent string, cfg *config.Config, execute bool) error {
	// ── Gather workspace context ─────────────────────────────────────────────
	wsCtx := gatherWorkspaceContext()

	// ── Session setup ────────────────────────────────────────────────────────
	sessionID := uuid.New().String()
	state := session.NewState(sessionID, intent, wsCtx)
	state.AutonomyLevel = cfg.AutonomyLevel

	mgr := session.NewManager("")

	fmt.Printf("[hyperi] session %s started\n", sessionID)
	fmt.Printf("[hyperi] intent: %s\n", intent)
	fmt.Printf("[hyperi] autonomy: %s\n", config.AutonomyLevelText(cfg.AutonomyLevel))

	// ── LLM client ──────────────────────────────────────────────────────────
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	llmClient := llm.NewClient(apiKey)

	// ── Intent Agent ─────────────────────────────────────────────────────────
	fmt.Println("[hyperi] running intent agent...")
	ia := agents.NewIntentAgent(llmClient)
	graph, err := ia.Run(ctx, intent, wsCtx)
	if err != nil {
		return fmt.Errorf("intent agent: %w", err)
	}
	state.Goals = graph.Goals
	_ = mgr.Save(state)

	// ── Planner Agent ────────────────────────────────────────────────────────
	fmt.Println("[hyperi] running planner agent...")
	pa := agents.NewPlannerAgent(llmClient)
	plan, err := pa.Run(ctx, graph)
	if err != nil {
		return fmt.Errorf("planner agent: %w", err)
	}
	state.Plan = plan
	_ = mgr.Save(state)

	// ── Adversarial Agent ────────────────────────────────────────────────────
	fmt.Println("[hyperi] running adversarial agent...")
	aa := agents.NewAdversarialAgent(llmClient)
	report, err := aa.Run(ctx, graph, plan)
	if err != nil {
		return fmt.Errorf("adversarial agent: %w", err)
	}

	// ── Policy Arbiter ───────────────────────────────────────────────────────
	fmt.Println("[hyperi] running policy arbiter...")
	arb := arbiter.NewWithLevel(cfg.AutonomyLevel)
	verdicts := arb.Decide(plan, report)

	// ── Present plan ─────────────────────────────────────────────────────────
	stub := executor.NewStub(os.Stdout)
	stub.Present(plan, verdicts, report)

	if !execute {
		fmt.Println("\n[hyperi] dry-run mode — use --execute to run approved steps")
		state.Status = "halted"
		_ = mgr.Save(state)
		return nil
	}

	// ── Execute approved steps ───────────────────────────────────────────────
	cwd := wsCtx.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	reg := capability.NewRegistry()
	reg.SetWorkspace(cwd)

	allowlistPath := filepath.Join(defaultConfigDir(), "allowlist.yaml")
	if _, statErr := os.Stat(allowlistPath); statErr == nil {
		if loadErr := reg.LoadAllowlist(allowlistPath); loadErr != nil {
			fmt.Fprintf(os.Stderr, "[hyperi] warning: could not load allowlist: %v\n", loadErr)
		}
	}

	b := bus.New(256)
	defer b.Close()

	exec_ := executor.New(executor.ExecutorConfig{
		DryRun:       false,
		Registry:     reg,
		Workspace:    cwd,
		ExecutorType: plan.Executor,
		Bus:          b,
		SessionID:    sessionID,
	})

	verdictMap := map[string]types.ArbiterVerdict{}
	for _, v := range verdicts {
		verdictMap[v.StepID] = v
	}

	fmt.Println("\n[hyperi] executing approved steps...")
	state.Status = "in-progress"
	_ = mgr.Save(state)

	for _, step := range plan.Steps {
		v, ok := verdictMap[step.ID]
		if !ok || v.Verdict == "blocked" {
			fmt.Printf("[hyperi] skipping blocked step %s: %s\n", step.ID, step.Description)
			continue
		}

		fmt.Printf("[hyperi] executing step %s: %s\n", step.ID, step.Description)
		result, execErr := exec_.Execute(ctx, step)
		if execErr != nil {
			if errors.Is(execErr, executor.ErrStepSkipped) {
				fmt.Printf("[hyperi] step %s skipped\n", step.ID)
				state.MarkCompleted(step.ID)
				_ = mgr.Save(state)
				continue
			}
			if errors.Is(execErr, executor.ErrReplan) {
				fmt.Printf("[hyperi] step %s triggered replan — halting session\n", step.ID)
				state.Status = "halted"
				_ = mgr.Save(state)
				return fmt.Errorf("replan requested at step %s", step.ID)
			}
			// on_failure=halt or other error
			state.Status = "failed"
			_ = mgr.Save(state)
			return fmt.Errorf("step %s failed: %w", step.ID, execErr)
		}

		if result != nil && !result.Success {
			fmt.Fprintf(os.Stderr, "[hyperi] step %s returned failure: %s\n", step.ID, result.Error)
			state.Status = "failed"
			_ = mgr.Save(state)
			return fmt.Errorf("step %s failed: %s", step.ID, result.Error)
		}

		state.MarkCompleted(step.ID)
		_ = mgr.Save(state)
		fmt.Printf("[hyperi] step %s complete\n", step.ID)
	}

	completed, total := state.Progress()
	state.Status = "completed"
	_ = mgr.Save(state)

	fmt.Printf("\n[hyperi] session complete: %d/%d steps executed\n", completed, total)
	return nil
}

// gatherWorkspaceContext collects git and filesystem context for the current directory.
func gatherWorkspaceContext() types.WorkspaceContext {
	cwd, _ := os.Getwd()

	branch := runGit("rev-parse", "--abbrev-ref", "HEAD")
	log := runGit("log", "--oneline", "-5")
	status := runGit("status", "--short")

	return types.WorkspaceContext{
		Cwd:       cwd,
		GitBranch: strings.TrimSpace(branch),
		GitLog:    strings.TrimSpace(log),
		GitStatus: strings.TrimSpace(status),
	}
}

// runGit runs a git sub-command and returns stdout. Returns empty string on error.
func runGit(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// defaultConfigDir returns the directory where hyperi stores its config.
func defaultConfigDir() string {
	// Service user: /var/lib/hyperi; dev user: ~/.hyperi
	if _, err := os.Stat("/var/lib/hyperi"); err == nil {
		return "/var/lib/hyperi"
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hyperi")
}

// defaultConfigPath returns the path to config.json.
func defaultConfigPath() string {
	return filepath.Join(defaultConfigDir(), "config.json")
}

// truncate shortens s to max runes, appending "…" if truncated.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
