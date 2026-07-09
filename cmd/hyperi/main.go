// Package main is the CLI entry point for HyperiOS.
//
// Primary interface: `hyperi` launches the persistent TUI shell (Phase 2).
// The TUI wires the full pipeline, event bus, audit trail, plan docs, inotify
// watcher, and in-process scheduler together.
//
// Secondary (headless) interface: `hyperi session start --no-tui [intent]`
// runs a single pipeline pass without the TUI — useful over SSH or from
// systemd when no terminal is available.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/bus"
	"github.com/isellar/hyperios/internal/capability"
	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/manifest"
	"github.com/isellar/hyperios/internal/plan"
	"github.com/isellar/hyperios/internal/scheduler"
	"github.com/isellar/hyperios/internal/session"
	"github.com/isellar/hyperios/internal/shell"
)

// version is set at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

func main() {
	root := buildRoot()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ── Root command ──────────────────────────────────────────────────────────────

func buildRoot() *cobra.Command {
	var cfgPath string
	var autonomyFlag int
	autonomyChanged := false

	root := &cobra.Command{
		Use:   "hyperi",
		Short: "HyperiOS — AI-driven Linux OS interface",
		Long: `hyperi is the HyperiOS agent shell.

Running without a subcommand launches the persistent TUI shell (Phase 2).
Type your intent at the prompt; hyperi plans, executes, and reports.

Use subcommands for headless/scripted operation.`,
		Version: version,
		// Default action: launch TUI shell
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}
			if autonomyChanged {
				cfg.AutonomyLevel = autonomyFlag
			}
			return launchShell(cfg)
		},
	}

	root.PersistentFlags().StringVar(&cfgPath, "config", "", "Path to config.json (default: auto-detected)")
	root.PersistentFlags().IntVarP(&autonomyFlag, "autonomy", "a", config.AutonomyApproved,
		"Autonomy level 0–4 (overrides config for this session)")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		autonomyChanged = cmd.Flags().Changed("autonomy")
		return nil
	}

	root.AddCommand(buildSessionCmd())
	root.AddCommand(buildPlansCmd())
	root.AddCommand(buildConfigCmd())
	root.AddCommand(buildVersionCmd())

	return root
}

// ── Infrastructure bootstrap ──────────────────────────────────────────────────

// bootstrap creates all shared infrastructure (dirs, config, registry, bus,
// manifest, scheduler, audit logger, session manager).  Everything that needs
// to be shared between the shell, the headless runner, and the web UI lives
// here so it is only constructed once.
type infra struct {
	cfg        *config.Config
	dataPathFn func(string) string
	logPathFn  func(string) string
	eventBus   *bus.Bus
	registry   *capability.Registry
	validator  *capability.CommandValidator
	manifestSt *manifest.Store
	sessionMgr *session.Manager
	auditLog   *audit.Logger
	sched      *scheduler.Scheduler
	apiKey     string
	workDir    string
}

