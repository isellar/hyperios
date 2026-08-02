package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/selfmodify"
)

// buildSelfModifyManager constructs a *selfmodify.Manager from cfg, filling
// in sensible defaults (current executable path, current working directory)
// for any field the user hasn't explicitly set.
// buildSelfModifyManager returns a Manager for CLI use (`hyperi selfmodify
// verify/status/rollback`): re-exec is disabled, since replaying argv after
// a one-shot CLI command's own re-exec would just re-run that same command
// again in the new process. Apply()/Rollback() print a "restart manually"
// message instead of automatically restarting.
func buildSelfModifyManager(cfg *config.Config) *selfmodify.Manager {
	sourceDir, binaryPath := selfModifyPaths(cfg)
	return selfmodify.NewManager(sourceDir, binaryPath, selfmodify.Options{})
}

// buildSelfModifyManagerForServer returns a Manager for use inside a
// long-running `hyperi serve` process, where re-exec after a successful
// Apply()/Rollback() is the correct behavior (it restarts the server with
// the same argv, i.e. `serve` again).
func buildSelfModifyManagerForServer(cfg *config.Config) *selfmodify.Manager {
	sourceDir, binaryPath := selfModifyPaths(cfg)
	return selfmodify.NewManager(sourceDir, binaryPath, selfmodify.Options{ReExecEnabled: true})
}

// selfModifyPaths resolves the source directory and binary path from cfg,
// falling back to the current working directory / currently-running
// executable when unset.
func selfModifyPaths(cfg *config.Config) (sourceDir, binaryPath string) {
	sourceDir = cfg.SelfModifySourceDir
	if sourceDir == "" {
		sourceDir, _ = os.Getwd()
	}
	binaryPath = cfg.SelfModifyBinaryPath
	if binaryPath == "" {
		if exe, err := currentExecutablePath(); err == nil {
			binaryPath = exe
		}
	}
	return sourceDir, binaryPath
}

// ── selfmodify command ────────────────────────────────────────────────────────

func buildSelfModifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "selfmodify",
		Short: "Let the agent rebuild, verify, and apply changes to its own source code",
		Long: `HyperiOS can rebuild its own source tree, run the same build+vet+test gate
CI enforces, and — only if that passes — swap in the new binary and restart
into it, without you needing to manually rebuild/redeploy.

Nothing about this changes until you run 'hyperi selfmodify enable' and
confirm — the self_modify tool isn't registered, and the agent has no way
to touch its own binary, until then.`,
	}
	cmd.AddCommand(buildSelfModifyEnableCmd())
	cmd.AddCommand(buildSelfModifyDisableCmd())
	cmd.AddCommand(buildSelfModifyStatusCmd())
	cmd.AddCommand(buildSelfModifyVerifyCmd())
	cmd.AddCommand(buildSelfModifyRollbackCmd())
	return cmd
}

func buildSelfModifyEnableCmd() *cobra.Command {
	var (
		yes        bool
		sourceDir  string
		binaryPath string
	)

	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Confirm and enable self-modification",
		Long: `Registers the self_modify tool, giving the agent the ability to rebuild its
own source tree, verify the result (go build + go vet + go test ./...,
matching CI), and — only if that passes — replace the running binary and
restart into it.

Every applied binary is backed up first (see 'hyperi selfmodify status');
'hyperi selfmodify rollback' restores the most recent backup.

Use --yes to skip the confirmation prompt (e.g. for scripted provisioning).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSelfModifyEnable(sourceDir, binaryPath, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	cmd.Flags().StringVar(&sourceDir, "source-dir", "", "HyperiOS source tree to build from (default: current directory)")
	cmd.Flags().StringVar(&binaryPath, "binary-path", "", "Installed binary to replace (default: currently-running executable)")
	return cmd
}

func runSelfModifyEnable(sourceDirOverride, binaryPathOverride string, skipConfirm bool) error {
	cfg, err := loadConfig("")
	if err != nil {
		return err
	}

	sourceDir := sourceDirOverride
	if sourceDir == "" {
		sourceDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("determine source directory: %w", err)
		}
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "go.mod")); err != nil {
		return fmt.Errorf("no go.mod found at %s — pass --source-dir pointing at the HyperiOS source tree", sourceDir)
	}

	binaryPath := binaryPathOverride
	if binaryPath == "" {
		exe, err := currentExecutablePath()
		if err != nil {
			return fmt.Errorf("determine current executable path: %w", err)
		}
		binaryPath = exe
	}

	fmt.Printf("Source tree:     %s\n", sourceDir)
	fmt.Printf("Installed binary: %s\n", binaryPath)
	fmt.Println()
	fmt.Println("This will:")
	fmt.Println("  1. Register a 'self_modify' tool the agent can call during any goal")
	fmt.Println("  2. Let it run 'go build && go vet ./... && go test ./...' against the source tree")
	fmt.Println("  3. On a passing verify + explicit 'apply' action, replace the binary above")
	fmt.Println("     and restart the running server into it (same PID, no downtime)")
	fmt.Println()
	fmt.Println("Every applied binary is backed up first (up to 5 kept). Revert anytime with:")
	fmt.Println("  hyperi selfmodify disable   (stops the agent from doing this again)")
	fmt.Println("  hyperi selfmodify rollback  (restores the most recent backup right now)")

	if !skipConfirm {
		if !confirm("Proceed?") {
			fmt.Println("Aborted — no changes made.")
			return nil
		}
	}

	cfg.SelfModifyEnabled = true
	cfg.SelfModifyExplicitlyDisabled = false
	cfg.SelfModifySourceDir = sourceDir
	cfg.SelfModifyBinaryPath = binaryPath
	cfg.SelfModifyConfirmedAt = time.Now()
	if err := config.Save(defaultConfigPath(), cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println("\nSelf-modification enabled.")
	fmt.Println("Restart 'hyperi serve' for this to take effect (if already running).")
	return nil
}

func buildSelfModifyDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Disable self-modification immediately",
		Long: `Sets self_modify_enabled=false. Takes effect after restarting 'hyperi
serve' (if already running) — the self_modify tool is not re-registered on
the next startup. Does not touch any previously-applied binary or backups;
use 'hyperi selfmodify rollback' separately if you also want to revert the
currently-running binary.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig("")
			if err != nil {
				return err
			}
			cfg.SelfModifyEnabled = false
			cfg.SelfModifyExplicitlyDisabled = true
			if err := config.Save(defaultConfigPath(), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Println("Self-modification disabled.")
			fmt.Println("Restart 'hyperi serve' for this to take effect (if already running).")
			fmt.Println("To re-enable: hyperi selfmodify enable")
			return nil
		},
	}
}

func buildSelfModifyStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current self-modification configuration and available backups",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig("")
			if err != nil {
				return err
			}

			effective := !cfg.SelfModifyExplicitlyDisabled
			status := "enabled (default)"
			if cfg.SelfModifyEnabled {
				status = "enabled (confirmed)"
			}
			if cfg.SelfModifyExplicitlyDisabled {
				status = "disabled (run 'hyperi selfmodify enable' to re-enable)"
			}
			fmt.Printf("self_modify_status:  %s\n", status)
			fmt.Printf("effective:           %v\n", effective)
			fmt.Printf("source_dir:          %s\n", orNone(cfg.SelfModifySourceDir))
			fmt.Printf("binary_path:         %s\n", orNone(cfg.SelfModifyBinaryPath))
			if !cfg.SelfModifyConfirmedAt.IsZero() {
				fmt.Printf("confirmed_at:        %s\n", cfg.SelfModifyConfirmedAt.Format(time.RFC3339))
			}

			if cfg.SelfModifySourceDir == "" && cfg.SelfModifyBinaryPath == "" {
				return nil
			}

			mgr := buildSelfModifyManager(cfg)
			backups, err := mgr.ListBackups()
			if err != nil {
				fmt.Printf("backups:             error: %v\n", err)
				return nil
			}
			fmt.Println()
			if len(backups) == 0 {
				fmt.Println("backups:             (none)")
				return nil
			}
			fmt.Println("backups (newest first):")
			for _, b := range backups {
				fmt.Printf("  %s  (%s)\n", b.Name, b.CreatedAt.Format(time.RFC3339))
			}
			return nil
		},
	}
}

func buildSelfModifyVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Run build+vet+test against the source tree without applying anything",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig("")
			if err != nil {
				return err
			}
			if cfg.SelfModifySourceDir == "" {
				return fmt.Errorf("self-modification not configured yet — run 'hyperi selfmodify enable' first")
			}
			mgr := buildSelfModifyManager(cfg)
			result, err := mgr.Verify(cmd.Context())
			if err != nil {
				return fmt.Errorf("verify: %w", err)
			}
			fmt.Print(result.Summary())
			if !result.Passed {
				os.Exit(1)
			}
			return nil
		},
	}
}

func buildSelfModifyRollbackCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Restore the most recently backed-up binary and restart into it",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig("")
			if err != nil {
				return err
			}
			if cfg.SelfModifyBinaryPath == "" {
				return fmt.Errorf("self-modification not configured yet — run 'hyperi selfmodify enable' first")
			}
			if !yes && !confirm(fmt.Sprintf("Restore the most recent backup of %s?", cfg.SelfModifyBinaryPath)) {
				fmt.Println("Aborted — no changes made.")
				return nil
			}
			mgr := buildSelfModifyManager(cfg)
			msg, err := mgr.Rollback()
			if err != nil {
				return fmt.Errorf("rollback: %w", err)
			}
			fmt.Println(msg)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

// ── misc ──────────────────────────────────────────────────────────────────────

// currentExecutablePath returns the resolved path of the currently-running
// executable, following symlinks (e.g. if invoked via a PATH symlink).
func currentExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil // fall back to the unresolved path rather than failing
	}
	return resolved, nil
}
