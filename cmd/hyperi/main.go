// Package main is the CLI entry point for HyperiOS.
//
// Primary interface: `hyperi serve` (also the default action with no
// subcommand) starts the HTTP API server and the background goal-processing
// loop. Any UI — web, CLI, mobile — talks to the agent through this API;
// see internal/apiserver for the route list.
//
// `hyperi` is a fire-and-forget system: submitting a goal via the API queues
// it for background execution immediately (unless marked as a draft); the
// caller polls for results rather than blocking on a request.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/isellar/hyperios/internal/apiserver"
	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/config"
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

	root := &cobra.Command{
		Use:   "hyperi",
		Short: "HyperiOS — AI-driven agent server",
		Long: `hyperi is the HyperiOS agent server.

Running without a subcommand is equivalent to 'hyperi serve': it starts the
HTTP API and begins processing queued goals in the background. Any UI talks
to the agent through the API — see internal/apiserver for the route list.`,
		Version: version,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cfgPath, defaultServeAddr)
		},
	}

	root.PersistentFlags().StringVar(&cfgPath, "config", "", "Path to config.json (default: auto-detected)")

	root.AddCommand(buildServeCmd())
	root.AddCommand(buildConfigCmd())
	root.AddCommand(buildModelsCmd())
	root.AddCommand(buildSelfModifyCmd())
	root.AddCommand(buildVersionCmd())

	return root
}

// ── serve command ─────────────────────────────────────────────────────────────

const defaultServeAddr = ":8080"

func buildServeCmd() *cobra.Command {
	var (
		cfgPath string
		addr    string
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HyperiOS agent HTTP API server",
		Long: `Starts the HTTP API server and the background goal-processing loop.

Goals submitted via POST /api/goals are queued and executed asynchronously;
poll GET /api/goals/{id} or GET /api/goals/{id}/result to observe progress.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cfgPath, addr)
		},
	}

	cmd.Flags().StringVar(&cfgPath, "config", "", "Path to config.json")
	cmd.Flags().StringVar(&addr, "addr", defaultServeAddr, "HTTP listen address")
	return cmd
}

// runServe loads config, wires all modules, and blocks serving the API until
// interrupted (SIGINT/SIGTERM) or the server errors.
func runServe(cfgPath, addr string) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	llmClient := buildLLMClient(cfg)

	auditLogPath := filepath.Join(resolveLogDir(), "audit.jsonl")
	auditLog, err := audit.NewLogger(auditLogPath)
	if err != nil {
		return fmt.Errorf("create audit logger: %w", err)
	}

	modules, err := WireModules(cfg, llmClient, auditLog)
	if err != nil {
		return fmt.Errorf("wire modules: %w", err)
	}

	srv := apiserver.New(addr, &apiserver.Modules{
		GoalFulfillment: modules.GoalFulfillment,
		Processor:       modules.Processor,
		Memory:          modules.Memory,
		SelfImprovement: modules.SelfImprovement,
		IOToolbox:       modules.IOToolbox,
		Config:          cfg,
		ResultStore:     modules.ResultStore,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("hyperi %s starting on %s", version, addr)
	return srv.Start(ctx)
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

Keys: autonomy_level, llm_provider, llm_api_key, llm_model,
      goal_timeout_minutes, max_tool_iterations,
      local_model_num_ctx, local_model_keep_alive
(local model keys not listed here — see 'hyperi models status')`,
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
			case "llm_provider":
				fmt.Printf("%s\n", providerOrDefault(cfg.LLMProvider))
			case "llm_api_key":
				fmt.Printf("%s\n", maskKey(cfg.LLMAPIKey))
			case "llm_model":
				fmt.Printf("%s\n", cfg.LLMModel)
			case "goal_timeout_minutes":
				fmt.Printf("%d\n", cfg.GoalTimeoutMinutes)
			case "max_tool_iterations":
				fmt.Printf("%d\n", cfg.MaxToolIterations)
			case "local_model_num_ctx":
				if cfg.LocalModelNumCtx > 0 {
					fmt.Printf("%d\n", cfg.LocalModelNumCtx)
				} else {
					fmt.Println("auto (recomputed from hardware at server startup)")
				}
			case "local_model_keep_alive":
				fmt.Printf("%s\n", orDefault(cfg.LocalModelKeepAlive, "30m"))
			default:
				return fmt.Errorf("unknown config key %q\nRun 'hyperi config get --help' for the list of keys", key)
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
			case "goal_timeout_minutes":
				n, err := strconv.Atoi(val)
				if err != nil || n <= 0 {
					return fmt.Errorf("goal_timeout_minutes must be a positive integer, got %q", val)
				}
				cfg.GoalTimeoutMinutes = n
				fmt.Printf("goal_timeout_minutes set to %d\n", n)
			case "max_tool_iterations":
				n, err := strconv.Atoi(val)
				if err != nil || n <= 0 {
					return fmt.Errorf("max_tool_iterations must be a positive integer, got %q", val)
				}
				cfg.MaxToolIterations = n
				fmt.Printf("max_tool_iterations set to %d\n", n)
			case "local_model_num_ctx":
				if val == "auto" || val == "0" {
					cfg.LocalModelNumCtx = 0
					fmt.Println("local_model_num_ctx set to auto")
				} else {
					n, err := strconv.Atoi(val)
					if err != nil || n <= 0 {
						return fmt.Errorf("local_model_num_ctx must be a positive integer or \"auto\", got %q", val)
					}
					cfg.LocalModelNumCtx = n
					fmt.Printf("local_model_num_ctx set to %d\n", n)
				}
			case "local_model_keep_alive":
				cfg.LocalModelKeepAlive = val
				fmt.Printf("local_model_keep_alive set to %q\n", val)
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
			fmt.Printf("  autonomy_level       %d  (%s)\n",
				cfg.AutonomyLevel, config.AutonomyLevelText(cfg.AutonomyLevel))
			fmt.Printf("  llm_provider         %s\n", providerOrDefault(cfg.LLMProvider))
			fmt.Printf("  llm_api_key          %s\n", maskKey(cfg.LLMAPIKey))
			fmt.Printf("  llm_model            %s\n", cfg.LLMModel)
			fmt.Printf("  goal_timeout_minutes %d\n", cfg.GoalTimeoutMinutes)
			fmt.Printf("  max_tool_iterations  %d\n", cfg.MaxToolIterations)
			fmt.Println("\n  (local model settings: see 'hyperi models status')")
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

// loadConfig loads the runtime config from the given path, or auto-detects it.
func loadConfig(path string) (*config.Config, error) {
	if path == "" {
		path = defaultConfigPath()
	}
	return config.Load(path)
}

// resolveAPIKey returns the API key to use for cfg.LLMProvider.
// cfg.LLMAPIKey (set via 'hyperi config set llm_api_key ...') takes
// precedence; otherwise fall back to the env var for whichever provider is
// configured. This lets a user with an exhausted ANTHROPIC_API_KEY switch to
// OpenCode Zen without unsetting anything.
func resolveAPIKey(cfg *config.Config) string {
	if cfg.LLMAPIKey != "" {
		return cfg.LLMAPIKey
	}
	switch cfg.LLMProvider {
	case config.ProviderOpenCodeZen:
		return os.Getenv("OPENCODE_API_KEY")
	default:
		return os.Getenv("ANTHROPIC_API_KEY")
	}
}

// ── Misc utilities ────────────────────────────────────────────────────────────

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