func bootstrap(cfg *config.Config) (*infra, error) {
	// ── Paths ─────────────────────────────────────────────────────────────────
	dataDir := resolveDataDir()
	logDir := resolveLogDir()

	dataPathFn := func(rel string) string { return filepath.Join(dataDir, rel) }
	logPathFn := func(rel string) string { return filepath.Join(logDir, rel) }

	for _, dir := range []string{
		dataPathFn("sessions"),
		dataPathFn("plans"),
		logDir,
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("bootstrap: create dir %s: %w", dir, err)
		}
	}

	// ── API key ───────────────────────────────────────────────────────────────
	// cfg.LLMAPIKey (set via 'hyperi config set llm_api_key ...') takes
	// precedence; otherwise fall back to the env var for whichever provider
	// is configured (cfg.LLMProvider). This lets a user with an exhausted
	// ANTHROPIC_API_KEY switch to OpenCode Zen without unsetting anything.
	apiKey := cfg.LLMAPIKey
	if apiKey == "" {
		switch cfg.LLMProvider {
		case config.ProviderOpenCodeZen:
			apiKey = os.Getenv("OPENCODE_API_KEY")
		default:
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}

	// ── Event bus ─────────────────────────────────────────────────────────────
	b := bus.New(512)

	// ── Capability registry ───────────────────────────────────────────────────
	reg := capability.NewRegistry()
	cwd, _ := os.Getwd()
	reg.SetWorkspace(cwd)

	allowlistPath := dataPathFn("allowlist.yaml")
	if _, err := os.Stat(allowlistPath); os.IsNotExist(err) {
		// Not in data dir yet — try to seed it from the repo-bundled copy,
		// then fall back to the repo path directly if seeding fails.
		repoAllowlist := findRepoAllowlist()
		if repoAllowlist != "" {
			// Copy it into the data dir so future runs find it there.
			if data, readErr := os.ReadFile(repoAllowlist); readErr == nil {
				_ = os.WriteFile(allowlistPath, data, 0o640)
			}
		}
		// Re-check: use data dir copy if it now exists, else repo path, else give up.
		if _, err2 := os.Stat(allowlistPath); os.IsNotExist(err2) {
			if repoAllowlist != "" {
				allowlistPath = repoAllowlist
			}
		}
	}
	if err := reg.LoadAllowlist(allowlistPath); err != nil {
		// Non-fatal: restricted mode — all steps will require explicit grants.
		fmt.Fprintf(os.Stderr, "Warning: could not load allowlist (%s): %v\n", allowlistPath, err)
	}

	// ── Command validator ─────────────────────────────────────────────────────
	mstore := manifest.NewStore(dataPathFn("manifest.json"))
	// Seed defaults if the manifest doesn't exist yet
	if _, err := os.Stat(dataPathFn("manifest.json")); os.IsNotExist(err) {
		mstore.SeedDefaults()
		_ = mstore.Save()
	} else {
		_ = mstore.Load()
	}
	validator := capability.NewCommandValidator(reg).WithManifest(mstore)

	// ── Session manager ───────────────────────────────────────────────────────
	sessionMgr := session.NewManager(dataPathFn("sessions"))

	// ── Audit logger ──────────────────────────────────────────────────────────
	auditLog, err := audit.NewLogger(logPathFn("audit.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("bootstrap: audit logger: %w", err)
	}

	// ── Scheduler ─────────────────────────────────────────────────────────────
	sched := scheduler.New(b)
	sched.DefaultJobs(
		// manifest:rescan
		func() {
			_ = mstore.Load()
			b.Publish(bus.Event{
				Kind:      bus.EventManifestUpdated,
				Payload:   "periodic rescan",
				Timestamp: time.Now(),
			})
		},
		// session:cleanup — remove sessions older than 30 days
		func() { _ = sessionMgr.CleanupOld(30 * 24 * time.Hour) },
		// audit:rotate — rename audit.jsonl → audit-<date>.jsonl
		func() { rotateAuditLog(logPathFn("audit.jsonl")) },
	)
	sched.Start()

	return &infra{
		cfg:        cfg,
		dataPathFn: dataPathFn,
		logPathFn:  logPathFn,
		eventBus:   b,
		registry:   reg,
		validator:  validator,
		manifestSt: mstore,
		sessionMgr: sessionMgr,
		auditLog:   auditLog,
		sched:      sched,
		apiKey:     apiKey,
		workDir:    cwd,
	}, nil
}

// ── Shell launch ──────────────────────────────────────────────────────────────

func launchShell(cfg *config.Config) error {
	infra, err := bootstrap(cfg)
	if err != nil {
		return err
	}
	defer infra.sched.Stop()
	defer infra.eventBus.Close()

	// Start inotify watcher on watched paths (best-effort, non-fatal)
	watcher, watchErr := manifest.NewWatcher(infra.manifestSt, infra.eventBus, cfg.WatchPaths)
	if watchErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: inotify watcher could not initialise: %v\n", watchErr)
	} else {
		watcher.Start()
		defer watcher.Stop()
	}

	s := shell.NewShell(shell.Config{
		APIKey:        infra.apiKey,
		HypConfig:     infra.cfg,
		ConfigPath:    defaultConfigPath(),
		EventBus:      infra.eventBus,
		Registry:      infra.registry,
		Validator:     infra.validator,
		ManifestStore: infra.manifestSt,
		SessionMgr:    infra.sessionMgr,
		AuditLogger:   infra.auditLog,
		DataPathFn:    infra.dataPathFn,
		LogPathFn:     infra.logPathFn,
		WorkDir:       infra.workDir,
	})

	return s.Run()
}

// ── session command ───────────────────────────────────────────────────────────

func buildSessionCmd() *cobra.Command {
	sessionCmd := &cobra.Command{
		Use:   "session",
		Short: "Manage agent sessions",
	}
	sessionCmd.AddCommand(buildSessionStartCmd())
	sessionCmd.AddCommand(buildSessionListCmd())
	sessionCmd.AddCommand(buildSessionResumeCmd())
	return sessionCmd
}

func buildSessionStartCmd() *cobra.Command {
	var (
		noTUI        bool
		execute      bool
		autonomyFlag int
		cfgPath      string
	)

	cmd := &cobra.Command{
		Use:   "start [intent]",
		Short: "Start a new agent session",
		Long: `Run the full agent pipeline for the given intent.

Without --no-tui, this is equivalent to running 'hyperi' and typing the intent.
With --no-tui, runs the pipeline headlessly (useful from scripts or systemd).`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			intent := strings.Join(args, " ")
			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("autonomy") {
				cfg.AutonomyLevel = autonomyFlag
			}

			if !noTUI {
				// Launch TUI with the intent pre-filled
				return launchShellWithIntent(cfg, intent, execute)
			}

			// Headless mode
			return runHeadless(cfg, intent, execute)
		},
	}

	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "Run headlessly without the TUI")
	cmd.Flags().BoolVar(&execute, "execute", false, "Execute arbiter-approved steps (headless mode only)")
	cmd.Flags().IntVar(&autonomyFlag, "autonomy", config.AutonomyApproved, "Autonomy level 0–4")
	cmd.Flags().StringVar(&cfgPath, "config", "", "Path to config.json")
	return cmd
}

func buildSessionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List recent sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := session.NewManager(filepath.Join(resolveDataDir(), "sessions"))
			sessions, err := mgr.List()
			if err != nil {
				return fmt.Errorf("list sessions: %w", err)
			}
			if len(sessions) == 0 {
				fmt.Println("No sessions found.")
				return nil
			}
			fmt.Printf("%-10s  %-10s  %-20s  %s\n", "ID", "STATUS", "UPDATED", "INTENT")
			fmt.Println(strings.Repeat("─", 72))
			for _, s := range sessions {
				status := s.Status
				if status == "" {
					status = "unknown"
				}
				fmt.Printf("%-10s  %-10s  %-20s  %s\n",
					s.ID,
					status,
					s.UpdatedAt.Format("2006-01-02 15:04:05"),
					truncate(s.Intent, 36),
				)
			}
			return nil
		},
	}
}

func buildSessionResumeCmd() *cobra.Command {
	var cfgPath string

	cmd := &cobra.Command{
		Use:   "resume <session-id>",
		Short: "Resume a halted or in-progress session",
		Long: `Re-opens the TUI and resumes a halted session.
For approval-halted sessions, re-presents the pending approval prompt.
For execution-halted sessions, re-enters the re-plan loop.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}

			dataDir := resolveDataDir()
			planPath := filepath.Join(dataDir, "plans", sessionID+".md")
			if _, err := os.Stat(planPath); os.IsNotExist(err) {
				return fmt.Errorf("no plan doc found for session %s (expected %s)", sessionID, planPath)
			}

			planState, err := plan.ParsePlanDoc(planPath)
			if err != nil {
				return fmt.Errorf("parse plan doc: %w", err)
			}

			switch planState.Status {
			case plan.StatusCompleted:
				fmt.Printf("Session %s is already completed.\n", sessionID)
				return nil
			case plan.StatusInProgress, plan.StatusHalted, plan.StatusFailed:
				fmt.Printf("Resuming session %s (status: %s)...\n", sessionID, planState.Status)
			default:
				fmt.Printf("Session %s has unknown status %q — attempting resume anyway.\n", sessionID, planState.Status)
			}

			// Launch TUI with resume intent
			resumeIntent := fmt.Sprintf("__resume__:%s", sessionID)
			return launchShellWithIntent(cfg, resumeIntent, true)
		},
	}

	cmd.Flags().StringVar(&cfgPath, "config", "", "Path to config.json")
	return cmd
}

// ── plans command ─────────────────────────────────────────────────────────────

func buildPlansCmd() *cobra.Command {
	var statusFilter string

	cmd := &cobra.Command{
		Use:   "plans",
		Short: "List plan documents by status",
		Long: `List all plan documents, optionally filtered by status.

Status values: in-progress, completed, failed, halted

Plan documents are stored at <data-dir>/plans/<session-id>.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			plansDir := filepath.Join(resolveDataDir(), "plans")
			entries, err := os.ReadDir(plansDir)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("No plans found.")
					return nil
				}
				return fmt.Errorf("read plans dir: %w", err)
			}

			type planSummary struct {
				path      string
				name      string
				status    string
				sessionID string
				modTime   time.Time
			}

			var plans []planSummary
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
					continue
				}
				path := filepath.Join(plansDir, entry.Name())
				state, err := plan.ParsePlanDoc(path)
				if err != nil {
					continue
				}
				if statusFilter != "" && state.Status != statusFilter {
					continue
				}
				info, _ := entry.Info()
				mod := time.Time{}
				if info != nil {
					mod = info.ModTime()
				}
				displayName := state.Name
				if displayName == "" {
					displayName = strings.TrimSuffix(entry.Name(), ".md")
				}
				plans = append(plans, planSummary{
					path:      path,
					name:      displayName,
					status:    state.Status,
					sessionID: state.SessionID,
					modTime:   mod,
				})
			}

			if len(plans) == 0 {
				if statusFilter != "" {
					fmt.Printf("No plans with status %q found.\n", statusFilter)
				} else {
					fmt.Println("No plans found.")
				}
				return nil
			}

			// Sort by mod time descending
			sort.Slice(plans, func(i, j int) bool {
				return plans[i].modTime.After(plans[j].modTime)
			})

			fmt.Printf("%-10s  %-12s  %-20s  %s\n", "SESSION", "STATUS", "UPDATED", "NAME")
			fmt.Println(strings.Repeat("─", 80))
			for _, p := range plans {
				fmt.Printf("%-10s  %-12s  %-20s  %s\n",
					p.sessionID,
					p.status,
					p.modTime.Format("2006-01-02 15:04:05"),
					p.name,
				)
			}
			fmt.Printf("\nPlan documents: %s\n", plansDir)
			return nil
		},
	}

	cmd.Flags().StringVarP(&statusFilter, "status", "s", "", "Filter by status (in-progress|completed|failed|halted)")
	return cmd
}

// ── config command ────────────────────────────────────────────────────────────

func buildConfigCmd() *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "View and modify HyperiOS configuration",
	}
	configCmd.AddCommand(buildConfigGetCmd())
	configCmd.AddCommand(buildConfigSetCmd())
	configCmd.AddCommand(buildConfigShowCmd())
	return configCmd
}

func buildConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a config value",
		Long: `Get a configuration value by key.

Keys: autonomy_level, approval_timeout_foreground, approval_timeout_background,
      voice_enabled, voice_push_to_talk_key, whisper_model_path,
      llm_provider, llm_api_key, llm_model`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig("")
			if err != nil {
				return err
			}
			key := args[0]
			switch key {
			case "autonomy_level":
				fmt.Printf("%d  (%s)\n", cfg.AutonomyLevel, config.AutonomyLevelText(cfg.AutonomyLevel))
			case "approval_timeout_foreground":
				fmt.Printf("%ds\n", cfg.ApprovalTimeoutForeground)
			case "approval_timeout_background":
				fmt.Printf("%ds\n", cfg.ApprovalTimeoutBackground)
			case "voice_enabled":
				fmt.Printf("%v\n", cfg.VoiceEnabled)
			case "voice_push_to_talk_key":
				fmt.Printf("%s\n", cfg.VoicePushToTalkKey)
			case "whisper_model_path":
				fmt.Printf("%s\n", cfg.WhisperModelPath)
			case "llm_provider":
				fmt.Printf("%s\n", providerOrDefault(cfg.LLMProvider))
			case "llm_api_key":
				fmt.Printf("%s\n", maskKey(cfg.LLMAPIKey))
			case "llm_model":
				fmt.Printf("%s\n", cfg.LLMModel)
			default:
				return fmt.Errorf("unknown config key %q\nValid keys: autonomy_level, approval_timeout_foreground, approval_timeout_background, voice_enabled, voice_push_to_talk_key, whisper_model_path, llm_provider, llm_api_key, llm_model", key)
			}
			return nil
		},
	}
}

// providerOrDefault returns the configured provider name, defaulting to
// config.ProviderAnthropic when unset (fresh configs created before this
// field existed).
func providerOrDefault(p string) string {
	if p == "" {
		return config.ProviderAnthropic
	}
	return p
}

// maskKey returns a masked preview of a secret for display purposes so
// 'hyperi config get llm_api_key' doesn't dump the raw key to the terminal.
func maskKey(key string) string {
	if key == "" {
		return "(unset — using provider default env var)"
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func buildConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath := defaultConfigPath()
			cfg, err := loadConfig("")
			if err != nil {
				return err
			}
			key, val := args[0], args[1]
			switch key {
			case "autonomy_level":
				n, err := strconv.Atoi(val)
				if err != nil || n < 0 || n > 4 {
					return fmt.Errorf("autonomy_level must be 0–4, got %q", val)
				}
				cfg.AutonomyLevel = n
				cfg.AutonomyUpdatedAt = time.Now()
				cfg.AutonomyUpdatedBy = currentUser()
				fmt.Printf("autonomy_level set to %d (%s)\n", n, config.AutonomyLevelText(n))
			case "approval_timeout_foreground":
				n, err := strconv.Atoi(val)
				if err != nil || n <= 0 {
					return fmt.Errorf("approval_timeout_foreground must be a positive integer (seconds)")
				}
				cfg.ApprovalTimeoutForeground = n
				fmt.Printf("approval_timeout_foreground set to %ds\n", n)
			case "approval_timeout_background":
				n, err := strconv.Atoi(val)
				if err != nil || n <= 0 {
					return fmt.Errorf("approval_timeout_background must be a positive integer (seconds)")
				}
				cfg.ApprovalTimeoutBackground = n
				fmt.Printf("approval_timeout_background set to %ds\n", n)
			case "voice_enabled":
				switch val {
				case "true", "1", "yes":
					cfg.VoiceEnabled = true
				case "false", "0", "no":
					cfg.VoiceEnabled = false
				default:
					return fmt.Errorf("voice_enabled must be true or false")
				}
				fmt.Printf("voice_enabled set to %v\n", cfg.VoiceEnabled)
			case "voice_push_to_talk_key":
				cfg.VoicePushToTalkKey = val
				fmt.Printf("voice_push_to_talk_key set to %q\n", val)
			case "whisper_model_path":
				cfg.WhisperModelPath = val
				fmt.Printf("whisper_model_path set to %q\n", val)
			case "llm_provider":
				switch val {
				case config.ProviderAnthropic, config.ProviderOpenCodeZen:
					cfg.LLMProvider = val
				default:
					return fmt.Errorf("llm_provider must be %q or %q, got %q",
						config.ProviderAnthropic, config.ProviderOpenCodeZen, val)
				}
				fmt.Printf("llm_provider set to %q\n", cfg.LLMProvider)
			case "llm_api_key":
				cfg.LLMAPIKey = val
				fmt.Printf("llm_api_key set (%s)\n", maskKey(cfg.LLMAPIKey))
			case "llm_model":
				cfg.LLMModel = val
				fmt.Printf("llm_model set to %q\n", val)
			default:
				return fmt.Errorf("unknown config key %q", key)
			}
			if err := config.Save(cfgPath, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			return nil
		},
	}
}

func buildConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show current configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig("")
			if err != nil {
				return err
			}
			fmt.Printf("Config: %s\n\n", defaultConfigPath())
			fmt.Printf("  autonomy_level              %d  (%s)\n",
				cfg.AutonomyLevel, config.AutonomyLevelText(cfg.AutonomyLevel))
			fmt.Printf("  approval_timeout_foreground %ds\n", cfg.ApprovalTimeoutForeground)
			fmt.Printf("  approval_timeout_background %ds\n", cfg.ApprovalTimeoutBackground)
			fmt.Printf("  voice_enabled               %v\n", cfg.VoiceEnabled)
			fmt.Printf("  voice_push_to_talk_key      %s\n", cfg.VoicePushToTalkKey)
			fmt.Printf("  whisper_model_path          %s\n", cfg.WhisperModelPath)
			fmt.Printf("  whisper_cli_path            %s\n", cfg.WhisperCLIPath)
			fmt.Printf("  llm_provider                %s\n", providerOrDefault(cfg.LLMProvider))
			fmt.Printf("  llm_api_key                 %s\n", maskKey(cfg.LLMAPIKey))
			fmt.Printf("  llm_model                   %s\n", cfg.LLMModel)
			if !cfg.AutonomyUpdatedAt.IsZero() {
				fmt.Printf("\n  autonomy last changed by %s at %s\n",
					cfg.AutonomyUpdatedBy,
					cfg.AutonomyUpdatedAt.Format(time.RFC3339),
				)
			}
			return nil
		},
	}
}

// ── version command ───────────────────────────────────────────────────────────

func buildVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the hyperi version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("hyperi %s\n", version)
		},
	}
}

// ── Headless pipeline runner ──────────────────────────────────────────────────

// runHeadless runs the full pipeline without the TUI. Used by
// `hyperi session start --no-tui` and the systemd service (Phase 0/1).
func runHeadless(cfg *config.Config, intent string, execute bool) error {
	infra, err := bootstrap(cfg)
	if err != nil {
		return err
	}
	defer infra.sched.Stop()
	defer infra.eventBus.Close()

	// Subscribe audit consumer
	auditCh := infra.eventBus.Subscribe()
	go bus.DrainToAudit(auditCh, infra.auditLog.Log)

	if intent == "" {
		return fmt.Errorf("intent is required in --no-tui mode")
	}

	runner := shell.NewPipelineRunner(shell.RunnerConfig{
		APIKey:        infra.apiKey,
		Provider:      cfg.LLMProvider,
		ProviderModel: cfg.LLMModel,
		AutonomyLevel: cfg.AutonomyLevel,
		ExecutorType:  "local",
		EventBus:      infra.eventBus,
		Registry:      infra.registry,
		Validator:     infra.validator,
		Manifest:      infra.manifestSt,
		SessionMgr:    infra.sessionMgr,
		AuditLogger:   infra.auditLog,
		Config:        infra.cfg,
		DataPathFn:    infra.dataPathFn,
		WorkspaceDir:  infra.workDir,
	})

	if !execute {
		// Autonomy 0 = observe only — present plan, don't execute
		cfg.AutonomyLevel = config.AutonomyObserve
	}

	return runner(context.Background(), intent, "")
}

// launchShellWithIntent opens the TUI shell with an intent pre-queued.
// The intent is submitted automatically as the first command after the TUI
// is ready. When intent is empty it just opens the shell normally.
func launchShellWithIntent(cfg *config.Config, intent string, _ bool) error {
	// For now, set the intent as an env var that the shell model picks up on
	// startup via the standard notification/startup path. Full pre-queue wiring
	// (passing the intent directly to shell.Model) is left as Phase 2 polish.
	if intent != "" && !strings.HasPrefix(intent, "__resume__:") {
		os.Setenv("HYPERI_INITIAL_INTENT", intent)
	}
	return launchShell(cfg)
}

// ── Path helpers ──────────────────────────────────────────────────────────────

// resolveDataDir returns the agent data directory.
// Priority: HYPERI_DATA_DIR env → /var/lib/hyperi (if accessible) → ~/.hyperi
//
// We check accessibility (os.ReadDir), not just existence (os.Stat).
// /var/lib/hyperi exists on this machine but is owned by the hyperi service user;
// a regular user running hyperi interactively must fall back to ~/.hyperi.
func resolveDataDir() string {
	if d := os.Getenv("HYPERI_DATA_DIR"); d != "" {
		return d
	}
	if _, err := os.ReadDir("/var/lib/hyperi"); err == nil {
		return "/var/lib/hyperi"
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hyperi")
}

// resolveLogDir returns the agent log directory.
func resolveLogDir() string {
	if d := os.Getenv("HYPERI_LOG_DIR"); d != "" {
		return d
	}
	if _, err := os.ReadDir("/var/log/hyperi"); err == nil {
		return "/var/log/hyperi"
	}
	return filepath.Join(resolveDataDir(), "logs")
}

// defaultConfigPath returns the path to config.json.
func defaultConfigPath() string {
	return filepath.Join(resolveDataDir(), "config.json")
}

// findRepoAllowlist returns the path to the bundled config/allowlist.yaml by
// searching from the executable location and known install paths.
// Returns "" if not found.
func findRepoAllowlist() string {
	candidates := []string{
		// Relative to the repo root when running from the checkout
		"config/allowlist.yaml",
		// Installed alongside the binary in /opt/hyperios
		"/opt/hyperios/config/allowlist.yaml",
		// Executable-relative: walk up from the binary to find a config/ dir
		func() string {
			exe, err := os.Executable()
			if err != nil {
				return ""
			}
			// Resolve symlinks (e.g. /usr/local/bin/hyperi -> actual path)
			exe, _ = filepath.EvalSymlinks(exe)
			// Walk up at most 3 levels looking for config/allowlist.yaml
			dir := filepath.Dir(exe)
			for i := 0; i < 3; i++ {
				candidate := filepath.Join(dir, "config", "allowlist.yaml")
				if _, err := os.Stat(candidate); err == nil {
					return candidate
				}
				dir = filepath.Dir(dir)
			}
			return ""
		}(),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// loadConfig loads the runtime config from the given path, or auto-detects it.
func loadConfig(path string) (*config.Config, error) {
	if path == "" {
		path = defaultConfigPath()
	}
	return config.Load(path)
}

// ── Misc utilities ────────────────────────────────────────────────────────────

// rotateAuditLog renames audit.jsonl → audit-<date>.jsonl.
func rotateAuditLog(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}
	dest := strings.TrimSuffix(path, ".jsonl") + "-" + time.Now().Format("2006-01-02") + ".jsonl"
	_ = os.Rename(path, dest)
}

// currentUser returns the current OS username, or "unknown".
func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("LOGNAME"); u != "" {
		return u
	}
	return "unknown"
}

// truncate shortens s to max runes, appending "…" if truncated.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
